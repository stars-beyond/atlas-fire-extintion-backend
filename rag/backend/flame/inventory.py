"""Answer collection questions from the validated corpus, never a search sample."""
import re
import unicodedata


def build_inventory(chunks):
    documents, data = {}, {}
    for chunk in chunks:
        meta = chunk["metadata"]
        table = ("Experimental table" in str(meta.get("source_type", ""))
                 or meta.get("data_category") == "Experimental table")
        if table or meta.get("document_role") == "data_card":
            psi = meta.get("psi_id")
            entry = data.setdefault(meta["document_id"], {
                "title": " ".join((meta.get("title") or meta["document_id"]).split()),
                "investigation": " · ".join(filter(None, ["; ".join(psi) if isinstance(psi, list) else psi,
                                                          meta.get("experiment")])) or "?",
                "kind": "file", "category": None, "rows": set(), "card_rows": 0})
            entry["kind"] = "table" if table or entry["kind"] == "table" else "file"
            if meta.get("document_role") == "data_card":
                entry.update(category=meta.get("data_category"), card_rows=meta.get("data_rows") or 0)
            elif meta.get("row") is not None:
                entry["rows"].add(meta["row"])
        document = documents.setdefault(meta["document_id"], {
            "document_id": meta["document_id"],
            "title": " ".join((meta.get("title") or meta["document_id"]).split()),
            "psi_id": None,
        })
        # Only an explicitly identified investigation record counts as a PSI
        # record; a PDF/table mentioning PSI is not another investigation.
        if (meta.get("source") == "NASA PSI" and
                meta.get("source_type") == "Investigation description (not full paper or raw dataset)" and
                re.fullmatch(r"PSI-\d+", meta.get("psi_id") or "")):
            document["psi_id"] = meta["psi_id"]
        if meta.get("source") == "NASA NTRS" and meta.get("document_role"):
            document.update(role=meta["document_role"], study_title=meta.get("study_title"),
                            # Documents about the same investigation count as one study.
                            study=meta.get("study_group") or meta["document_id"])
    result = {
        "documents": sorted(documents.values(), key=lambda d: d["document_id"]),
        "document_count": len(documents),
        "psi_record_count": len({d["psi_id"] for d in documents.values() if d["psi_id"]}),
        "chunks": len(chunks),
    }
    ntrs = [d for d in documents.values() if d.get("study")]
    if ntrs:
        result["ntrs"] = {
            "documents": len(ntrs), "studies": len({d["study"] for d in ntrs}),
            "grouped_documents": sum(1 for d in ntrs if d["study_title"]),
            "bibliographies": sum(1 for d in ntrs if d["role"] == "bibliography"),
            "abstract_only": sum(1 for d in ntrs if d["role"] == "abstract_only"),
        }
    if data:
        result["data"] = sorted(({"document_id": key, "title": e["title"], "investigation": e["investigation"],
                                  "kind": e["kind"], "category": e["category"],
                                  "rows": max(len(e["rows"]), e["card_rows"])} for key, e in data.items()),
                                key=lambda e: (e["investigation"], e["kind"], e["title"]))
    return result


def _normalize(text):
    plain = unicodedata.normalize("NFKD", text.casefold())
    plain = "".join(c for c in plain if not unicodedata.combining(c))
    return " ".join(re.sub(r"[^a-z0-9]+", " ", plain).split())


def inventory_request(query, history=()):
    """Conservative whole-question grammar: scientific/mixed questions fall through."""
    text = _normalize(query)
    pt_noun = r"(?:estudos?|artigos?|documentos?|fontes?|registros?|investigacao|investigacoes|trechos?)"
    pt_scope = r"(?:(?:do |da |de )?psi|do repositorio psi)"
    pt_location = r"(?:(?:na|da|em|dentro da) (?:sua |nossa |a )?(?:base(?: de (?:dados|conhecimento))?|colecao)|(?:no|do) (?:seu |nosso )?rag)"
    pt_access = rf"(?:(?:(?:voce|voces|o rag|o chat) )?(?:ja )?(?:tem|possui|conhece|consegue acessar|pode acessar)(?: acesso(?: a)?| disponivel| disponiveis)?|(?:ja )?(?:estao|foram) (?:indexados|extraidos|integrados|carregados|disponiveis)|existem|ha)(?: {pt_location})?"
    pt_tail = rf"(?: {pt_access}| {pt_location})?(?: (?:ao|no) todo| no total| atualmente| agora| hoje| ja)?"
    en_noun = r"(?:studies|study|papers?|documents?|sources?|records?|investigations?|chunks?)"
    en_location = r"(?:in|of|from) (?:the |your |our )?(?:database|knowledge base|corpus|collection|rag|index)"
    en_access = rf"(?:do you (?:have(?: access to)?|know)|can you access|are (?:available|indexed|loaded)(?: {en_location})?|are there(?: {en_location})?|are {en_location}|{en_location})"
    pt_data = (r"(?:dados|datasets?|tabelas(?: de dados| experimentais| de ensaios)?|arquivos de dados|conjuntos de dados|planilhas)"
               r"(?: (?:numericos|experimentais|brutos|tabulares|quantitativos|cientificos|medidos))?")
    en_data = r"(?:(?:numeric|numerical|experimental|raw|tabular|measured) )?(?:data|datasets?|data files|tables|spreadsheets)"
    patterns = (
        ("pt", "data", rf"(?:quais|que|quantos|quantas) (?:sao (?:os |as )?)?{pt_data}{pt_tail}"),
        ("pt", "data", rf"(?:liste|listar|mostre|me mostre) (?:(?:todos|todas) )?(?:os |as )?{pt_data}{pt_tail}"),
        ("pt", "data", rf"o que (?:voce )?tem de {pt_data}(?: {pt_location})?"),
        ("en", "data", rf"(?:what|which|list|show me)(?: all)?(?: the)? {en_data}(?: {en_access}| {en_location})?"),
        ("pt", "count", rf"(?:a )?quant[oa]s? (?:{pt_noun})(?: {pt_scope})?{pt_tail}"),
        ("pt", "count", rf"(?:qual (?:e )?(?:o )?total de|(?:voce )?(?:tem acesso a|consegue acessar|pode acessar) quantos) {pt_noun}(?: {pt_scope})?{pt_tail}"),
        ("pt", "list", rf"(?:quais (?:sao (?:os |as )?)?|(?:liste|listar|mostre|me mostre) (?:(?:todos|todas) )?(?:os |as )?){pt_noun}(?: {pt_scope})?{pt_tail}"),
        ("pt", "list", rf"o que (?:(?:voce|voces) )?(?:tem|ha|existe)(?: disponivel)? {pt_location}"),
        ("en", "count", rf"how many (?:psi )?{en_noun}(?: {en_access})?(?: in total| currently| now)?"),
        ("en", "list", rf"(?:which |what (?:are (?:the )?)?|list (?:all )?(?:the )?|show me (?:all )?(?:the )?)(?:psi )?{en_noun}(?: {en_access}| {en_location})?"),
    )
    for language, mode, pattern in patterns:
        if re.fullmatch(pattern, text):
            return {"language": language, "mode": mode,
                    "scope": "psi" if re.search(r"\bpsi\b", text) else "all"}
    # A short catalog follow-up refers only to the immediately preceding user
    # request, so an intervening scientific question cannot inherit this route.
    if text in {"quais sao eles", "quais sao elas", "liste todos", "liste todas",
                "list them", "which ones", "show them"}:
        previous = next((m.content for m in reversed(history) if m.role == "user"), "")
        request = inventory_request(previous) if previous else None
        if request:
            return request if request["mode"] == "data" else {**request, "mode": "list"}
    return None


def data_response(inventory, request):
    # Answers are always in English (the UI language), whatever the question language.
    entries = inventory.get("data") or []
    if not entries:
        return {"answer": "The active collection has no catalogued data files.", "sources": []}
    tables = [e for e in entries if e["kind"] == "table"]
    files = [e for e in entries if e["kind"] == "file"]
    investigations = {}
    for entry in entries:
        investigations.setdefault(entry["investigation"], []).append(entry)
    answer = (f"The active collection has **{len(tables)} experimental tables** "
              f"({sum(e['rows'] for e in tables)} rows) and **{len(files)} other data files** with a data card "
              f"({sum(e['rows'] for e in files)} exported rows) in **{len(investigations)} investigations**. "
              "Data cards give columns, counts and automatic minimum/maximum values; complete values stay "
              "in the originals and the dataset SQLite. Ask for a quantity to see values, e.g. "
              "“Which oxygen concentrations were tested?”\n\n**By investigation:**")
    escape = lambda text: re.sub(r"([\\`*_{}\[\]<>()#!|])", r"\\\1", text)
    for name, members in sorted(investigations.items()):
        table_count = sum(1 for e in members if e["kind"] == "table")
        categories = {}
        for entry in members:
            if entry["kind"] == "file":
                categories[entry["category"] or "?"] = categories.get(entry["category"] or "?", 0) + 1
        parts = []
        if table_count:
            rows = sum(e["rows"] for e in members if e["kind"] == "table")
            parts.append(f"{table_count} experimental table(s), {rows} rows")
        if categories:
            listed = ", ".join(f"{escape(category)} {count}" for category, count in sorted(categories.items()))
            parts.append(f"data files: {listed}")
        answer += f"\n- **{escape(name)}**: " + "; ".join(parts)
    return {"answer": answer, "sources": []}


def inventory_response(inventory, request):
    if request["mode"] == "data":
        return data_response(inventory, request)
    docs = inventory["documents"]
    psi_only = request["scope"] == "psi"
    if psi_only:
        docs = list({d["psi_id"]: d for d in docs if d["psi_id"]}.values())
    answer = (f'The active collection contains **{inventory["document_count"]} sources/documents**, '
              f'with **{inventory["chunks"]} indexed chunks**, including '
              f'**{inventory["psi_record_count"]} descriptive PSI investigation records**. '
              'Sources include reports, guides, pages, descriptions and tables; '
              'these totals do not mean that every source is an independent study or a full paper. '
              'Counts come from the complete active corpus inventory, not from search results.')
    label = "PSI records" if psi_only else "Indexed sources/documents"
    ntrs = inventory.get("ntrs")
    if ntrs and not psi_only:
        answer += (f' It includes **{ntrs["documents"]} NASA NTRS documents** corresponding to '
                   f'**{ntrs["studies"]} studies/investigations**: {ntrs["grouped_documents"]} documents '
                   'about the same investigation are grouped and counted once. Among them are '
                   f'{ntrs["bibliographies"]} reference bibliography and {ntrs["abstract_only"]} '
                   'abstract-only record.')
    if request["mode"] == "list":
        # Titles are corpus data; escape Markdown rather than treating them as
        # links, citations, HTML or instructions.
        def escape(text):
            return re.sub(r"([\\`*_{}\[\]<>()#!|])", r"\\\1", text)

        def title(document):
            label = escape(document["title"])
            if document.get("study_title"):
                label += " — study: " + escape(document["study_title"])
            if document.get("role") == "bibliography":
                label += " (reference bibliography)"
            return label
        answer += f"\n\n**{label} ({len(docs)}):**\n\n" + "\n".join(f"- {title(d)}" for d in docs)
    return {"answer": answer, "sources": []}
