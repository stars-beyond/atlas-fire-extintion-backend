"""Experimental conditions table: values with units and their exact source.

Two deterministic extractors, no LLM:
- experimental tables: a column whose label names a quantity and unit (e.g. 'pinit (atm)',
  'XO2', 'Air Flow (cm/s)') gives one row per test;
- text (report pages, records, NTRS papers): value+unit mentions with the sentence around them.
Rows are candidates for reading, not validated measurements. The role is a keyword hint
and normalization is a plain unit conversion; the literal value and context are kept.
"""
import argparse
import csv
import hashlib
import json
import re
import sqlite3
from collections import Counter, defaultdict
from pathlib import Path

from .config import ROOT, settings
from .pipeline import write_json

QUANTITIES = {
    "gravity": ("Nível de gravidade", "Gravity level", "g"),
    "heat_flux": ("Fluxo de calor", "Heat flux", "kW/m²"),
    "oxygen": ("Concentração de oxigênio", "Oxygen concentration", "% vol"),
    "pressure": ("Pressão", "Pressure", "kPa"),
    "flow_velocity": ("Velocidade do escoamento", "Flow velocity", "cm/s"),
    "spread_rate": ("Taxa de propagação da chama", "Flame spread rate", "cm/s"),
    "temperature": ("Temperatura", "Temperature", "K"),
}
FACTORS = {
    "pressure": {"atm": 101.325, "kpa": 1.0, "mpa": 1000.0, "pa": 0.001, "psia": 6.894757, "psi": 6.894757,
                 "torr": 0.1333224, "mmhg": 0.1333224, "bar": 100.0, "mbar": 0.1},
    "flow_velocity": {"cm/s": 1.0, "mm/s": 0.1, "m/s": 100.0},
    "spread_rate": {"cm/s": 1.0, "mm/s": 0.1, "m/s": 100.0},
    "heat_flux": {"kw/m2": 1.0, "w/cm2": 10.0, "w/m2": 0.001},
    "gravity": {"g": 1.0, "mg": 1e-3, "µg": 1e-6, "ug": 1e-6, "m/s2": 1 / 9.80665},
    "oxygen": {"%": 1.0, "fraction": 100.0},
}
EXCLUDED_ROLES = {"bibliography", "data_card"}
EXCLUDED_TYPES = ("Bibliographic references", "Search snapshot", "Challenge")

NUM = r"(\d{1,3}(?:,\d{3})+(?!\d)|\d+(?:\.\d+)?|\.\d+)"
# A number right after a minus sign is an exponent or a negative: '10−4 m/s2' is not 4 m/s2.
RANGE = rf"(?<![\w.,/^\-−–]){NUM}(?:\s*(?:-|–|—|to|and)\s*{NUM})?"
SPEED = r"(cm|mm|m)\s*(?:/\s*s(?:ec)?\b|\s+s\s*[-−]\s*1\b)"
TEXT_PATTERNS = [
    ("pressure", re.compile(RANGE + r"\s*(atm|kPa|MPa|psia|psi|torr|Torr|mmHg|mbar|bar)\b")),
    # W/m2-K (transfer coefficient) and W/cm2 K4 (Stefan-Boltzmann) are not heat fluxes.
    ("heat_flux", re.compile(RANGE + r"\s*(kW|W)\s*/\s*(cm|m)\s*(?:2|²|\^2)(?!\s*[-·⋅]?\s*K)")),
    ("velocity", re.compile(RANGE + r"\s*" + SPEED)),
    ("oxygen", re.compile(RANGE + r"\s*(?:%|percent)\s*(?:by volume\s*|vol\.?\s*)?(?:O2|O₂|oxygen)\b", re.I)),
    # Same line only: in flattened tables a later percentage belongs to another species.
    ("oxygen", re.compile(r"(?:O2|O₂|oxygen)(?:[ \t]+(?:concentrations?|mole fractions?|levels?|contents?))?"
                          r"(?:[ \t]+(?:of|at|was|were|is|between|from))?[ \t]*[=:~]?[ \t]*" + RANGE + r"[ \t]*(?:%|percent)", re.I)),
    # Mole fractions such as 'YO2 = 0.209' in test summaries.
    ("oxygen_fraction", re.compile(r"\b[XY]\s?_?\s?O2\s*[=:]\s*" + RANGE)),
    # Kelvin needs a space or a combustion-scale value: '54K' is usually an identifier.
    ("temperature", re.compile(RANGE + r"\s*(°\s*C|°\s*F|deg(?:rees)?\s*C|K)(?![A-Za-z])")),
    # 'g-jitter' after a section number such as '5.5' is a heading, not a gravity level.
    ("gravity", re.compile(RANGE + r"\s*[-‐]?\s*(µg|ug|mg|g0|g|m\s*/\s*s\s*(?:2|²))(?![A-Za-z/\-])")),
]
EXPONENT_G = re.compile(r"10\s*[-−–]\s*(\d)\s*(g|m\s*/\s*s\s*(?:2|²))(?![A-Za-z/])")
MECHANICAL = re.compile(r"stress|strength|tensile|yield|modulus|load|MSu|burst|proof", re.I)
GRAVITY_CONTEXT = re.compile(r"gravit|g-level|g level|accelerat|microgravit|lunar|moon|martian|mars|"
                             r"centrifug|drop tower|parabolic|jitter|reduced.g|low.g|partial.g|normal.g|earth", re.I)
SPREAD_CONTEXT = re.compile(r"spread|propagat|regression|burning rate|flame speed", re.I)
FLOW_CONTEXT = re.compile(r"flow|veloc|air ?speed|forced|ventilat|blow|opposed|concurrent|wind|stream|fan", re.I)
ROLES = (("requirement/planned", re.compile(r"\b(shall|requir\w*|planned|proposed|will be|target\w*|goal|design\w*|specif\w*)\b", re.I)),
         ("simulation/model", re.compile(r"\b(simulat\w*|model\w*|predict\w*|numerical\w*|computed|calculat\w*|CFD|FDS)\b")),
         ("test/measurement", re.compile(r"\b(measur\w*|observ\w*|tested|tests?|experiment\w*|flight|runs?|burn\w*|ignit\w*|recorded|conducted)\b", re.I)))


def number(text):
    return float(text.replace(",", ""))


def unit_key(unit):
    unit = re.sub(r"\s+|deg(?:rees)?", "", unit.lower()).replace("°", "").replace("²", "2").replace("^", "")
    return {"sec": "s"}.get(unit, unit.replace("/sec", "/s").replace("s-1", "/s").replace("s−1", "/s"))


def normalize(quantity, low, high, unit):
    key = unit_key(unit)
    if quantity == "temperature":
        convert = {"c": lambda v: v + 273.15, "f": lambda v: (v - 32) * 5 / 9 + 273.15, "k": lambda v: v}.get(key)
    else:
        factor = FACTORS[quantity].get(key)
        convert = (lambda v: v * factor) if factor else None
    if not convert:
        return None, None
    # Significant figures, not decimal places: microgravity levels are 1e-6 g.
    return float(f"{convert(low):.6g}"), float(f"{convert(high):.6g}")


def context(text, start, end, width=160):
    """The sentence around a value, at most `width` characters on each side."""
    boundary = text.rfind(". ", max(0, start - width), start)
    left = boundary + 2 if boundary != -1 else max(0, start - width)
    boundary = text.find(". ", end, end + width)
    right = boundary + 1 if boundary != -1 else min(len(text), end + width)
    return " ".join(text[left:right].split())


def role(window):
    for label, pattern in ROLES:
        if pattern.search(window):
            return label
    return "mention"


def provenance(meta):
    return {key: meta.get(key) for key in ("document_id", "title", "experiment", "psi_id", "study_group",
                                           "source_type", "page", "row", "url", "local_file", "content_sha256")}


def record(quantity, literal, low, high, unit, meta, ctx, row_role, column=None, flags=()):
    norm_low, norm_high = normalize(quantity, low, high, unit)
    flags = list(flags)
    if norm_low is None:
        flags.append("unit not converted")
    base = provenance(meta)
    identity = json.dumps([quantity, literal, column, ctx, base["document_id"], base["page"], base["row"]], ensure_ascii=False)
    return {"condition_id": hashlib.sha256(identity.encode()).hexdigest()[:16], "quantity": quantity,
            "value_text": literal, "value_min": low, "value_max": high, "unit": unit,
            "normalized_min": norm_low, "normalized_max": norm_high, "normalized_unit": QUANTITIES[quantity][2],
            "role": row_role, "column": column, "context": ctx, "flags": flags, **base}


def text_conditions(text, meta):
    rows, seen = [], set()
    for kind, pattern in TEXT_PATTERNS:
        for match in pattern.finditer(text):
            low = number(match.group(1))
            high = number(match.group(2)) if match.group(2) else low
            unit = match.group(match.lastindex) if not kind.startswith("oxygen") else "%"
            if kind == "oxygen_fraction":
                kind, unit = "oxygen", "fraction" if high <= 1 else "%"
            if kind == "heat_flux":
                unit = f"{match.group(3)}/{match.group(4)}2"
            window = text[max(0, match.start() - 150):match.end() + 150]
            near = text[max(0, match.start() - 70):match.start()]
            if kind == "velocity":
                quantity = "spread_rate" if SPREAD_CONTEXT.search(near) and not FLOW_CONTEXT.search(near[-30:]) else "flow_velocity"
                unit = f"{match.group(3)}/s"
            else:
                quantity = kind
            if quantity == "gravity" and not GRAVITY_CONTEXT.search(window):
                continue
            if quantity == "pressure" and MECHANICAL.search(near[-40:]):
                continue
            if quantity == "temperature" and unit == "K" and not re.search(r"\sK$", match.group(0)) and high < 150:
                continue
            flags = []
            if quantity == "gravity" and EXPONENT_G.fullmatch(match.group(0).strip()):
                continue
            if " and " in match.group(0):
                flags.append("values cited together; not necessarily a range")
            elif low > high:
                flags.append("range reversed in the text")
            low, high = min(low, high), max(low, high)
            key = (quantity, match.start())
            if key in seen:
                continue
            seen.add(key)
            # The role hint reads only the value's own sentence.
            sentence = context(text, match.start(), match.end())
            rows.append(record(quantity, match.group(0).strip(), low, high, unit, meta,
                               sentence, role(sentence), flags=flags))
    for match in EXPONENT_G.finditer(text):
        window = text[max(0, match.start() - 150):match.end() + 150]
        if GRAVITY_CONTEXT.search(window):
            value = 10.0 ** -int(match.group(1))
            unit = "g" if match.group(2) == "g" else "m/s2"
            sentence = context(text, match.start(), match.end())
            rows.append(record("gravity", match.group(0), value, value, unit, meta, sentence, role(sentence),
                               flags=[f"exponent probably lost in extraction (10^-n {unit}); check the original"]))
    return rows


LABELS = [
    ("oxygen", re.compile(r"(?:^|[^a-z])(?:x\s*o2|o2|o₂|oxygen)(?![a-z])", re.I)),
    ("pressure", re.compile(r"pressure|\b(?:atm|kpa|psia|psi|torr|mbar|bar)\b|^p\s*\w*\s*\(", re.I)),
    ("heat_flux", re.compile(r"heat flux|w/cm|kw/m", re.I)),
    ("gravity", re.compile(r"g-level|gravity|\bg level\b", re.I)),
    ("velocity", re.compile(r"cm/s|mm/s|\bm/s\b|veloc|air ?flow|flow speed", re.I)),
    ("temperature", re.compile(r"temp|\(k\)|°c|deg ?c|\(c\)", re.I)),
]
MASS_FLOW = re.compile(r"mg/s|g/s|slpm|sccm|ppm|lpm|mass|\bm[A-Z]?o2\b|^mo2", re.I)
UNIT_IN_LABEL = re.compile(r"\(([^)]*)\)|\b(atm|kPa|psia|psi|torr|mbar|bar|cm/s|mm/s|m/s|W/cm2|kW/m2|K)\b", re.I)


def column_quantity(label):
    for kind, pattern in LABELS:
        if pattern.search(label):
            if kind == "oxygen" and MASS_FLOW.search(label):
                return None
            if kind == "velocity":
                return "spread_rate" if re.search(r"spread", label, re.I) else "flow_velocity"
            return kind
    return None


def label_unit(quantity, label):
    for match in UNIT_IN_LABEL.finditer(label):
        unit = (match.group(1) or match.group(2) or "").strip()
        if quantity == "temperature" and unit_key(unit) in {"k", "c", "f"}:
            return unit
        if quantity == "oxygen" and "%" in unit:
            return "%"
        if quantity in FACTORS and unit_key(unit) in FACTORS[quantity]:
            return unit
    if quantity == "oxygen":
        return "%" if re.search(r"%|percent", label, re.I) else ("fraction" if re.search(r"\bx\s*o2|fraction", label, re.I) else None)
    return None


def table_cells(content):
    header = re.search(r"candidate column labels, not inferred or validated\): (\[.*?\])\n", content)
    cells = re.search(r"Original cells \(column positions preserved\): (\[.*\])", content)
    if cells:
        try:
            values = json.loads(cells.group(1))
            labels = json.loads(header.group(1)) if header else None
        except json.JSONDecodeError:
            return []
        if not labels or labels == values:
            return []
        return [(str(label or ""), value) for label, value in zip(labels, values)]
    pairs = []
    for line in content.splitlines():
        label, separator, value = line.partition(": ")
        if separator and not line.startswith(("Note ", "Notas")):
            pairs.append((label, value))
    return pairs


def table_conditions(content, meta):
    rows = []
    for label, value in table_cells(content):
        quantity = column_quantity(label)
        if not quantity or value in (None, ""):
            continue
        match = re.search(NUM, str(value))
        if not match:
            continue
        unit = label_unit(quantity, label)
        if unit is None and quantity in ("flow_velocity", "spread_rate", "pressure"):
            inline = re.search(r"(cm/s|mm/s|m/s|atm|kPa|psia|psi)", str(value))
            unit = inline.group(1) if inline else None
        if unit is None:
            continue
        low = number(match.group(1))
        if quantity == "oxygen" and unit == "fraction" and low > 1:
            unit = "%"
        flags = ["approximate value in the table"] if "~" in str(value) else []
        rows.append(record(quantity, str(value).strip(), low, low, unit, meta,
                           f"{label}: {value}", "experimental table", column=label, flags=flags))
    return rows


def excluded(meta):
    return (meta.get("document_role") in EXCLUDED_ROLES
            or any(token in str(meta.get("source_type", "")) for token in EXCLUDED_TYPES))


def latex_text(latex):
    """Plain reading of a value transcription, e.g. 'P = 768.492\\,\\mathrm{mmHg}' -> 'P = 768.492 mmHg'."""
    text = re.sub(r"\\(?:mathrm|mathit|text|textrm|operatorname)\s*", "", latex)
    text = re.sub(r"\\[,;!:]|\\quad|~", " ", text)
    text = text.replace(r"\%", "%").replace(r"\circ", "°").replace(r"\mu", "µ").replace(r"\times", "×")
    text = re.sub(r"\^\{?2\}?", "2", text)
    return " ".join(re.sub(r"[{}_]|\\[A-Za-z]+", "", text).split())


def build_conditions(documents, transcribed=()):
    rows = []
    for document in documents:
        meta = document["metadata"]
        if excluded(meta):
            continue
        if "Experimental table" in str(meta.get("source_type", "")):
            rows.extend(table_conditions(document["content"], meta))
        else:
            rows.extend(text_conditions(document["content"], meta))
    # Values read from images (figures, slides, boxes) complement the text layer;
    # a value already found on the same page is not repeated.
    known = {(r["document_id"], r["page"], r["quantity"], r["normalized_min"], r["normalized_max"]) for r in rows}
    for item in transcribed:
        if item.get("kind") != "value":
            continue
        for row in text_conditions(latex_text(item["latex"]), {**item, "document_role": None}):
            key = (row["document_id"], row["page"], row["quantity"], row["normalized_min"], row["normalized_max"])
            if key in known:
                continue
            known.add(key)
            rows.append({**row, "role": "image transcription",
                         "flags": row["flags"] + ["value automatically transcribed from an image (not reviewed)"]})
    return list({row["condition_id"]: row for row in rows}.values())


# ---------------------------------------------------------------- chat route

QUANTITY_WORDS = [
    ("heat_flux", r"fluxos? de calor|frequ[eê]ncias? de calor|fluxo radiante|heat flux\w*|radiant flux"),
    ("spread_rate", r"(?:taxa|velocidade)s? de propaga[çc][ãa]o|spread rates?|flame spread"),
    ("flow_velocity", r"velocidades? (?:d[oae]s? )?(?:escoamento|fluxo|ar|vento)|fluxo de ar|escoamento|airflow|air flow|flow (?:velocit\w*|speeds?)|ventila\w*"),
    ("gravity", r"gravidade|n[ií]ve(?:l|is) de g\b|g-level\w*|gravity"),
    ("oxygen", r"oxig[eê]nio|\bo2\b|o₂|oxygen"),
    ("pressure", r"press(?:[ãa]o|[õo]es)|pressure\w*"),
    ("temperature", r"temperaturas?|temperatures?"),
]
VALUE_INTENT = re.compile(r"\b(valor(?:es)?|faixas?|n[ií]ve(?:l|is)|quant[oa]s?|condi[çc](?:[ãa]o|[õo]es)|"
                          r"range|ranges|levels?|values?|usad\w*|testad\w*|aplicad\w*|utilizad\w*|medid\w*|"
                          r"used|tested|applied|measured)\b", re.I)


def condition_request(query):
    quantities, rest = [], query
    for quantity, pattern in QUANTITY_WORDS:
        if re.search(pattern, rest, re.I):
            quantities.append(quantity)
            rest = re.sub(pattern, " ", rest, flags=re.I)
    return quantities if quantities and VALUE_INTENT.search(query) else []


def psi_list(value):
    return value if isinstance(value, list) else [value] if value else []


def in_scope(row, experiment_groups, psi_ids):
    if psi_ids and not set(psi_list(row.get("psi_id"))) & set(psi_ids):
        return False
    if experiment_groups:
        experiment = row.get("experiment") or ""
        return any(experiment in group or any(experiment.startswith(name + " ") for name in group if name in {"ACME", "SAME"})
                   for group in experiment_groups)
    return True


def fmt(value):
    return f"{value:.4g}"


def span(low, high, unit):
    return f"{fmt(low)} {unit}" if low == high else f"{fmt(low)}–{fmt(high)} {unit}"


def condition_sources(table, quantities, experiment_groups=(), psi_ids=(), limit=12):
    """Table summaries and text mentions as numbered evidence, plus whole-table coverage."""
    rows = [r for r in table if r["quantity"] in quantities and in_scope(r, experiment_groups, psi_ids)]
    coverage = {}
    for quantity in quantities:
        chosen = [r for r in rows if r["quantity"] == quantity]
        per = Counter(r.get("experiment") or "?" for r in chosen)
        coverage[QUANTITIES[quantity][1]] = {"rows": len(chosen), "experiments": dict(per.most_common())}
    groups = defaultdict(list)
    for row in rows:
        kind = "table" if row["role"] == "experimental table" else "text"
        groups[(row.get("experiment") or "?", kind, row["document_id"])].append(row)
    preference = {"test/measurement": 0, "mention": 1, "simulation/model": 2, "requirement/planned": 3}
    candidates = sorted(groups.items(), key=lambda item: (item[0][1] != "table", -len(item[1])))
    by_experiment = defaultdict(list)
    for key, members in candidates:
        by_experiment[key[0]].append((key, members))
    order, depth = [], 0
    while len(order) < limit and any(len(v) > depth for v in by_experiment.values()):
        for experiment in sorted(by_experiment, key=lambda e: -sum(len(m) for _, m in by_experiment[e])):
            if len(by_experiment[experiment]) > depth and len(order) < limit:
                order.append(by_experiment[experiment][depth])
        depth += 1
        if depth >= 2:
            break
    sources = []
    for (experiment, kind, _), members in order:
        first = members[0]
        lines = [f"Automatically extracted conditions (not validated) — document: {first['title']}; "
                 f"investigation: {experiment}."]
        if kind == "table":
            columns = defaultdict(list)
            for row in members:
                columns[(row["quantity"], row["column"], row["unit"])].append(row)
            for (quantity, column, unit), values in columns.items():
                originals = sorted({r["value_min"] for r in values})
                normalized = [r["normalized_min"] for r in values if r["normalized_min"] is not None]
                line = (f"Experimental table, column '{column}': {len(values)} tests; values {span(originals[0], originals[-1], unit)}")
                if normalized and unit_key(unit) != unit_key(QUANTITIES[quantity][2]):
                    line += f" (≈ {span(min(normalized), max(normalized), QUANTITIES[quantity][2])})"
                shown = ", ".join(fmt(v) for v in originals[:10])
                lines.append(line + f"; distinct values: {shown}{' …' if len(originals) > 10 else ''}.")
        else:
            # Generic '0g'/'1g' mentions say less than a tested level such as 0.16 g;
            # repeated values add nothing.
            generic = lambda r: r["quantity"] == "gravity" and {r["normalized_min"], r["normalized_max"]} <= {0.0, 1.0}
            ranked = sorted(members, key=lambda r: (generic(r), preference.get(r["role"], 9), r.get("page") or 0))
            members = list({(r["quantity"], r["normalized_min"], r["normalized_max"]): r for r in reversed(ranked)}.values())
            members = sorted(members, key=lambda r: (generic(r), preference.get(r["role"], 9), r.get("page") or 0))[:5]
            for row in members:
                where = f"p. {row['page']}" if row.get("page") else "excerpt"
                note = f" [{'; '.join(row['flags'])}]" if row["flags"] else ""
                lines.append(f"{where} ({row['role']}): «{row['context']}» → {row['value_text']}{note}")
        meta = {key: first.get(key) for key in ("document_id", "title", "experiment", "psi_id", "study_group",
                                                 "source_type", "url", "local_file", "content_sha256")}
        meta.update(page=first.get("page") if kind == "text" else None, retrieval_method="conditions_table",
                    evidence_level="full_text", topic="microgravity_combustion",
                    data_warnings=["Conditions table generated by automatic extraction: check the excerpt and the original before using the value."])
        sources.append({"page_content": "\n".join(lines), "metadata": meta})
    return sources, coverage


# ---------------------------------------------------------------- export

FIELDS = ["condition_id", "quantity", "value_text", "value_min", "value_max", "unit", "normalized_min",
          "normalized_max", "normalized_unit", "role", "column", "context", "flags", "document_id", "title",
          "experiment", "psi_id", "study_group", "source_type", "page", "row", "url", "local_file", "content_sha256"]


def export(corpus, out, formulas=None):
    from .formulas import load_machine_items
    out = Path(out)
    documents = json.loads(Path(corpus).read_text(encoding="utf-8"))
    transcribed = load_machine_items(formulas, documents)
    table = sorted(build_conditions(documents, transcribed),
                   key=lambda r: (r["quantity"], r["experiment"] or "", r["document_id"],
                                  r.get("page") or 0, r.get("row") or 0))
    out.mkdir(parents=True, exist_ok=True)
    with (out / "conditions.jsonl").open("w", encoding="utf-8") as stream:
        for row in table:
            stream.write(json.dumps(row, ensure_ascii=False) + "\n")
    flat = [{**row, "flags": "; ".join(row["flags"]), "psi_id": "; ".join(psi_list(row["psi_id"]))} for row in table]
    with (out / "conditions.csv").open("w", encoding="utf-8-sig", newline="") as stream:
        writer = csv.DictWriter(stream, fieldnames=FIELDS, lineterminator="\n")
        writer.writeheader()
        writer.writerows(flat)
    database = out / "conditions.sqlite"
    temporary = database.with_suffix(".tmp")
    temporary.unlink(missing_ok=True)
    from contextlib import closing
    with closing(sqlite3.connect(temporary)) as db, db:
        db.execute(f"create table conditions ({', '.join(FIELDS)})")
        db.executemany(f"insert into conditions values ({', '.join('?' for _ in FIELDS)})",
                       [[row[f] for f in FIELDS] for row in flat])
        db.execute("create index conditions_quantity on conditions(quantity, experiment)")
    temporary.replace(database)
    report = {"rows": len(table), "by_quantity": dict(Counter(r["quantity"] for r in table)),
              "by_role": dict(Counter(r["role"] for r in table)),
              "documents": len({r["document_id"] for r in table}),
              "experiments": len({r["experiment"] for r in table}), "corpus": str(corpus),
              "validated": False}
    write_json(out / "report.json", report)
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--corpus", type=Path, default=settings()["corpus"])
    parser.add_argument("--out", type=Path, default=ROOT / "data/conditions_v1")
    parser.add_argument("--formulas", type=Path, default=settings()["formulas"],
                        help="Automatic transcription catalog whose values complement the text")
    args = parser.parse_args()
    print(json.dumps(export(args.corpus, args.out, args.formulas), ensure_ascii=False))


if __name__ == "__main__":
    main()
