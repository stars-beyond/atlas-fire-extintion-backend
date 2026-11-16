"""Transcribe PDF equation candidates to LaTeX from high-resolution crops.

Candidates are text blocks with mathematical signs: the PSI dataset's equation
candidates plus the same detection on the other PDFs of the active corpus (NTRS,
NASA resources). The model sees the rendered crop and the PDF text layer and
classifies every item as formula, value or definition. Every transcription is
machine-made and unreviewed; crops are kept so a person can check each item.
Run a sampled pilot first (--sample) to measure cost and accuracy.
"""
import argparse
import base64
import hashlib
import json
import random
import re
import sqlite3
import threading
import time
from collections import defaultdict
from concurrent.futures import ThreadPoolExecutor, as_completed
from datetime import datetime, timezone
from pathlib import Path

from .config import ROOT, openai_client, settings
from .pipeline import write_json

ZOOM = 3.5
# Pilot: 7 of 50 crops cut a superscript, a unit or the line end.
PADDING_X, PADDING_Y = 36, 14
MATH = re.compile(r"[=∑∫∂√∝]")
KINDS = ("formula", "value", "definition")
PROMPT = (
    "This image is a block cropped from a scientific PDF page about combustion or fire safety. "
    "The PDF text layer of the same block is given for reference; it often loses fractions, "
    "subscripts and superscripts, so trust the image. Transcribe every mathematical expression "
    "with '=' or another relation exactly as printed, in LaTeX without $ delimiters. Do not correct, "
    "simplify or complete expressions; skip references, code, URLs and isolated table cells. "
    "Classify each item: kind='formula' for a relation between quantities (physical law, model or "
    "balance equation, empirical fit or correlation, calibration polynomial, chemical reaction); "
    "kind='value' for a quantity set equal to a number, with or without unit; kind='definition' for a "
    "symbol, acronym or label explained in words. Give the printed equation number if any. List "
    "symbol meanings only when this image defines them. Set legible=false when any symbol, index, "
    "exponent or fraction is uncertain or cut by the crop edge. Return an empty list when the block "
    "has no mathematics.")
SCHEMA = {"type": "json_schema", "json_schema": {"name": "equations", "strict": True, "schema": {
    "type": "object", "additionalProperties": False, "required": ["equations"],
    "properties": {"equations": {"type": "array", "items": {
        "type": "object", "additionalProperties": False,
        "required": ["kind", "number", "latex", "symbols", "legible"],
        "properties": {
            "kind": {"type": "string", "enum": list(KINDS)},
            "number": {"type": ["string", "null"]},
            "latex": {"type": "string"},
            "legible": {"type": "boolean"},
            "symbols": {"type": "array", "items": {
                "type": "object", "additionalProperties": False, "required": ["symbol", "meaning"],
                "properties": {"symbol": {"type": "string"}, "meaning": {"type": "string"}}}},
        }}}}}}}
DOCUMENT_KEYS = ("document_id", "title", "experiment", "psi_id", "study_group", "source", "source_type",
                 "url", "local_file", "content_sha256")


def corpus_pdfs(corpus):
    """One entry per PDF revision of the active corpus, bibliographies excluded."""
    documents = {}
    for unit in corpus:
        meta = unit["metadata"]
        if (str(meta.get("local_file", "")).lower().endswith(".pdf") and meta.get("content_sha256")
                and meta.get("document_role") != "bibliography"
                and not any(t in str(meta.get("source_type", "")) for t in ("Search snapshot", "Challenge"))):
            documents.setdefault(meta["content_sha256"], {key: meta.get(key) for key in DOCUMENT_KEYS})
    return documents


def dataset_candidates(db, documents):
    query = ("select e.rowid, a.sha256, e.page, e.details_json from equations e join assets a "
             "on a.asset_id = e.asset_id where e.kind = 'pdf_block_candidate'")
    for rowid, sha256, page, details in db.execute(query):
        if sha256 not in documents:
            continue
        details = json.loads(details)
        bbox = json.loads(details["bbox"]) if isinstance(details["bbox"], str) else details["bbox"]
        yield {"candidate_id": f"{sha256[:16]}:p{int(page)}:r{rowid}", "page": int(page), "bbox": bbox,
               "text_layer": details.get("extracted_text") or "", **documents[sha256]}


def block_candidates(path, document):
    import pymupdf
    with pymupdf.open(path) as pdf:
        for number, page in enumerate(pdf, 1):
            for index, block in enumerate(page.get_text("dict", flags=3)["blocks"]):
                content = "\n".join("".join(span.get("text", "") for span in line["spans"])
                                    for line in block.get("lines", []))
                # Same rule as the PSI dataset extractor.
                if not MATH.search(content) or all("http" in row or not row.strip() for row in content.splitlines()):
                    continue
                yield {"candidate_id": f"{document['content_sha256'][:16]}:p{number}:b{index}", "page": number,
                       "bbox": list(block["bbox"]), "text_layer": content, **document}


def gather(corpus_dir, dataset):
    corpus_dir = Path(corpus_dir)
    documents = corpus_pdfs(json.loads((corpus_dir / "corpus.json").read_text(encoding="utf-8")))
    db = sqlite3.connect(f"file:{Path(dataset).resolve()}?mode=ro", uri=True)
    try:
        covered = {row[0] for row in db.execute("select sha256 from assets where lower(filename) like '%.pdf'")}
        items = list(dataset_candidates(db, documents))
    finally:
        db.close()
    for sha256, document in sorted(documents.items()):
        if sha256 not in covered:
            items.extend(block_candidates(original(corpus_dir, document), document))
    return items


VERIFIED = {}  # path -> SHA256 already checked in this process


def original(corpus_dir, item):
    path = (Path(corpus_dir) / item["local_file"]).resolve()
    if not path.is_relative_to(Path(corpus_dir).resolve()) or not path.is_file():
        raise ValueError(f"Original PDF missing for {item['title']}")
    if VERIFIED.get(path) != item["content_sha256"]:
        if hashlib.sha256(path.read_bytes()).hexdigest() != item["content_sha256"]:
            raise ValueError(f"Original PDF hash mismatch for {item['title']}")
        VERIFIED[path] = item["content_sha256"]
    return path


def sample(items, size, seed=20261114):
    # One candidate per document first, then a second round, so few long reports cannot dominate.
    by_document = defaultdict(list)
    for item in items:
        by_document[item["content_sha256"]].append(item)
    rng = random.Random(seed)
    groups = [rng.sample(group, len(group)) for _, group in sorted(by_document.items())]
    rng.shuffle(groups)
    chosen, depth = [], 0
    while len(chosen) < size and any(len(g) > depth for g in groups):
        chosen += [g[depth] for g in groups if len(g) > depth][:size - len(chosen)]
        depth += 1
    return chosen


def render(item, corpus_dir, out):
    import pymupdf
    crop = out / "crops" / (item["candidate_id"].replace(":", "_") + ".png")
    crop.parent.mkdir(parents=True, exist_ok=True)
    with pymupdf.open(original(corpus_dir, item)) as pdf:
        page = pdf[item["page"] - 1]
        x0, y0, x1, y1 = item["bbox"]
        clip = pymupdf.Rect(x0 - PADDING_X, y0 - PADDING_Y, x1 + PADDING_X, y1 + PADDING_Y) & page.rect
        page.get_pixmap(matrix=pymupdf.Matrix(ZOOM, ZOOM), clip=clip).save(crop)
    return crop


def transcribe(llm, model, crop, text_layer):
    image = base64.b64encode(crop.read_bytes()).decode()
    started = time.monotonic()
    result = llm.chat.completions.create(
        model=model, response_format=SCHEMA, max_completion_tokens=4000,
        messages=[{"role": "user", "content": [
            {"type": "text", "text": PROMPT + "\n\nPDF text layer:\n" + text_layer[:3000]},
            {"type": "image_url", "image_url": {"url": "data:image/png;base64," + image, "detail": "high"}}]}])
    choice = result.choices[0]
    if choice.finish_reason != "stop" or not choice.message.content:
        raise ValueError(f"Incomplete transcription ({choice.finish_reason})")
    usage = result.usage
    return json.loads(choice.message.content)["equations"], {
        "input_tokens": usage.prompt_tokens, "output_tokens": usage.completion_tokens,
        "seconds": round(time.monotonic() - started, 2)}


# Sensor calibration polynomials, signal scaling, unit conversions and control-software
# conditions (matched on canonical LaTeX).
INSTRUMENT = re.compile(r"voltage|setpoint|slpm|sccm|full.?scale|output|counts|gain|signal|adc|irradiance|"
                        r"calibrat|[0-9a-z]mv\b|^mv|totalmv|btu|fov|control|ramp|cycles|cjtemp|tctemp", re.I)


def canonical(latex):
    """Grouping key: drops layout-only LaTeX, keeps symbols, case, signs and digits."""
    text = re.sub(r"\\(?:left|right|displaystyle|,|;|!|:| |quad|qquad)|\\(?:mathrm|mathit|text|textrm|operatorname)\b", "", latex)
    text = re.sub(r"\\tag\{[^}]*\}", "", text)
    # Same multiplication or fraction, different typing.
    text = re.sub(r"\\(?:ast|times|cdot)(?![A-Za-z])", "*", text)
    text = re.sub(r"\\[dt]frac(?![A-Za-z])", r"\\frac", text)
    text = re.sub(r"[\s{}~]", "", text)
    return text.rstrip(".,;:")


def category(latex):
    """Signal/unit conversions of an instrument are kept apart from physical laws and models."""
    return "instrument_conversion" if INSTRUMENT.search(canonical(latex)) else "physical_or_model"


def catalog_items(records):
    items = []
    for record in records:
        if record.get("status") != "transcribed":
            continue
        for position, equation in enumerate(record["equations"]):
            key = canonical(equation["latex"])
            if not key:
                continue
            items.append({"item_id": f"{record['candidate_id']}:{position}",
                          "group_id": hashlib.sha256((equation["kind"] + "|" + key).encode()).hexdigest()[:12],
                          **{k: record.get(k) for k in DOCUMENT_KEYS}, "page": record["page"],
                          "crop": record["crop"], **equation, "validation_status": "machine_transcription_unreviewed"})
    return items


def group_items(items):
    """Identical transcriptions (same kind and canonical LaTeX) across pages and documents."""
    groups = {}
    for item in items:
        entry = groups.setdefault(item["group_id"], {
            "group_id": item["group_id"], "kind": item["kind"], "latex": item["latex"],
            "canonical": canonical(item["latex"]), "category": category(item["latex"]), "occurrences": 0, "documents": set(),
            "investigations": set(), "psi_ids": set(), "examples": []})
        entry["occurrences"] += 1
        entry["documents"].add(item["document_id"])
        entry["investigations"].add(item.get("experiment") or "?")
        psi = item.get("psi_id")
        entry["psi_ids"].update(psi if isinstance(psi, list) else [psi] if psi else [])
        if len(entry["examples"]) < 8 and (item["document_id"], item["page"]) not in {
                (e["document_id"], e["page"]) for e in entry["examples"]}:
            entry["examples"].append({key: item.get(key) for key in (*DOCUMENT_KEYS, "page", "item_id", "number", "legible", "symbols")})
    ordered = sorted(groups.values(), key=lambda g: (-len(g["investigations"]), -len(g["documents"]), -g["occurrences"], g["group_id"]))
    return [{**g, "documents": len(g["documents"]), "investigations": sorted(g["investigations"]),
             "psi_ids": sorted(g["psi_ids"])} for g in ordered]


def read_records(path):
    """Results so far; a line cut by an interrupted write is ignored and its candidate retried."""
    if not path.is_file():
        return []
    records = []
    for line in path.read_text(encoding="utf-8").splitlines():
        try:
            records.append(json.loads(line))
        except json.JSONDecodeError:
            continue
    return records


def run(corpus_dir, dataset, out, size=None, model=None, workers=6):
    corpus_dir, out = Path(corpus_dir).resolve(), Path(out).resolve()
    config = settings()
    model = model or config["chat_model"]
    items = gather(corpus_dir, dataset)
    chosen = sample(items, size) if size else items
    out.mkdir(parents=True, exist_ok=True)
    results_path = out / "transcriptions.jsonl"
    done = {r["candidate_id"] for r in read_records(results_path) if r["status"] == "transcribed"}
    pending = [item for item in chosen if item["candidate_id"] not in done]
    llm = openai_client(config)
    llm.max_retries = 5
    lock, progress = threading.Lock(), [0]

    def work(item):
        record = {key: item.get(key) for key in ("candidate_id", "page", "bbox", "text_layer", *DOCUMENT_KEYS)}
        try:
            crop = render(item, corpus_dir, out)
            equations, usage = transcribe(llm, model, crop, item["text_layer"])
            record.update(status="transcribed", crop=crop.relative_to(out).as_posix(), equations=equations,
                          usage=usage, model=model, validation_status="machine_transcription_unreviewed")
        except Exception as error:  # Recorded per candidate; a rerun retries failures.
            record.update(status="failed", reason=f"{type(error).__name__}: {error}")
        return record

    try:
        with results_path.open("a", encoding="utf-8") as stream, ThreadPoolExecutor(workers) as pool:
            for future in as_completed([pool.submit(work, item) for item in pending]):
                record = future.result()
                with lock:
                    stream.write(json.dumps(record, ensure_ascii=False) + "\n")
                    stream.flush()
                    progress[0] += 1
                    if progress[0] % 100 == 0 or progress[0] == len(pending):
                        print(f"{progress[0]}/{len(pending)}", flush=True)
    finally:
        llm.close()
    latest = {}
    for record in read_records(results_path):
        if record["status"] == "transcribed" or record["candidate_id"] not in latest:
            latest[record["candidate_id"]] = record
    records = list(latest.values())
    transcribed = [r for r in records if r["status"] == "transcribed"]
    catalog = catalog_items(records)
    groups = group_items(catalog)
    with (out / "catalog.jsonl").open("w", encoding="utf-8") as stream:
        stream.writelines(json.dumps(item, ensure_ascii=False) + "\n" for item in catalog)
    with (out / "groups.jsonl").open("w", encoding="utf-8") as stream:
        stream.writelines(json.dumps(entry, ensure_ascii=False) + "\n" for entry in groups)
    kinds = defaultdict(int)
    for item in catalog:
        kinds[item["kind"]] += 1
    report = {"generated_at": datetime.now(timezone.utc).isoformat(), "model": model,
              "candidates_total": len(items), "candidates_run": len(records), "transcribed": len(transcribed),
              "failed": len(records) - len(transcribed), "items": len(catalog), "items_by_kind": dict(kinds),
              "groups": len(groups), "formula_groups": sum(1 for g in groups if g["kind"] == "formula"),
              "documents": len({r["document_id"] for r in transcribed}),
              "self_reported_illegible": sum(1 for item in catalog if not item["legible"]),
              "input_tokens": sum(r["usage"]["input_tokens"] for r in transcribed),
              "output_tokens": sum(r["usage"]["output_tokens"] for r in transcribed),
              "padding_points": [PADDING_X, PADDING_Y], "zoom": ZOOM, "sample_seed": 20261114 if size else None,
              "validated": False}
    write_json(out / "report.json", report)
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--corpus-dir", type=Path, default=settings()["corpus"].parent)
    parser.add_argument("--dataset", type=Path, default=ROOT / "data/psi_dataset_v1/dataset.sqlite")
    parser.add_argument("--out", type=Path, default=ROOT / "data/formulas_v2/full")
    parser.add_argument("--sample", type=int, help="Stratified pilot size; omit to transcribe every candidate")
    parser.add_argument("--workers", type=int, default=6)
    parser.add_argument("--model")
    args = parser.parse_args()
    if args.sample is not None and not 1 <= args.sample <= 500:
        parser.error("Use a pilot sample between 1 and 500")
    if not 1 <= args.workers <= 12:
        parser.error("Use 1-12 workers")
    print(json.dumps(run(args.corpus_dir, args.dataset, args.out, args.sample, args.model, args.workers)))


if __name__ == "__main__":
    main()
