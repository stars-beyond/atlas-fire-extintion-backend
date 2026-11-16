"""Gera data/seed/external/zarm.json: os ensaios da torre de queda do ZARM como evidência de fora da NASA.

Fonte: ml/registry.csv (linhas zarm...) e ml/registry_barriers.csv, que por sua vez vêm dos índices
publicados no Zenodo (18479713 e 14959942, CC-BY-NC-4.0). Só biblioteca padrão.

Estes ensaios NÃO entram no acervo do PSI nem no nível de evidência: a API os carrega numa lista à
parte (Catalog.External) e os mostra sempre rotulados como de fora da NASA. São os únicos testes de
sólido em 0 g acima de 22,2 % de O2 que o Atlas conhece.

Quatro ensaios aparecem nos dois registros (mesma data e condições); fica o do registro 18479713, que
tem a taxa de propagação medida, e o outro vira `also_in`.

Uso: python3 scripts/make_external.py
"""
import csv
import json
import os
from collections import Counter

AQUI = os.path.dirname(os.path.abspath(__file__))
RAIZ = os.path.join(AQUI, "..")
SAIDA = os.path.join(RAIZ, "data", "seed", "external", "zarm.json")
KPA_ATM = 101.325

RECORDS = {
    "zenodo:18479713": {
        "id": "zenodo:18479713", "short": "metal barriers",
        "title": "Flame Spread and Heat Transfer over Discrete Fuels separated by Metal Barriers under Microgravity",
        "authors": "Ries, Krieger, Meyer et al.", "url": "https://doi.org/10.5281/zenodo.18479713",
    },
    "zenodo:14959942": {
        "id": "zenodo:14959942", "short": "air gaps",
        "title": "Infra Red Recordings of Opposed-Flow Flame Propagation on Discrete PMMA Sheets",
        "authors": "Ries, Krieger, Meyer et al.", "url": "https://zenodo.org/records/14959942",
    },
}
BARREIRA = {"Stainless Steel": "stainless-steel barriers", "Aluminium": "aluminium barriers", "Air": "air gaps"}


def num(v):
    return float(v) if v not in ("", None) else None


def main():
    with open(os.path.join(RAIZ, "ml", "registry.csv"), encoding="utf-8") as f:
        linhas = [r for r in csv.DictReader(f) if r["trial_id"].startswith("zarm")]
    with open(os.path.join(RAIZ, "ml", "registry_barriers.csv"), encoding="utf-8") as f:
        barreiras = [b for b in csv.DictReader(f) if b["trial_id"]]
    por_ensaio = {}
    for b in barreiras:
        por_ensaio.setdefault(b["trial_id"], []).append(b)

    tests = []
    for n, r in enumerate(linhas, start=2):                      # linha do registry.csv (1 = cabeçalho)
        if r["source"] == "zenodo:14959942" and r["same_run_as"]:
            continue                                             # o mesmo ensaio físico já entra pelo outro registro
        rec = RECORDS[r["source"]]
        meio = BARREIRA.get(r["interruption_material"], "")
        vaos = r["interruption"].replace("/", ", ")
        amostra = "continuous film" if not r["interruption"] else f"film split by {meio} of {vaos} mm"
        if r["outcome"] == "burned":
            outcome = "queimou"
            raw = "burned along the whole continuous sample" if not r["interruption"] else f"the flame crossed every measured barrier ({vaos} mm)"
        elif r["outcome"] == "extinguished":
            outcome = "extinguiu"
            paradas = [b for b in por_ensaio.get(r["trial_id"], []) if b["extinction"] == "Yes"]
            onde = f" of {paradas[0]['gap_mm'].rstrip('0').rstrip('.')} mm" if paradas else ""
            artigo = "an" if meio[:1] in "aeiou" else "a"
            raw = f"the flame went out at {artigo} {meio[:-1]}{onde}: stopped by the firebreak, not by the air"
        else:
            outcome = "desconhecido"
            raw = "ignited; whether the flame crossed the air gaps has not been read from the infrared recording"
        tests.append({
            "id": r["trial_id"], "record": r["source"], "run": r["test_id"], "label": f"run {r['test_id']} ({rec['short']})",
            "o2": round(num(r["o2_pct"]) / 100, 4), "pressure_atm": round(num(r["pressure_kpa"]) / KPA_ATM, 4),
            "pressure_kpa": num(r["pressure_kpa"]), "gravity": 0.0, "flow_cm_s": num(r["flow_cm_s"]),
            "material": "pmma", "fuel": f"PMMA film {r['thickness_mm']} mm", "sample": amostra,
            "outcome": outcome, "outcome_raw": raw, "spread_mm_s": num(r["spread_rate_mm_s"]),
            "barriers": [{"material": b["material"], "gap_mm": num(b["gap_mm"]), "stopped": b["extinction"] == "Yes"}
                         for b in por_ensaio.get(r["trial_id"], [])],
            "also_in": r["same_run_as"] or None,
            "source_file": "ml/registry.csv", "source_row": n, "provenance": r["provenance"],
        })

    cont = Counter(t["outcome"] for t in tests)
    doc = {
        "schema": "atlas.external/1",
        "id": "EXT-ZARM", "name": "ZARM drop tower", "agency": "ZARM, University of Bremen (Germany)",
        "what": "Thin acrylic film (0.075 mm PMMA) burning in weightlessness, with the flame moving against a 9 to 20 cm/s airflow, recorded by an infrared camera. Each drop gives a few seconds of 0 g.",
        "not_nasa": "These tests are not in NASA's PSI archive. They are shown apart and do not change the NASA evidence level.",
        "license": "CC-BY-NC-4.0", "records": list(RECORDS.values()),
        "summary": {
            "tests": len(tests), "outcomes": dict(cont), "with_spread_rate": sum(t["spread_mm_s"] is not None for t in tests),
            "o2": [min(t["o2"] for t in tests), max(t["o2"] for t in tests)],
            "pressure_atm": [min(t["pressure_atm"] for t in tests), max(t["pressure_atm"] for t in tests)],
            "flow_cm_s": [min(t["flow_cm_s"] for t in tests), max(t["flow_cm_s"] for t in tests)],
            "same_run_dropped": sum(1 for r in linhas if r["source"] == "zenodo:14959942" and r["same_run_as"]),
        },
        "tests": tests,
    }
    os.makedirs(os.path.dirname(SAIDA), exist_ok=True)
    with open(SAIDA, "w", encoding="utf-8") as f:
        json.dump(doc, f, ensure_ascii=False, indent=1)
        f.write("\n")
    print(f"gravado {os.path.relpath(SAIDA, RAIZ)}: {len(tests)} ensaios {dict(cont)}, {doc['summary']['with_spread_rate']} com taxa de propagação")


if __name__ == "__main__":
    main()
