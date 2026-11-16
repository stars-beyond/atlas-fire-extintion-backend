#!/usr/bin/env python3
"""BASS-II (PSI-25) outcomes taken from appendix A of the NASA report.

The PSI experimental table does not say how each test ended. Report
NASA/TM-20210011385 does, in free text, in the last column of the test matrices.
This script cross-references the two sources and writes, for each test:

  - whether it ignited and how the flame ended (too little air, strong airflow, on its own, burned to the end...);
  - the fan setting at that moment and the estimated air speed in cm/s;
  - the report sentence and page, so anyone can check it;
  - the disagreements between the PSI table and the report (audit list).

No AI is used here: these are text rules, and hand corrections live in
`PSI-25.overrides.json`, each with its reason.

Usage (standard library only):
    python3 scripts/extract_outcomes.py                 # reads the seed and writes data/seed/outcomes/PSI-25.json
    python3 scripts/extract_outcomes.py --from-corpus   # rebuilds the appendix text from the RAG corpus
"""
import argparse
import csv
import io
import json
import math
import os
import re
import statistics
import sys
import zipfile
import xml.etree.ElementTree as ET

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SEED = os.path.join(ROOT, "data", "seed")
TABLE = os.path.join(SEED, "tables", "PSI-25_Experimental table_BASS-II.csv")
APPENDIX = os.path.join(SEED, "reports", "PSI-25_BASS-II_Summary_Report_appendix_A.txt")
CALIB = os.path.join(SEED, "reference", "PSI-25_Reference Data_Fan calibration data.xlsx")
OUT = os.path.join(SEED, "outcomes", "PSI-25.json")
OVERRIDES = os.path.join(SEED, "outcomes", "PSI-25.overrides.json")
CORPUS = os.path.join(ROOT, "rag", "data", "flame_v6", "corpus.json")
REPORT_ID = "ntrs-20210011385"
REPORT = {"title": "Burning and Suppression of Solids–II (BASS-II) Summary Report", "number": "NASA/TM-20210011385",
          "url": "https://ntrs.nasa.gov/citations/20210011385", "section": "Appendix A, Tables A.1 to A.6"}
PDF_PAGES = range(111, 122)  # appendix A in the PDF; the printed page is the PDF page minus 8

# ----------------------------------------------------------------- appendix text


def appendix_from_corpus(path):
    corpus = json.load(open(path, encoding="utf-8"))
    pages = {}
    for d in corpus:
        m = d["metadata"]
        if m.get("document_id") == REPORT_ID and m.get("page") in PDF_PAGES:
            pages.setdefault(m["page"], []).append(d["content"])
    if len(pages) != len(PDF_PAGES):
        sys.exit(f"the corpus has {len(pages)} of the {len(PDF_PAGES)} pages of appendix A")
    return "".join(f"<<<PAGE {p}>>>\n" + "\n".join(pages[p]).strip() + "\n\n" for p in sorted(pages))


def page_bodies(text):
    """Returns [(PDF page, body)] without the table header or the footnotes."""
    out = []
    for m in re.finditer(r"<<<PAGE (\d+)>>>\n(.*?)(?=<<<PAGE|\Z)", text, re.S):
        page, body = int(m.group(1)), m.group(2)
        head = re.search(r"etc\.\)[a-z]? ?\n", body)
        body = body[head.end():] if head else body
        foot = re.search(r"\n[a-i][A-Z][a-z]", body)
        body = body[:foot.start()] if foot else body
        out.append((page, re.sub(r"\s+", " ", body.replace("–", "-").replace("—", "-")).strip()))
    return out


# ----------------------------------------------------------------- fan calibration

NS = {"m": "http://schemas.openxmlformats.org/spreadsheetml/2006/main"}


def xlsx_rows(path):
    z = zipfile.ZipFile(path)
    shared = []
    if "xl/sharedStrings.xml" in z.namelist():
        for si in ET.fromstring(z.read("xl/sharedStrings.xml")).findall("m:si", NS):
            shared.append("".join(t.text or "" for t in si.iter("{%s}t" % NS["m"])))
    sheet = sorted(n for n in z.namelist() if re.match(r"xl/worksheets/sheet\d+\.xml", n))[0]
    rows = {}
    for row in ET.fromstring(z.read(sheet)).iter("{%s}row" % NS["m"]):
        cells = {}
        for c in row.findall("m:c", NS):
            v = c.find("m:v", NS)
            if v is not None:
                cells[re.match(r"[A-Z]+", c.get("r")).group()] = shared[int(v.text)] if c.get("t") == "s" else v.text
        rows[int(row.get("r"))] = cells
    return rows


def col_index(name):
    n = 0
    for ch in name:
        n = n * 26 + ord(ch) - 64
    return n


def col_name(n):
    s = ""
    while n:
        n, r = divmod(n - 1, 26)
        s = chr(65 + r) + s
    return s


def fnum(v):
    try:
        return float(v)
    except (TypeError, ValueError):
        return None


def band(values):
    """Range across calibration days: 10th and 90th percentiles with 5 or more days; otherwise, minimum and maximum."""
    if len(values) < 5:
        return min(values), max(values)
    q = statistics.quantiles(values, n=10, method="inclusive")
    return round(q[0], 2), round(q[-1], 2)


def load_calibration(path):
    """The spreadsheet has one block (Pot, Fan Display, Air Display) per calibration day, and inside each
    block three stacked sections: no restrictor, 1 restrictor, 2 restrictors. The air speed is
    U = 1.0066 × Air Display (the spreadsheet's own note). Returns, per number of restrictors,
    {pot: [air readings on each day]} and the pot -> fan display relation."""
    rows = xlsx_rows(path)
    header = next(n for n, c in rows.items() if list(c.values()).count("Pot") > 5)
    air = {0: {}, 1: {}, 2: {}}
    fan = {}
    for name, v in rows[header].items():
        if v != "Pot" or name == "A":  # block A is the spreadsheet's worked example, laid out differently
            continue
        ci = col_index(name)
        fcol, acol = col_name(ci + 1), col_name(ci + 2)
        section, inside = -1, False
        for n in range(header + 1, max(rows) + 1):
            pot = fnum(rows.get(n, {}).get(name))
            if pot is None:
                inside = False
                continue
            if not inside:
                section, inside = section + 1, True
            if section > 2:
                break
            f, a = fnum(rows[n].get(fcol)), fnum(rows[n].get(acol))
            if f is not None:
                fan.setdefault(pot, []).append(f)
            if a is not None:
                air[section].setdefault(pot, []).append(a)
    curves = {}
    for r, table in air.items():
        pts = sorted((p, statistics.median(v), *band(v), len(v)) for p, v in table.items())
        curves[r] = [{"pot": p, "air_median": md, "air_min": lo, "air_max": hi, "days": n} for p, md, lo, hi, n in pts]
    fan_curve = sorted((p, statistics.median(v)) for p, v in fan.items())
    return curves, fan_curve


def interp(x, pts):
    """Linear interpolation over sorted (x, y) points; outside the range, clamps to the edge."""
    if x <= pts[0][0]:
        return pts[0][1]
    for (x0, y0), (x1, y1) in zip(pts, pts[1:]):
        if x <= x1:
            return y0 + (y1 - y0) * (x - x0) / (x1 - x0) if x1 > x0 else y0
    return pts[-1][1]


class Fan:
    K = 1.0066  # cm/s per Air Display unit

    def __init__(self, path):
        self.curves, self.fan_curve = load_calibration(path)

    def pot_from_fan(self, fan):
        return round(interp(fan, [(f, p) for p, f in self.fan_curve]), 2)

    def flow(self, pot, restrictors):
        """(estimate, minimum, maximum) in cm/s for a potentiometer setting."""
        c = self.curves.get(restrictors) or self.curves[2]
        est = interp(pot, [(q["pot"], q["air_median"]) for q in c]) * self.K
        lo = interp(pot, [(q["pot"], q["air_min"]) for q in c]) * self.K
        hi = interp(pot, [(q["pot"], q["air_max"]) for q in c]) * self.K
        return round(est, 1), round(lo, 1), round(hi, 1)


# ----------------------------------------------------------------- report rows

MISSING = {"NA", "N/A", "none", "?", "video", "failed", "+OR", "Over"}  # +OR and Over: reading above full scale


def is_num(tok):
    return bool(re.fullmatch(r"-?\d+(?:\.\d+)?", tok))


def is_gap(tok):
    return tok in MISSING or bool(re.fullmatch(r"-{2,}|[<>]\d+", tok))


def find_tail(tokens):
    """Index of the first of the 8 numbers that close the row (calibrated O₂ initial and final, read O₂ initial
    and final, CO₂ initial and final, CO initial and final). The observation is whatever comes after them."""
    for i in range(len(tokens) - 7):
        w = tokens[i:i + 8]
        if not is_num(w[0]) or not all(is_num(t) or is_gap(t) for t in w) or sum(is_num(t) for t in w) < 4:
            continue
        a, c, d = float(w[0]), fnum(w[2]), fnum(w[3])
        if not 10 <= a <= 25:
            continue
        if c is not None and not 0 <= a - c <= 2.6:  # calibrated O₂ is up to ~1.6 points above the read value
            continue
        if c is None and (d is None or not 9 <= d <= 25):
            continue
        if any(v is not None and v > 9 for v in (fnum(w[4]), fnum(w[5]))):  # CO₂ never exceeds a few percent
            continue
        return i
    return None


def split_rows(bodies, table):
    """Finds each PSI table test in the appendix text and returns the stretch of its row."""
    flat, starts = "", []
    for page, body in bodies:
        starts.append((len(flat), page))
        flat += body + " "
    anchors = []
    for r in table:
        test, sample = r["Test #"].strip(), r["Sample #"].strip()
        pats = [r"(?<![\w.])(\d{1,3}[a-z]?) %s %s(?!\w)" % (re.escape(test), re.escape(sample)),
                r"(?<![\w.])(\d{1,3}[a-z]?) %s (\d{2,3}[a-zA-Z]{0,2})(?!\w)" % re.escape(test),
                r"(?<![\w.])(\d{1,3}[a-z]?) %s(?!\w)" % re.escape(test)]
        m = None
        for p in pats:
            m = re.search(p, flat, re.I)
            if m:
                break
        anchors.append((m.start() if m else None, m, r))
    found = sorted((a for a in anchors if a[0] is not None), key=lambda a: a[0])
    rows = {}
    for k, (pos, m, r) in enumerate(found):
        end = found[k + 1][0] if k + 1 < len(found) else len(flat)
        seg = flat[pos:end]
        # the end of the stretch carries the date and time of the next row
        dates = list(re.finditer(r"\d{1,2}/\d{1,2}/(?:20)?\d{2}\b", seg))
        if dates and re.fullmatch(r"[\sGMTto\d:\-.apmsg]*", seg[dates[-1].end():]):
            seg = seg[:dates[-1].start()]
        seg = re.sub(r"[\s-]{5,}$", "", seg)
        page = [p for s, p in starts if s <= pos][-1]
        rows[r["Test #"].strip()] = {"as_run": m.group(1), "text": seg.strip(), "pdf_page": page,
                                     "sample_report": m.group(2) if m.re.groups >= 2 else None}
    in_report = set(re.findall(r"(?<![\w.])\d{1,3}[a-z]? ([BFPMOT]\d{1,2}[a-z]?) \d{2,3}[a-zA-Z]{0,2}(?!\w)", flat))
    return rows, sorted(in_report - {r["Test #"].strip() for r in table})


# ----------------------------------------------------------------- classification

END_LABEL = {
    "quench": "went out when the airflow became too weak",
    "blowoff": "blown out by strong airflow",
    "self": "went out on its own, with no change to the airflow",
    "not_sustained": "ignited briefly but did not sustain",
    "fan_off": "burned until the fan was turned off",
    "burnout": "burned until the sample was consumed",
    "not_ignited": "did not ignite",
    "burned": "burned; the report does not say how it ended",
    "unknown": "the report does not describe what happened",
}
NEG = r"(?:did not|didn.t|not|no|never|without|unable to|couldn.t|could not|would not|wouldn.t|failed to|tried to)"
BLOW = r"(?:blow[- ]?off|blew[- ]?(?:off|out)|blown[- ]?(?:off|out)|blows? (?:off|out))"
EXT = r"(?:extin\w*|quench\w*|(?:flame|went|goes|and) out\b|died|\bout (?=at|immediately|when))"
GAP = r"(?:[^.;]|\.\d){0,60}?"  # within the same sentence; a period only if it is a decimal point
DOWN = r"(?:turn\w* (?:the )?(?:air|flow|fan|pot|it)? ?down|decreas\w*|reduc\w*|lower\w*|flow down|slow\w* (?:pot )?decr)"


def classify(obs, fan_txt, air_txt):
    """Text rules over the report observation. Returns how the flame ended, the fan setting
    (pot or fan display) or the air speed (cm/s) at that moment, and the attempts to blow the flame out
    that did not succeed."""
    o = re.sub(r"self ?- ?extin", "self-extin", obs.lower())
    r = {"ignited": None, "end": "unknown", "pot": None, "fan": None, "air": None, "blowoff_failed": False, "basis": []}
    if not o:
        return r

    flame = re.search(r"ignit|flame|burn|spread|blue|soot|extin|quench|blow|bubble layer", o)
    no_ign = re.search(r"(?:did not|didn.t|failed to|unable to|could not) ignite|no ignition", o)
    weak = re.search(r"(?:would|did|could) not sustain|no sustained flame|not sustain|would not stay lit|brief ignition|briefly flashed|"
                     r"brief flames? go out|brief flame|extinguished quickly|only stayed lit|did not spread at all", o)
    if no_ign and not weak:
        r.update(ignited=False, end="not_ignited")
        return r
    if weak:
        r.update(ignited=True, end="not_sustained")
        return r
    if flame:
        r["ignited"] = True
    o = re.sub(r"(?:did not|didn.t|not) (?:get )?extin\w*", "kept burning", o)

    blow = re.search(BLOW, o)
    blow_neg = re.search(NEG + r"[^.;]{0,40}" + BLOW, o) or re.search(BLOW + r"[^.;]{0,30}(?:without success|unsuccess|did not)", o) \
        or re.search(r"no " + BLOW, o)
    blow_yes = blow and (not blow_neg or re.search(r"(?:successful|finally|then|did) " + BLOW, o))
    if blow_neg and not blow_yes:
        r["blowoff_failed"] = True
    m = re.search(NEG + r"[^.;]{0,20}" + BLOW + r" at pot (\d+(?:\.\d+)?)", o)
    if m:
        r["blowoff_failed"], r["tried_pot"] = True, float(m.group(1))

    ext = re.search(EXT, o)
    at_air = re.search(EXT + GAP + r"(?:at|to|below)(?: an air reading of)? ~?(\d+(?:\.\d+)?) ?cm/s", o)
    at_pot = re.search(EXT + GAP + r"(?:pots?|at|to|of)(?: same| the)? (0?\.\d{1,3})(?! ?cm)\b", o) or re.search(r"(0?\.\d{1,3}) ?(?:pot )?\(?(?:flame )?(?:out|extin)", o)
    down = re.search(DOWN, o)
    fan_off = re.search(r"turn\w* (?:off|down) (?:the )?(?:flow|fan)(?: to 0| off)?\b(?! to (?:\d*[1-9]))|turn\w* (?:the )?(?:flow|fan) off|"
                        r"(?:pot|flow|fan) (?:turned |down )?to 0\b(?!\.)|\bfan off\b|flow to 0 to extin", o)
    fan_off = fan_off and re.search(r"\boff\b|to 0\b(?!\.)", fan_off.group())
    burnout = re.search(r"burn(?:ed|t)? (?:out|through|to (?:the )?end|complete\w*|entire|all|full|whole)|burnout|consumed|sample (?:mostly )?gone|"
                        r"(?:whole|full|entire) sample|ran out of (?:fuel|sample)", o)
    self_ext = re.search(r"self-extin|on its own", o)

    if blow_yes:
        r["end"] = "blowoff"
        m = re.search(BLOW + r"[^.;]{0,25}?fan (?:display )?(?:of |at )?(\d{2,3})\b", o) or re.search(r"fan (\d{2,3})[^.;]{0,20}" + BLOW, o)
        if m:
            r["fan"] = float(m.group(1))
        m = re.search(r"\b(?:pot|pt)\.? (?:of |at |to )?(\d+(?:\.\d+)?)", o)
        if m and r["fan"] is None:
            r["pot"] = float(m.group(1))
    elif fan_off:
        r["end"] = "fan_off"
    elif at_pot and not self_ext or at_pot and float(at_pot.group(1)) < 1:
        r.update(end="quench", pot=float(at_pot.group(1)))
    elif at_air:
        r.update(end="self" if self_ext or not down else "quench", air=float(at_air.group(1)))
    elif self_ext:
        r["end"] = "self"
    elif ext and down:
        r["end"] = "quench"
    elif burnout:
        r["end"] = "burnout"
    elif ext:
        r["basis"].append("went out, no reason given")
    elif r["ignited"]:
        r["end"] = "burned"
    return r


def first_air(air_txt):
    """Nominal starting speed, in cm/s, read from the Air display column."""
    t = air_txt or ""
    m = re.match(r"\s*(\d+(?:\.\d+)?)", t)
    v = float(m.group(1)) if m else None
    if v is not None and v >= 38:  # it is a fan display reading; the speed comes later, with a unit or in parentheses
        m = re.search(r"(\d+(?:\.\d+)?) ?cm/s", t) or re.search(r"\((\d+(?:\.\d+)?)", t)
        v = float(m.group(1)) if m else None
    return v if v is not None and 0 < v < 38 else None


def max_air(air_txt):
    vals = [float(x) for x in re.findall(r"(?<![\d.])\d+(?:\.\d+)?", air_txt or "")]
    vals = [v for v in vals if 0 < v <= 60]
    return max(vals) if vals else None


# ----------------------------------------------------------------- published boundary (acrylic rods)
# The report defines the flammability boundary of the rods by Eq. (6.1), p. 75:
#     X_O2 = 6·exp(-0.5·a) + 0.0167·a + 13.4          (X_O2 in % by volume)
# with the stretch rate a = 3/2 · (U/r) · (r/r0)^(1/2), in 1/s, and r0 = 0.318 cm (6.35 mm rod).
# The curve is not refitted here: it is compared with the points this script extracted from the appendix,
# which is an independent check of the extraction and of the fan calibration.

EQ61 = {"A": 6.0, "k": 0.5, "B": 0.0167, "C": 13.4, "r0_cm": 0.318}


def eq61(a):
    return EQ61["A"] * math.exp(-EQ61["k"] * a) + EQ61["B"] * a + EQ61["C"]


def rod_radius_cm(material):
    m = re.search(r"(\d)\s*/\s*(\d)", material)
    inches = int(m.group(1)) / int(m.group(2)) if m else fnum((re.search(r"(0?\.\d+)\s*\"", material) or [None, None])[1])
    return round(inches * 2.54 / 2, 4) if inches else None


def stretch(u, r):
    return 1.5 * u / math.sqrt(r * EQ61["r0_cm"])


def quench_flow(o2, r):
    """Air speed at which the published curve predicts extinction from too little air at this O₂ (lower branch)."""
    lo, hi = 0.0, 8.0  # the curve minimum lies near a = 8 to 12 1/s
    if not eq61(hi) < o2 < eq61(lo):
        return None
    for _ in range(60):
        mid = (lo + hi) / 2
        lo, hi = (mid, hi) if eq61(mid) > o2 else (lo, mid)
    return round(lo * math.sqrt(r * EQ61["r0_cm"]) / 1.5, 2)


def boundary(tests):
    rods = [t for t in tests if t.get("form") == "bastão de acrílico"]
    blow, quench, held = [], [], []
    for t in rods:
        r = rod_radius_cm(t["material"])
        t["rod_radius_cm"] = r
        for p in t["points"]:
            if r and p.get("flow_cm_s") is not None:
                p["stretch_s"] = round(stretch(p["flow_cm_s"], r), 1)
            if not r or p.get("flow_cm_s") is None:
                continue
            if p["state"] == "apagou_vento":
                blow.append({"test": t["test"], "o2": p["o2"], "flow_cm_s": p["flow_cm_s"], "stretch_s": p["stretch_s"],
                             "curve_o2": round(eq61(p["stretch_s"]), 2), "diff_o2": round(p["o2"] - eq61(p["stretch_s"]), 2)})
            elif p["state"] == "vento_nao_apagou":
                held.append({"test": t["test"], "o2": p["o2"], "flow_cm_s": p["flow_cm_s"], "curve_o2": round(eq61(p["stretch_s"]), 2),
                             "consistent": p["o2"] > eq61(p["stretch_s"])})
            elif p["state"] == "apagou_pouco_ar":
                u = quench_flow(p["o2"], r)
                # below 2 cm/s the anemometer cannot tell values apart: it is enough that the curve also asks for less than 2 cm/s
                quench.append({"test": t["test"], "o2": p["o2"], "geometry": t.get("geometry"), "curve_flow_cm_s": u,
                               "inside_band": u is not None and u <= 2.0,
                               "off_curve": None if u is not None else "at the bottom of the curve" if p["o2"] <= eq61(8.0) + 0.3 else "above the curve value for still air"})
    diffs = [b["diff_o2"] for b in blow]
    rms = math.sqrt(sum(d * d for d in diffs) / len(diffs)) if diffs else None
    # own straight-line fit on the blowoff branch, to compare with the published linear term (0.0167·a + 13.4)
    fit = None
    if len(blow) >= 3:
        xs, ys = [b["stretch_s"] for b in blow], [b["o2"] for b in blow]
        mx, my = sum(xs) / len(xs), sum(ys) / len(ys)
        sxx = sum((x - mx) ** 2 for x in xs)
        slope = sum((x - mx) * (y - my) for x, y in zip(xs, ys)) / sxx
        inter = my - slope * mx
        res = [y - (inter + slope * x) for x, y in zip(xs, ys)]
        s2 = sum(e * e for e in res) / (len(xs) - 2)
        fit = {"slope": round(slope, 4), "intercept": round(inter, 2), "slope_se": round(math.sqrt(s2 / sxx), 4),
               "rms_o2": round(math.sqrt(s2), 2), "n": len(xs), "published": {"slope": EQ61["B"], "intercept": EQ61["C"]}}
    with_flow = [q for q in quench if q["curve_flow_cm_s"] is not None]
    return {
        "form": "bastão de acrílico",
        "published": {"equation": "X_O2 = 6·exp(−0.5·a) + 0.0167·a + 13.4", "stretch": "a = 3/2 · (U/r) · (r/r0)^½, with r0 = 0.318 cm",
                      "coef": EQ61, "source": f"{REPORT['number']}, Eq. (6.1), p. 75", "url": REPORT["url"],
                      "minimum": "about 13.5% O₂ with a between 8 and 12 1/s",
                      "applies_to": "acrylic rods with the flame spreading in the direction of the airflow (concurrent), in microgravity and at 1 atm"},
        "check": {
            "blowoff": {"n": len(blow), "mean_diff_o2": round(sum(diffs) / len(diffs), 2) if diffs else None,
                        "rms_diff_o2": round(rms, 2) if rms is not None else None,
                        "max_abs_diff_o2": round(max(abs(d) for d in diffs), 2) if diffs else None, "points": blow},
            "held": {"n": len(held), "consistent": sum(1 for h in held if h["consistent"]), "points": held},
            "quench": {"n": len(quench), "on_curve": len(with_flow), "inside_band": sum(1 for q in with_flow if q["inside_band"]),
                       "off_curve": [{"test": q["test"], "o2": q["o2"], "geometry": q["geometry"], "why": q["off_curve"]} for q in quench if q["off_curve"]],
                       "still_air_o2": round(eq61(0.0), 1),
                       "curve_flow_cm_s": [min(q["curve_flow_cm_s"] for q in with_flow), max(q["curve_flow_cm_s"] for q in with_flow)] if with_flow else None,
                       "points": quench},
            "own_blowoff_line": fit,
        },
        "band_o2": round(rms, 2) if rms is not None else None,
        "tested": {"o2": [min(p["o2"] for t in rods for p in t["points"]), max(p["o2"] for t in rods for p in t["points"])],
                   "flow_cm_s_max": 55, "flow_source": f"{REPORT['number']}, p. 89: the duct produced airflows of 0 to 55 cm/s over the rods",
                   "pressure_atm": 1.0, "gravity": 0.0},
        # natural (buoyant) flow that gravity creates next to the flame, added to the forced flow (as the report does in Fig. 6.22)
        "buoyancy": {"flow_1g_cm_s": [20, 35], "superposition": "linear, as in Fig. 6.22 of the report",
                     "sources": [{"value": 20, "ref": f"{REPORT['number']}, p. 89 (Ref. 32)", "url": REPORT["url"]},
                                 {"value": 35, "ref": "Solid Fuel Ignition Testing for Lunar Applications (Takahashi's estimate)",
                                  "url": "https://ntrs.nasa.gov/citations/20250002114"}],
                     "partial_gravity": "There is no measured value for the Moon or Mars; the lunar ignition report states that the flow speed in lunar gravity is not known.",
                     "earth_check": {"o2": 15.4, "flow_cm_s": 24, "curve_o2": round(eq61(stretch(24, EQ61["r0_cm"])), 2),
                                     "ref": f"{REPORT['number']}, p. 89: limit at 1 g of 15.4% O₂ with 20 cm/s of buoyant flow plus 4 cm/s forced"}},
    }


# ----------------------------------------------------------------- main


def pct(v):
    x = fnum(v)
    return x if x is not None and 5 <= x <= 30 else None


def o2_artifact(nums):
    """Explains an impossible final calibrated O₂ by the arithmetic that produces it, when the arithmetic checks out."""
    cal_i, cal_f, raw_i, raw_f = nums[:4]
    f = lambda v: f"{v:g}"
    if cal_f is None or cal_i is None:
        return None
    if raw_f is None and raw_i is not None and abs((cal_i - raw_i) - cal_f) <= 0.06:
        return (f"{f(cal_f)} is the difference between the calibrated and the read O₂ at the start of the test ({f(cal_i)} − {f(raw_i)}). "
                "The final reading is blank in the report: the calibration correction was left alone in the cell.")
    if raw_i is None and raw_f is not None and abs((cal_i + raw_f) - cal_f) <= 0.06:
        return (f"{f(cal_f)} is the sum of the initial calibrated O₂ and the final read O₂ ({f(cal_i)} + {f(raw_f)}). "
                "The initial reading is blank in the report.")
    return None


def build(table, rows, fan, overrides, report_only=()):
    tests, audit = [], []
    pi_names = {"B": "Bhattacharjee", "F": "Ferkul", "P": "Fernandez-Pello", "M": "Miller", "O": "Olson", "T": "T'ien"}
    for n, r in enumerate(table, start=2):
        test = r["Test #"].strip()
        row = rows.get(test)
        o2i_t, o2f_t = fnum(r["Calibrated  initial O2 % by vol "]), fnum(r["Calibrated final O2 % by vol"])
        rec = {"test": test, "pi": pi_names.get(test[0], r["PI"]), "table_row": n, "sample": r["Sample #"].strip(),
               "material": re.sub(r"\s+", " ", r["Fuel Sample Material"]).strip(),
               "restrictors": int(fnum(r["Flow restrictor"])) if fnum(r["Flow restrictor"]) is not None else None,
               "fan_display": re.sub(r"\s+", " ", r["Fan display"]).strip(), "air_display": re.sub(r"\s+", " ", r["Air display"]).strip()}
        if not row:
            rec.update({"found": False, "observation": None, "end": "unknown", "end_label": END_LABEL["unknown"], "ignited": None, "blowoff_failed": False,
                        "o2_initial": pct(o2i_t), "o2_final": pct(o2f_t), "points": [], "flags": ["row not found in the report"]})
            # "teste" is a field id (the front compares it); keep it as is
            audit.append({"test": test, "table_row": n, "field": "teste", "table": "present", "report": None,
                          "kind": "table test missing from the report", "severity": "média",
                          "note": "It is in the PSI table and does not appear in any of the test matrices in appendix A of the report."})
            ov = overrides.get(test)
            if ov:
                rec.update({k2: v for k2, v in ov.items() if k2 != "why"})
                rec["reviewed"] = ov.get("why", "corrected by hand")
                finish(rec, fan)
            else:
                rec["form"] = form_of(rec["material"])
            tests.append(rec)
            continue
        tokens = row["text"].split(" ")
        k = find_tail(tokens)
        flags = []
        if k is None:
            obs, nums = "", [None] * 8
            flags.append("trailing numbers of the row not recognized")
        else:
            nums = [fnum(t) for t in tokens[k:k + 8]]
            obs = " ".join(tokens[k + 8:]).strip()
        geo = re.search(r"\b(Opposed|Concurrent)\b", " ".join(tokens[:k] if k else tokens))
        o2i, o2f = pct(nums[0]) or pct(o2i_t), pct(nums[1])
        if o2f is None and pct(o2f_t) is not None and nums[1] is None:
            o2f = pct(o2f_t)
        # audit: the PSI table against the report
        for label, tv, rv in (("Calibrated initial O₂ (%)", o2i_t, nums[0]), ("Calibrated final O₂ (%)", o2f_t, nums[1]),
                              ("Initial CO₂ (%)", fnum(r["Initial CO2 % by vol"]), nums[4]), ("Final CO₂ (%)", fnum(r["Final CO2 % by vol"]), nums[5]),
                              ("Initial CO (ppm)", fnum(r["Initial CO (ppm)"]), nums[6]), ("Final CO (ppm)", fnum(r["Final CO (ppm)"]), nums[7])):
            if k is None or (tv is None and rv is None):
                continue
            where = {"pdf_page": row["pdf_page"], "report_page": row["pdf_page"] - 8}
            is_o2 = label.startswith("Calibrated")
            bad_t = is_o2 and tv is not None and not 5 <= tv <= 30
            bad_r = is_o2 and rv is not None and not 5 <= rv <= 30
            same = tv is not None and rv is not None and abs(tv - rv) <= 0.051
            if bad_t or bad_r:
                why = o2_artifact(nums) or "The value cannot be a measurement of oxygen in the cabin."
                kind = "impossible value in both sources" if bad_t and (bad_r or same) else \
                    "impossible value in the table" if bad_t else "impossible value in the report"
                if not bad_t:
                    why += " The PSI table leaves the field blank, which is correct."
                audit.append({"test": test, "table_row": n, "field": label, "table": tv, "report": rv, **where, "kind": kind,
                              "severity": "alta" if bad_t else "média", "note": why})
            elif not same:
                kind = "missing from the table" if tv is None else "missing from the report" if rv is None else "different values"
                note = {"missing from the table": "The report has the value and the PSI table is blank.",
                        "missing from the report": "The PSI table has the value and the report is blank.",
                        "different values": ("Difference of " + f"{abs(tv - rv):g}" + ".") if tv is not None and rv is not None else ""}[kind]
                audit.append({"test": test, "table_row": n, "field": label, "table": tv, "report": rv, **where, "kind": kind,
                              "severity": "média", "note": note})
        # the same number one column over: the table's final CO in the report's initial CO column
        ci_t, cf_t = fnum(r["Initial CO (ppm)"]), fnum(r["Final CO (ppm)"])
        if k is not None and cf_t is not None and nums[6] is not None and ci_t != nums[6] and abs(cf_t - nums[6]) <= 0.051:
            for a_ in audit:
                if a_["test"] == test and a_["field"].endswith(" CO (ppm)"):
                    a_["kind"], a_["note"] = "shifted columns", (
                        f"The table's final CO ({cf_t:g} ppm) appears in the report as the initial CO. One of the two sources is shifted by one column.")
        sr = row.get("sample_report")
        if sr and sr.lower() != rec["sample"].lower():
            audit.append({"test": test, "table_row": n, "field": "sample", "table": rec["sample"], "report": sr,
                          "pdf_page": row["pdf_page"], "report_page": row["pdf_page"] - 8, "kind": "different values",
                          "severity": "baixa", "note": "The sample number is not the same in the two sources."})
        c = classify(obs, rec["fan_display"], rec["air_display"])
        # geometry values are ids (ml/build_registry.py maps them to opposed/concurrent); keep them as is
        rec.update({"found": True, "as_run": row["as_run"], "pdf_page": row["pdf_page"], "report_page": row["pdf_page"] - 8,
                    "geometry": {"Opposed": "contra o vento", "Concurrent": "a favor do vento"}.get(geo.group(1)) if geo else None,
                    "o2_initial": o2i, "o2_final": o2f, "observation": obs or None, "ignited": c["ignited"], "end": c["end"],
                    "pot": c["pot"], "fan": c["fan"], "air": c["air"], "blowoff_failed": c["blowoff_failed"], "tried_pot": c.get("tried_pot"), "basis": c["basis"], "flags": flags})
        ov = overrides.get(test)
        if ov:
            rec.update({k2: v for k2, v in ov.items() if k2 != "why"})
            rec["reviewed"] = ov.get("why", "corrected by hand")
        finish(rec, fan)
        tests.append(rec)
    # two columns of the same row that disagree
    for t in tests:
        for fl in t.get("flags") or []:
            if "fan column" in fl:
                audit.append({"test": t["test"], "table_row": t["table_row"], "field": "fan setting", "table": None, "report": None,
                              "pdf_page": t.get("pdf_page"), "report_page": t.get("report_page"), "kind": "two columns of the same row disagree",
                              "severity": "baixa", "note": fl.split(";")[0].capitalize() + "."})
    # tests that only exist in the report
    for tid in report_only:
        audit.append({"test": tid, "table_row": None, "field": "teste", "table": None, "report": "present",
                      "kind": "report test missing from the table", "severity": "média",
                      "note": "It appears in the test matrices of appendix A and is not in the PSI table."})
    # gaps in the numbering shared by both sources
    known = {t["test"] for t in tests} | set(report_only)
    for prefix in sorted({t[0] for t in known}):
        nums_ = sorted({int(re.match(r"[A-Z](\d+)", t).group(1)) for t in known if t[0] == prefix})
        for miss in sorted(set(range(1, nums_[-1] + 1)) - set(nums_)):
            audit.append({"test": f"{prefix}{miss}", "table_row": None, "field": "teste", "table": None, "report": None,
                          "kind": "test number with no record", "severity": "baixa",
                          "note": f"The numbering jumps from {prefix}{miss - 1} to {prefix}{miss + 1} in both sources. They give no way to know whether the test was run."})
    order = {"alta": 0, "média": 1, "baixa": 2}
    audit.sort(key=lambda a_: order[a_["severity"]])
    return tests, audit


def form_of(material):
    # the returned values are form ids shared with the API and the front; keep them as is
    t = material.lower()
    if "nomex" in t:
        return "tecido Nomex"
    if "sibal" in t:
        return "tecido de algodão (SIBAL)"
    if "wax" in t or "candle" in t:
        return "vela de cera"
    if "spher" in t:
        return "esfera de acrílico"
    if "rod" in t:
        return "bastão de acrílico"
    if "film" in t or "micron" in t or "µm" in t:
        return "filme fino de acrílico"
    if "pmma" in t or "thick" in t or "sided" in t:
        return "placa de acrílico"
    return "outro"


def finish(rec, fan):
    """Converts the fan setting into cm/s and builds the points of the flammability map."""
    rec["end_label"] = END_LABEL[rec["end"]]
    rec["form"] = form_of(rec["material"])
    rec.setdefault("basis", [])
    rs = rec["restrictors"] if rec["restrictors"] in (0, 1, 2) else 2
    pot = rec.get("pot")
    if pot is None and rec.get("fan") is not None:
        pot = fan.pot_from_fan(rec["fan"])
        rec["basis"].append(f"fan display {rec['fan']:g} converted to pot {pot:g}")
    other = re.search(r"blowoff (?:fan )?(?:pot|pt) (\d+(?:\.\d+)?)", rec["fan_display"].lower())
    other = float(other.group(1)) if other and rec["end"] == "blowoff" and pot is not None else None
    limit = None
    if rec["end"] in ("quench", "blowoff", "self") and rec.get("air") is not None:
        limit = {"flow_cm_s": rec["air"], "source": "air speed quoted in the observation (anemometer)"}
    elif rec["end"] in ("quench", "blowoff") and pot is not None:
        est, lo, hi = fan.flow(pot, rs)
        limit = {"pot": pot, "flow_cm_s": est, "flow_min": lo, "flow_max": hi,
                 "source": f"pot {pot:g} with {rs} restrictor(s), converted with the fan calibration"}
        if rec["end"] == "quench" and pot < 0.5:
            limit["below_resolution"] = True  # below the lowest calibrated point (pot 0.5)
        if other is not None and abs(other - pot) >= 1:
            e2, l2, h2 = fan.flow(other, rs)
            limit["flow_min"], limit["flow_max"] = min(lo, l2), max(hi, h2)
            rec.setdefault("flags", []).append(
                f"the fan column records the extinction at pot {other:g} and the observation gives fan display {rec.get('fan') or 0:g}, which corresponds to pot {pot:g}; the speed range covers both")
    rec["limit"] = limit
    o2i, o2f = rec.get("o2_initial"), rec.get("o2_final")
    o2_end = o2f if o2f is not None else o2i
    pts = []
    start = first_air(rec["air_display"])
    burned = rec["ignited"] and rec["end"] not in ("not_sustained", "not_ignited", "unknown")
    # START_SOURCE stays in Portuguese: brain/atlas/outcomes.py looks for "partida" in it to find the starting speed
    if burned and start is not None and o2i is not None:
        pts.append({"state": "queimou", "o2": o2i, "flow_cm_s": start, "source": START_SOURCE})
    if rec.get("burn_pot") is not None and o2_end is not None:
        est, lo, hi = fan.flow(rec["burn_pot"], rs)
        pts.append({"state": "queimou", "o2": o2_end, "flow_cm_s": est, "flow_range": [lo, hi],
                    "source": f"steady flame at pot {rec['burn_pot']:g} with {rs} restrictor(s)"})
    if limit and o2_end is not None:
        state = {"quench": "apagou_pouco_ar", "blowoff": "apagou_vento", "self": "apagou_sozinha"}[rec["end"]]
        p = {"state": state, "o2": o2_end, "flow_cm_s": limit["flow_cm_s"], "source": limit["source"]}
        if o2i is not None and o2i != o2_end:
            p["o2_range"] = sorted([o2_end, o2i])
        if "flow_min" in limit:
            p["flow_range"] = [limit["flow_min"], limit["flow_max"]]
        pts.append(p)
    if rec.get("blowoff_failed") and o2_end is not None:
        top = max_air(rec["air_display"])
        if rec.get("tried_pot") is not None:
            top, src = fan.flow(rec["tried_pot"], rs)[0], f"pot {rec['tried_pot']:g} with {rs} restrictor(s), converted with the fan calibration"
        elif top is None or top < 8:
            top, src = fan.flow(10.0, rs)[0], f"maximum fan flow with {rs} restrictor(s)"
        else:
            src = "highest speed in the Air display column"
        pts.append({"state": "vento_nao_apagou", "o2": o2_end, "flow_cm_s": top, "source": src})
    if rec["end"] in ("not_sustained", "not_ignited") and o2i is not None and start is not None:
        pts.append({"state": "nao_sustentou", "o2": o2i, "flow_cm_s": start, "source": START_SOURCE})
    rec["points"] = pts


START_SOURCE = "velocidade de partida na coluna Air display"  # = starting speed in the Air display column


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--from-corpus", action="store_true", help="rebuild the appendix text from the RAG corpus")
    ap.add_argument("--corpus", default=CORPUS)
    ap.add_argument("--print", action="store_true", help="list each test with its classification, for review")
    args = ap.parse_args()

    if args.from_corpus or not os.path.exists(APPENDIX):
        os.makedirs(os.path.dirname(APPENDIX), exist_ok=True)
        open(APPENDIX, "w", encoding="utf-8").write(appendix_from_corpus(args.corpus))
        print(f"appendix A written to {os.path.relpath(APPENDIX, ROOT)}")
    text = open(APPENDIX, encoding="utf-8").read()
    table = list(csv.DictReader(io.open(TABLE, encoding="utf-8-sig")))
    fan = Fan(CALIB)
    overrides = json.load(open(OVERRIDES, encoding="utf-8")) if os.path.exists(OVERRIDES) else {}
    rows, report_only = split_rows(page_bodies(text), table)
    tests, audit = build(table, rows, fan, overrides, report_only)

    ends = {}
    for t in tests:
        ends[t["end"]] = ends.get(t["end"], 0) + 1
    states = {}
    for t in tests:
        for p in t["points"]:
            states[p["state"]] = states.get(p["state"], 0) + 1
    out = {
        "investigation": "PSI-25", "report": REPORT, "table": os.path.basename(TABLE), "calibration": os.path.basename(CALIB),
        "method": "Text rules over the observations column of appendix A, matched to the PSI table by test number. "
                  "Air speed: potentiometer setting converted with the fan calibration (U = 1.0066 × Air Display), "
                  "median across calibration days, with minimum and maximum. Hand corrections in PSI-25.overrides.json.",
        "summary": {"tests": len(tests), "found": sum(1 for t in tests if t["found"]), "with_observation": sum(1 for t in tests if t.get("observation")),
                    "with_end": sum(1 for t in tests if t["end"] not in ("unknown", "burned")), "ends": ends, "points": states,
                    "reviewed": sum(1 for t in tests if t.get("reviewed")), "audit": len(audit)},
        "calibration_curves": fan.curves, "boundaries": [boundary(tests)], "tests": tests, "audit": audit,
    }
    os.makedirs(os.path.dirname(OUT), exist_ok=True)
    json.dump(out, open(OUT, "w", encoding="utf-8", newline="\n"), ensure_ascii=False, indent=1)
    print(json.dumps(out["summary"], ensure_ascii=False))
    if args.print:
        for t in tests:
            lim = t.get("limit") or {}
            print(f"\n{t['test']:5} p.{t.get('report_page')} O2 {t.get('o2_initial')}>{t.get('o2_final')} r{t['restrictors']} {t.get('geometry')} | fan[{t['fan_display']}] air[{t['air_display']}]"
                  f"\n      -> {t['end']} ign={t['ignited']} pot={t.get('pot')} fan={t.get('fan')} air={t.get('air')} U={lim.get('flow_cm_s')} blowoff_failed={t.get('blowoff_failed')} {t.get('flags') or ''}"
                  f"\n      {t.get('observation')}")


if __name__ == "__main__":
    main()
