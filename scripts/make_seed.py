"""Builds data/seed from an earlier PSI download (optional).

Ingestion fetches everything from the PSI on its own. The seed only ensures the Atlas
starts without network access: trimmed catalogs (without the raw file lists) and the
experimental tables. Usage: python3 scripts/make_seed.py <folder with PSI-*/catalogo.json> <tables folder>
"""
import glob
import json
import os
import shutil
import sys

RAIZ = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
KEEP = ["accession", "investigationAcronym", "title", "description", "objective", "approach", "hypothesis",
        "researchImpacts", "experimentHardware", "experimentHardwareLink", "flightProgram", "projectType",
        "researchArea", "subResearchArea", "investigationStartDate", "investigationEndDate", "managingNasaCenter",
        "doi", "totalFileSize", "releaseDate", "funding", "licenseName"]


def enxugar(cat):
    out = {k: cat.get(k) for k in KEEP}
    out["publications"] = [{"title": p.get("title", ""), "authorList": p.get("authorList", ""), "doi": p.get("doi", "")}
                           for p in cat.get("publications") or []]
    out["contacts"] = [{"firstName": c.get("firstName", ""), "lastName": c.get("lastName", ""),
                        "affiliation": c.get("affiliation", "")} for c in cat.get("contacts") or []]
    arquivos = []
    for a in list(cat.get("experimentalTable") or []) + list(cat.get("added") or []):
        nome = a.get("file_name", "")
        partes = (a.get("remote_path") or "").split("/")
        cat_arq = partes[4] if len(partes) > 5 else ""
        if nome.lower().endswith((".pdf", ".csv")) and "ProofSheet" not in nome and cat_arq != "Raw Data":
            arquivos.append({"file_name": nome, "file_size": a.get("file_size") or 0, "category": cat_arq})
    out["files"] = arquivos
    out["file_count"] = len(cat.get("added") or [])
    return out


def main(dados, tabelas):
    n = 0
    for p in sorted(glob.glob(os.path.join(dados, "PSI-*", "catalogo.json"))):
        cat = json.load(open(p))
        dest = os.path.join(RAIZ, "data", "seed", "catalog", cat["accession"] + ".json")
        json.dump(enxugar(cat), open(dest, "w"), ensure_ascii=False, indent=1)
        n += 1
    t = 0
    for f in sorted(glob.glob(os.path.join(tabelas, "*.csv"))):
        shutil.copy(f, os.path.join(RAIZ, "data", "seed", "tables", os.path.basename(f)))
        t += 1
    print(f"seed: {n} catalogs, {t} tables")


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2])
