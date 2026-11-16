"""Grounded generation using native OpenAI and Qdrant clients."""
import json
import logging
import re
from collections import Counter
from types import SimpleNamespace
from backend.flame.config import openai_client, settings, vector_client
from backend.flame.index import fingerprint, prepare, validate_collection
from backend.flame.inventory import build_inventory, inventory_request, inventory_response
from backend.flame.formulas import (build_formula_catalog, common_formula_request, formula_context,
                                    formula_question, frequency_sources, load_machine_items)
from backend.flame.formula_transcribe import group_items
from backend.flame.conditions import build_conditions, condition_request, condition_sources
from .display_en import metadata_en, text_en
from .flame_prompt import messages

logger = logging.getLogger(__name__)
rag_pipeline = {}

class RagUnavailable(Exception):
    pass

class GenerationError(Exception):
    pass

def close_pipeline():
    for key in ("vector", "llm"):
        client = rag_pipeline.pop(key, None)
        if client:
            client.close()

def setup_rag_pipeline():
    close_pipeline()
    rag_pipeline.clear()
    rag_pipeline.update(ready=False, detail="Configure the research knowledge base.", chunks=0)
    try:
        config = settings()
        rag_pipeline["config"] = config
        validate_collection(config["collection"])
        if not config["api_key"]:
            raise RagUnavailable("Set OPENAI_API_KEY in backend/aplication/.env, import the documents and run the indexing.")
        if not config["corpus"].is_file():
            raise RagUnavailable("Corpus missing. Import the PDFs with python -m backend.flame.ingest_local.")
        documents = json.loads(config["corpus"].read_text(encoding="utf-8"))
        chunks = prepare(documents)
        if not chunks:
            raise RagUnavailable("The corpus is empty. Import documents with extractable text.")
        vector = vector_client(config)
        rag_pipeline["vector"] = vector
        if not vector.collection_exists(config["collection"]):
            raise RagUnavailable("Collection missing. Run python -m backend.flame.index after importing the documents.")
        count = vector.count(config["collection"], exact=True).count
        points, _ = vector.scroll(config["collection"], limit=1, with_payload=True, with_vectors=False)
        if not points or count != len(chunks):
            raise RagUnavailable("Indexing is incomplete. Stop the API and resume python -m backend.flame.index.")
        payload = points[0].payload
        if payload.get("corpus_revision") != fingerprint(chunks) or payload.get("embedding_model") != config["embedding_model"]:
            raise RagUnavailable("The collection uses a different corpus/model. Configure a new collection and run the indexing.")
        inventory = build_inventory(chunks)
        # Automatic transcriptions count only where they match a page of this corpus revision.
        machine = load_machine_items(config.get("formulas"), documents)
        rag_pipeline.update(llm=openai_client(config), inventory=inventory,
                            bibliography=any(c["metadata"].get("document_role") == "bibliography" for c in chunks),
                            data_cards=any(c["metadata"].get("document_role") == "data_card" for c in chunks),
                            formula_catalog=build_formula_catalog(documents, machine=machine),
                            formula_groups=group_items(machine),
                            conditions=build_conditions(documents, machine),
                            ready=True, detail="Knowledge base ready for queries.", chunks=count)
    except RagUnavailable as error:
        rag_pipeline["detail"] = str(error)
    except Exception:
        logger.exception("Falha ao iniciar o RAG")
        rag_pipeline["detail"] = "Could not open Qdrant or the configuration. Check the .env file and stop any other process using the local database."
    if not rag_pipeline["ready"]:
        close_pipeline()

def status():
    return {"ready": rag_pipeline.get("ready", False), "detail": rag_pipeline.get("detail", "Knowledge base not started yet."), "chunks": rag_pipeline.get("chunks", 0)}

def retrieval_query(query, history):
    # History resolves references; it is never scientific evidence.
    followup = re.search(r"^(e quanto|e sobre|and what|what about)\b", query, re.I)
    explicit_subject = named_experiments(query) or re.search(r"\bPSI[-\s]?\d+\b|\bApollo[-\s]+\d+\b", query, re.I)
    if explicit_subject and not followup:
        return query
    # 'Conte outro' / 'another one' continue the previous request.
    if re.search(r"\b(ele|ela|eles|elas|esse|essa|esses|essas|isso|nesse|nessa|nesses|nessas|desses|dessas|its|it|those|these|"
                 r"outr[oa]s?|mais um[a]?|another|other)\b|^(e quanto|e sobre|and what|what about)\b", query, re.I):
        previous = next((m.content for m in reversed(history) if m.role == "user"), "")
        return f"{previous}\n{query}" if previous else query
    return query

def named_experiments(query):
    groups = []
    versions = {"1":"I", "2":"II", "3":"III", "4":"IV", "5":"V", "6":"VI"}
    pattern = r"\b(BASS|FLEX|SAFFIRE)(?:[-\s]*(III|II|IV|VI|V|I|[1-6]))?\b(?![-\w])"
    for match in re.finditer(pattern, query, re.I):
        name, version = match.group(1).upper(), (match.group(2) or "").upper()
        version = versions.get(version, version)
        if name == "BASS":
            values = ["BASS", "BASS-II"] if not version else ["BASS" if version == "I" else "BASS-" + version]
        elif name == "FLEX":
            values = ["FLEX", "FLEX-2"] if not version else ["FLEX" if version == "I" else "FLEX-" + next((k for k,v in versions.items() if v == version), version)]
        else:
            # Some reports cover the Saffire IV and V flights together.
            values = ["Saffire", *["Saffire-"+v for v in versions.values()], "Saffire-IV/V"] if not version else ["Saffire-" + version]
            if version in ("IV", "V"):
                values.append("Saffire-IV/V")
        if values not in groups:
            groups.append(values)
    extra = {
        "ACME": ["ACME"], "SLICE": ["SLICE"], "SPICE": ["SPICE"],
        "SAME": ["SAME"], "SAME-R": ["SAME-R"], "CFI": ["CFI"],
        "CFI-G": ["ACME CFI-G"], "DAFT": ["DAFT / DAFT-2"],
        "SOFIE": ["SoFIE", "SoFIE-GEL"], "SOFIE-GEL": ["SoFIE-GEL"],
        "LUCI": ["LUCI"], "WIF": ["WIF"], "CSD": ["CSD"],
    }
    for match in re.finditer(r"\b(SAME-R|CFI-G|SOFIE-GEL|ACME|SLICE|SPICE|SAME|CFI|DAFT|SOFIE|LUCI|WIF|CSD)\b(?![-\w])", query, re.I):
        name = match.group(1).upper()
        # SAME/SLICE/SPICE are also ordinary English words. Treat lowercase
        # occurrences as acronyms only with an explicit experiment/document cue.
        if name in {"SAME", "SLICE", "SPICE"} and match.group(1) != name:
            cue = r"\b(?:experiment|experimento|investigation|document|documento|pdf)\W+(?:archived\W+|arquivado\W+)?" + re.escape(name) + r"\b"
            if not re.search(cue, query, re.I):
                continue
        values = extra[name]
        if values not in groups:
            groups.append(values)
    return groups

def psi_constraints(query):
    ids = list(dict.fromkeys("PSI-" + m for m in re.findall(r"\bPSI[-\s]?(\d+)\b", query, re.I)))
    patterns = {
        "objective": r"\b(?:objetiv\w*|objectiv\w*)\b",
        "approach": r"\b(?:abordage\w*|approach\w*|metodolog\w*)\b",
        "description": r"\b(?:descri[çc][ãa]o|description)\b",
        "hypothesis": r"\b(?:hip[oó]tes\w*|hypothes\w*)\b",
        "researchImpacts": r"\b(?:impact\w*)\b",
        "experimentHardware": r"\b(?:hardware|equipamento\w*)\b",
    }
    return ids, [section for section, pattern in patterns.items() if re.search(pattern, query, re.I)]

LITERATURE = re.compile(r"\b(?:bibliograf\w*|refer[êe]ncias?|references?|literatur[ae]|publica[çc](?:[ãa]o|[õo]es)"
                        r"|publications?|cita[çc](?:[ãa]o|[õo]es)|citations?)\b", re.I)


def bibliography_condition(qm):
    return qm.FieldCondition(key="metadata.document_role", match=qm.MatchValue(value="bibliography"))


# Data cards are Portuguese statistics of data files: for other questions they win on
# language alone and crowd out the English reports.
DATA_QUESTION = re.compile(r"\b(?:dados|datasets?|data|arquivos?|planilhas?|tabelas?|colunas?|columns?|"
                           r"spreadsheets?|files?|\.dat|csv|xlsx?|fichas?)\b", re.I)


def excluded_roles(query):
    roles = []
    if rag_pipeline.get("bibliography"):
        roles.append("bibliography")
    if rag_pipeline.get("data_cards") and not DATA_QUESTION.search(query):
        roles.append("data_card")
    return roles


def citation_numbers(answer):
    # Array indices inside math/code are not evidence citations.
    prose = re.sub(r'```[\s\S]*?```|~~~[\s\S]*?~~~|`[^`\n]*`|\$\$[\s\S]*?\$\$|\$(?:\\.|[^$\\\n])+\$', '', answer)
    return {int(n) for n in re.findall(r'\[(\d+)\]', prose)}


def _prepare(user_query, chat_history):
    """Busca as evidências e monta o prompt. Devolve ("pronto", resposta) quando não há o que gerar,
    ou ("gerar", contexto) com prompt, fontes, esquema de saída e opções do modelo."""
    if not rag_pipeline.get("ready"):
        raise RagUnavailable(status()["detail"])
    request = inventory_request(user_query, chat_history)
    if request:
        inventory = rag_pipeline.get("inventory")
        if inventory is None:
            raise RagUnavailable("Inventory unavailable. Restart the API to validate the active knowledge base.")
        return "pronto", inventory_response(inventory, request)
    config = rag_pipeline["config"]
    llm, vector = rag_pipeline["llm"], rag_pipeline["vector"]
    try:
        query = retrieval_query(user_query, chat_history)
        embedding = llm.embeddings.create(model=config["embedding_model"], input=[query]).data[0].embedding
        from qdrant_client import models as qm
        # A long reference list would crowd out experimental evidence; it is only
        # consulted when the question asks for literature or references.
        has_bibliography = rag_pipeline.get("bibliography", False)
        roles = excluded_roles(query)
        general = qm.Filter(must_not=[qm.FieldCondition(key="metadata.document_role", match=qm.MatchAny(any=roles))]) if roles else None
        found = vector.query_points(config["collection"], query=embedding, query_filter=general, limit=32, score_threshold=config["threshold"], with_payload=True).points
        # Explicit comparisons reserve context for each mentioned family. Scores remain
        # retrieval scores; these filters do not assert that conditions are comparable.
        reserved = []
        if has_bibliography and LITERATURE.search(query):
            reserved.extend(vector.query_points(config["collection"], query=embedding,
                query_filter=qm.Filter(must=[bibliography_condition(qm)]), limit=2,
                score_threshold=config["threshold"], with_payload=True).points)
        psi_ids, sections = psi_constraints(query)
        for psi_id in psi_ids:
            match_id = qm.FieldCondition(key="metadata.psi_id", match=qm.MatchValue(value=psi_id))
            # Explicitly requested sections must not be displaced by similar text
            # from another investigation (or another section of the same record).
            for section in sections:
                reserved.extend(vector.query_points(config["collection"], query=embedding,
                    query_filter=qm.Filter(must=[match_id, qm.FieldCondition(key="metadata.section", match=qm.MatchValue(value=section))]),
                    limit=1, score_threshold=config["threshold"], with_payload=True).points)
            reserved.extend(vector.query_points(config["collection"], query=embedding,
                query_filter=qm.Filter(must=[match_id]), limit=2,
                score_threshold=config["threshold"], with_payload=True).points)
        for values in named_experiments(query):
            reserved.extend(vector.query_points(config["collection"], query=embedding,
                query_filter=qm.Filter(must=[qm.FieldCondition(key="metadata.experiment", match=qm.MatchAny(any=values))]),
                limit=2, score_threshold=config["threshold"], with_payload=True).points)
        mathematical = formula_context(rag_pipeline.get("formula_catalog", []), query,
                                       psi_ids, named_experiments(query))
        math_pages = {(point.payload['metadata']['document_id'], point.payload['metadata']['page'])
                      for point in mathematical}
        # Values used across experiments come from the whole conditions table:
        # similarity search alone would only see a few documents.
        quantities, coverage, tabulated, formula_coverage = condition_request(user_query), None, [], None
        if quantities and rag_pipeline.get("conditions"):
            summaries, coverage = condition_sources(rag_pipeline["conditions"], quantities,
                                                    named_experiments(query), psi_ids)
            tabulated = [SimpleNamespace(id=f"conditions:{n}", score=None, payload=s) for n, s in enumerate(summaries)]
        elif rag_pipeline.get("formula_groups") and (common_formula_request(user_query) or (
                formula_question(query) and not mathematical)):
            # "Common formulas" are counted over the whole transcription catalog, not a search
            # sample; a formula question with no matching page also starts from the catalog.
            tabulated, formula_coverage = frequency_sources(rag_pipeline["formula_groups"],
                                                            named_experiments(query), psi_ids)
        limit = len(tabulated) + 2 if tabulated else 8
        selected, counts = [], Counter()
        seen = set()
        for point in tabulated + mathematical + reserved + found:
            meta = point.payload.get("metadata", {})
            key = meta.get("document_id")
            if point.score is not None and (key, meta.get('page')) in math_pages:
                continue
            if counts[key] >= 3 or str(point.id) in seen:
                continue
            seen.add(str(point.id))
            counts[key] += 1
            selected.append(point)
            if len(selected) == limit:
                break
        if not selected:
            return "pronto", {"answer": "I did not find enough evidence in the consulted documents to answer this question.", "sources": []}
        docs, sources = [], []
        for number, point in enumerate(selected, 1):
            metadata = point.payload["metadata"]
            text = point.payload["page_content"]
            docs.append(SimpleNamespace(page_content=text, metadata=metadata))
            sources.append({**metadata_en(metadata), "citation": number,
                            "retrieval_score": float(point.score) if point.score is not None else None, "excerpt": text_en(text)})
        prompt = [{"role": "system" if role == "system" else "user", "content": content} for role, content in messages(user_query, chat_history, docs, rag_pipeline.get("inventory"), coverage, formula_coverage)]
        output_schema = {"type":"json_schema", "json_schema":{"name":"grounded_answer", "strict":True, "schema":{
            "type":"object", "properties":{"answer":{"type":"string"}, "evidence_sufficient":{"type":"boolean"}},
            "required":["answer","evidence_sufficient"], "additionalProperties":False,
        }}}
        options = {"reasoning_effort":"low"} if config["chat_model"].startswith("gpt-5") or config["chat_model"] == "gpt-6-luna" else {}
        return "gerar", {"prompt": prompt, "sources": sources, "schema": output_schema, "options": options}
    except Exception as error:
        _raise_generation(error)


def _finish(content, finish_reason, sources):
    """Valida a resposta estruturada do modelo e marca as fontes citadas."""
    if not content or finish_reason != "stop":
        raise GenerationError("The model did not finish the answer. Try a more specific question.")
    try:
        parsed = json.loads(content)
        answer = parsed["answer"].strip()
        sufficient = parsed["evidence_sufficient"]
        if not answer or not isinstance(sufficient, bool):
            raise ValueError("Invalid structured answer")
    except (ValueError, KeyError, TypeError, AttributeError) as error:
        raise GenerationError("The model returned an invalid answer. Please try again.") from error
    cited = citation_numbers(answer)
    if (sufficient and not cited) or not cited.issubset(set(range(1, len(sources) + 1))):
        raise GenerationError("The answer came back without valid references. Try rephrasing the question.")
    # Every retrieved excerpt keeps its number; the interface lists cited ones first.
    for source in sources:
        source["cited"] = source["citation"] in cited
    return {"answer": answer, "sources": sources}


def _raise_generation(error):
    """Traduz falhas da busca ou do modelo para mensagens que a interface mostra."""
    logger.warning("Falha na consulta RAG (%s)", type(error).__name__)
    from openai import AuthenticationError, RateLimitError
    if isinstance(error, AuthenticationError):
        raise GenerationError("The OpenAI key was not accepted. Check OPENAI_API_KEY in .env.") from error
    if isinstance(error, RateLimitError):
        raise GenerationError("The OpenAI API hit its usage or billing limit. Check the account and try again.") from error
    raise GenerationError("Failed to query the knowledge base or the model. Try again and check the API log.") from error


def _create(ctx, **extra):
    config = rag_pipeline["config"]
    return rag_pipeline["llm"].chat.completions.create(model=config["chat_model"], messages=ctx["prompt"], max_completion_tokens=3500,
                                                      response_format=ctx["schema"], **ctx["options"], **extra)


def get_ai_response(user_query, chat_history):
    kind, value = _prepare(user_query, chat_history)
    if kind == "pronto":
        return value
    try:
        choice = _create(value).choices[0]
        return _finish(choice.message.content, choice.finish_reason, value["sources"])
    except GenerationError:
        raise
    except Exception as error:
        _raise_generation(error)


_ANSWER_KEY = re.compile(r'"answer"\s*:\s*"')


class AnswerStream:
    """Extrai, aos pedaços, o texto do campo "answer" de um JSON que chega em partes (saída estruturada em streaming)."""

    def __init__(self):
        self.raw, self.start, self.sent, self.done = "", None, 0, False

    def feed(self, piece):
        self.raw += piece
        if self.done:
            return ""
        if self.start is None:
            match = _ANSWER_KEY.search(self.raw)
            if not match:
                return ""
            self.start = match.end()
        body, end, escaped = self.raw[self.start:], None, False
        for i, ch in enumerate(body):
            if escaped:
                escaped = False
            elif ch == "\\":
                escaped = True
            elif ch == '"':
                end = i
                break
        segment = body if end is None else body[:end]
        self.done = end is not None
        text = None
        for cut in range(0, 7):  # um escape (\n, \uXXXX) pode estar pela metade no fim do pedaço
            try:
                text = json.loads('"' + segment[:len(segment) - cut] + '"')
                break
            except ValueError:
                continue
        if text is None or len(text) <= self.sent:
            return ""
        new, self.sent = text[self.sent:], len(text)
        return new


def stream_ai_response(user_query, chat_history):
    """Como get_ai_response, mas em eventos: ("texto", pedaço) enquanto o modelo escreve e ("pronto", resposta)
    no fim, já validada. Se a validação falhar, levanta GenerationError depois do texto parcial."""
    kind, value = _prepare(user_query, chat_history)
    if kind == "pronto":
        yield "texto", value["answer"]
        yield "pronto", value
        return
    parts, finish, reader = [], None, AnswerStream()
    try:
        for chunk in _create(value, stream=True):
            if not chunk.choices:
                continue
            choice = chunk.choices[0]
            piece = choice.delta.content or ""
            if piece:
                parts.append(piece)
                new = reader.feed(piece)
                if new:
                    yield "texto", new
            if choice.finish_reason:
                finish = choice.finish_reason
    except GenerationError:
        raise
    except Exception as error:
        _raise_generation(error)
    yield "pronto", _finish("".join(parts), finish, value["sources"])
