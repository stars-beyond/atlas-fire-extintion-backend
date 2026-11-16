"""Discover and collect official NASA combustion documents; no LLM required.

Run from repository root: python -m backend.flame.pipeline --help
"""
import argparse
import csv
import hashlib
import json
import re
import time
import unicodedata
from io import BytesIO
from pypdf import PdfReader
from pypdf.errors import PdfReadError
from datetime import datetime, timezone
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode, urljoin, urlparse
from urllib.request import Request, urlopen

HERE = Path(__file__).resolve().parent
DEFAULT_OUT = HERE.parents[1] / "data" / "flame_2026"
API = "https://ntrs.nasa.gov/api/citations/"


def normalize(text):
    # Keep digits, decimal separators, signs, units, chemical symbols and negation.
    text = unicodedata.normalize("NFC", text).replace("\x00", "")
    return re.sub(r"[ \t]+", " ", re.sub(r"\n{3,}", "\n\n", text)).strip()


def fetch(url, limit=35_000_000):
    if urlparse(url).scheme != "https" or urlparse(url).hostname != "ntrs.nasa.gov":
        raise ValueError("Only HTTPS NTRS URLs may be collected by this adapter")
    for attempt in range(3):
        try:
            with urlopen(Request(url, headers={"User-Agent": "CosmoEX-Flame/2026 research collector"}), timeout=35) as response:
                if urlparse(response.url).hostname != "ntrs.nasa.gov":
                    raise ValueError("Unexpected redirect outside NTRS")
                body = response.read(limit + 1)
                if len(body) > limit:
                    raise ValueError("Download exceeds size limit")
                return body
        except (HTTPError, URLError, TimeoutError) as error:
            if isinstance(error, HTTPError) and error.code not in (429, 500, 502, 503, 504):
                raise
            if attempt == 2:
                raise
            time.sleep(0.5 * (attempt + 1))


def write_json(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    temp = path.with_suffix(path.suffix + ".tmp")
    temp.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    temp.replace(path)


def topical(record):
    text = (record.get("title", "") + " " + record.get("abstract", "")).lower()
    context = re.search(r"microgravity|reduced.gravity|low.gravity|spacecraft|space station", text)
    subject = re.search(r"combust|flame|flammab|fire safety|smoke|ignition|extinction", text)
    return bool(context and subject)


def metadata(record, seed):
    publications = record.get("publications") or []
    date = next((p["publicationDate"] for p in publications if p.get("publicationDate")), None)
    return {
        "document_id": seed["id"], "title": record["title"],
        "url": seed["url"], "source": "NASA NTRS", "source_type": record.get("stiTypeDetails", record.get("stiType")),
        "authors": [a.get("meta", {}).get("author", {}).get("name", "") for a in record.get("authorAffiliations", [])],
        "publication_date": date, "doi": record.get("doi") or next((p.get("doi") for p in publications if p.get("doi")), None),
        "experiment": seed.get("experiment", "unclassified"), "topic": "microgravity_combustion",
        "rights": record.get("copyright", {}).get("determinationType", "unspecified"),
        "retrieved_at": datetime.now(timezone.utc).isoformat(),
    }


def extract(record, meta, out, use_pdf=False):
    warnings = []
    for item in record.get("downloads", []):
        if item.get("draft"):
            continue
        links = item.get("links", {})
        variants = [("pdf", links.get("pdf"))] if use_pdf else []
        variants.append(("text", links.get("fulltext")))
        for kind, link in variants:
            if not link:
                continue
            url = urljoin(API, link)
            try:
                raw = fetch(url)
                sha = hashlib.sha256(raw).hexdigest()
                if kind == "pdf":
                    pdf = PdfReader(BytesIO(raw))
                    pages = [(i + 1, normalize(page.extract_text() or "")) for i, page in enumerate(pdf.pages)]
                else:
                    text = raw.decode("utf-8-sig", errors="replace")
                    if re.search(r"<(?:!doctype|html|body)\b", text[:500], re.I):
                        raise ValueError("HTML error page returned instead of full text")
                    pages = [(None, normalize(text))]
                if sum(len(text) for _, text in pages) < 200:
                    raise ValueError("No usable text (scan or empty document)")
                # Keep original downloads as reproducible local cache, excluded from ZIP.
                cache = out / "raw" / (meta["document_id"] + (".pdf" if kind == "pdf" else ".txt"))
                cache.parent.mkdir(parents=True, exist_ok=True)
                cache.write_bytes(raw)
                documents = [{"content": text, "metadata": {**meta, "page": page, "evidence_level": "full_text", "download_url": url, "content_sha256": sha, "extraction_method": kind}} for page, text in pages if len(text) >= 30]
                return documents, warnings
            except (HTTPError, URLError, TimeoutError, ValueError, RuntimeError, ImportError, PdfReadError) as error:
                warnings.append(f"{kind}: {type(error).__name__}: {error}")
    abstract = normalize(record.get("abstract", ""))
    if len(abstract) >= 80:
        return [{"content": abstract, "metadata": {**meta, "page": None, "evidence_level": "abstract_only", "extraction_method": "ntrs_abstract"}}], warnings
    return [], warnings + ["No usable full text or abstract"]


def discover(args):
    queries = args.query or json.loads((HERE / "profile.json").read_text())["queries"]
    records, errors = {}, []
    for query in queries:
        for page in range(args.pages):
            url = API + "search?" + urlencode({"q": query, "page.size": args.per_page, "page.from": page * args.per_page})
            try:
                response = json.loads(fetch(url))
                hits = response.get("results", [])
                for record in hits:
                    if not topical(record):
                        continue
                    key = str(record["id"])
                    entry = records.setdefault(key, {"id": "ntrs-" + key, "record_id": key, "provider": "ntrs", "title": record["title"], "url": "https://ntrs.nasa.gov/citations/" + key, "approved": False, "matched_queries": [], "status": "candidate_requires_review"})
                    if query not in entry["matched_queries"]:
                        entry["matched_queries"].append(query)
                if len(hits) < args.per_page:
                    break
            except (HTTPError, URLError, TimeoutError, ValueError) as error:
                errors.append({"query": query, "page": page, "error": str(error)})
                break
    write_json(args.out / "candidates.json", list(records.values()))
    write_json(args.out / "discovery_report.json", {"queries": queries, "candidates": len(records), "errors": errors, "exhaustive": False, "max_pages_per_query": args.pages})
    print(f"{len(records)} candidates; {len(errors)} discovery errors. Review before adding approved=true to the manifest.")
    return 1 if errors else 0


def collect(args):
    seeds = json.loads(args.manifest.read_text(encoding="utf-8"))
    if len({s["id"] for s in seeds}) != len(seeds):
        raise ValueError("Duplicate source identifiers in manifest")
    corpus, catalog = [], []
    failures = 0
    for seed in seeds:
        entry = dict(seed)
        if not seed.get("approved") or seed.get("provider") != "ntrs":
            entry.setdefault("status", "not_approved")
            catalog.append(entry)
            continue
        try:
            record_id = seed["record_id"]
            if not re.fullmatch(r"\d+", record_id):
                raise ValueError("Invalid NTRS identifier")
            record = json.loads(fetch(API + record_id))
            if str(record.get("id")) != record_id or record.get("distribution") != "PUBLIC":
                raise ValueError("Source is not the expected public record")
            if not topical(record):
                raise ValueError("Source does not pass combustion/space relevance filter")
            write_json(args.out / "records" / (record_id + ".json"), record)
            meta = metadata(record, seed)
            docs, warnings = extract(record, meta, args.out, args.pdf)
            corpus.extend(docs)
            entry.update(meta)
            entry.update({"status": docs[0]["metadata"]["evidence_level"] if docs else "no_text", "text_units": len(docs), "characters": sum(len(d["content"]) for d in docs), "warnings": warnings})
            failures += not bool(docs)
        except (HTTPError, URLError, TimeoutError, ValueError, KeyError) as error:
            failures += 1
            entry.update({"status": "failed", "error": f"{type(error).__name__}: {error}"})
        catalog.append(entry)
        print(seed["id"], entry["status"], flush=True)
    write_json(args.out / "corpus.json", corpus)
    write_json(args.out / "catalog.json", catalog)
    fields = ["id", "title", "experiment", "source_type", "publication_date", "doi", "url", "status", "text_units", "characters"]
    with (args.out / "catalog.csv").open("w", encoding="utf-8-sig", newline="") as file:
        writer = csv.DictWriter(file, fieldnames=fields, extrasaction="ignore", lineterminator="\n")
        writer.writeheader()
        writer.writerows(catalog)
    report = {"sources": len(catalog), "full_text": sum(c.get("status") == "full_text" for c in catalog), "abstract_only": sum(c.get("status") == "abstract_only" for c in catalog), "text_units": len(corpus), "failures": failures, "indexed_in_qdrant": False}
    write_json(args.out / "collection_report.json", report)
    print(json.dumps(report))
    return 1 if failures else 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    discovery = sub.add_parser("discover")
    discovery.add_argument("--query", action="append")
    discovery.add_argument("--pages", type=int, default=2)
    discovery.add_argument("--per-page", type=int, default=25)
    collection = sub.add_parser("collect")
    collection.add_argument("--manifest", type=Path, default=HERE / "sources.json")
    collection.add_argument("--pdf", action="store_true", help="Extract PDF pages; fall back to NTRS text or abstract")
    for child in [discovery, collection]:
        child.add_argument("--out", type=Path, default=DEFAULT_OUT)
    args = parser.parse_args()
    if args.command == "discover" and not (1 <= args.pages <= 20 and 1 <= args.per_page <= 100):
        parser.error("Use 1-20 pages and 1-100 records per page")
    args.out.mkdir(parents=True, exist_ok=True)
    return discover(args) if args.command == "discover" else collect(args)


if __name__ == "__main__":
    raise SystemExit(main())
