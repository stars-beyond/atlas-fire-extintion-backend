"""Cliente do NASA Physical Sciences Informatics (PSI). Sem login.

  busca       GET /repo/search?...ffield=Research Area&fvalue=combustion science
  catálogo    GET /repo/investigations/PSI-xx
  arquivo     GET /studies/PSI-xx/download?source=datamanager&file=<nome>  (302 para S3 pré-assinado)

Tudo passa por um cache em disco. Sem rede, vale o cache e depois a semente do repositório.
"""
import glob
import json
import os
import re
import time
from urllib.parse import quote

import requests

from . import config

_s = requests.Session()
_s.headers["User-Agent"] = "atlas-extincao/1.0"
KEEP = ["accession", "investigationAcronym", "title", "description", "objective", "approach", "hypothesis",
        "researchImpacts", "experimentHardware", "experimentHardwareLink", "flightProgram", "projectType",
        "researchArea", "subResearchArea", "investigationStartDate", "investigationEndDate", "managingNasaCenter",
        "doi", "totalFileSize", "releaseDate", "funding", "licenseName"]


def safe(name):
    return re.sub(r"[^\w.\- ]", "_", name).strip()


def online():
    return config.NETWORK != "off"


def _slim(cat):
    """Mantém só o que o Atlas usa; a lista de arquivos brutos (centenas de KB) fica de fora."""
    if "files" in cat:
        return cat
    out = {k: cat.get(k) for k in KEEP}
    out["publications"] = [{"title": p.get("title", ""), "authorList": p.get("authorList", ""), "doi": p.get("doi", "")}
                           for p in cat.get("publications") or []]
    out["contacts"] = [{"firstName": c.get("firstName", ""), "lastName": c.get("lastName", ""),
                        "affiliation": c.get("affiliation", "")} for c in cat.get("contacts") or []]
    files = []
    for a in list(cat.get("experimentalTable") or []) + list(cat.get("added") or []):
        name = a.get("file_name", "")
        parts = (a.get("remote_path") or "").split("/")
        category = parts[4] if len(parts) > 5 else ""
        if name.lower().endswith((".pdf", ".csv")) and "ProofSheet" not in name and category != "Raw Data":
            files.append({"file_name": name, "file_size": a.get("file_size") or 0, "category": category})
    out["files"] = files
    out["file_count"] = len(cat.get("added") or [])
    return out


def list_combustion():
    """Acessos (PSI-xx) das investigações de combustão. Devolve (lista, origem)."""
    if online():
        try:
            url = (f"{config.PSI}/repo/search?q=&data_source=psi&data_type=investigation&size=500"
                   "&ffield=Research%20Area&fvalue=combustion%20science")
            hits = _s.get(url, timeout=40).json()["hits"]["hits"]
            accs = sorted({h["_source"]["Accession"] for h in hits})
            if accs:
                return accs, "psi"
        except Exception as e:  # rede fora, API mudou: segue com o que há em disco
            print(f"  [psi] busca indisponível ({type(e).__name__}); usando cache/semente")
    local = {os.path.basename(p)[:-5] for d in ("cache/catalog", "seed/catalog")
             for p in glob.glob(os.path.join(config.DATA, d, "PSI-*.json"))}
    return sorted(local), "disco"


def catalog(acc):
    """Catálogo enxuto de uma investigação. Devolve (dict, origem)."""
    cached = os.path.join(config.CACHE, "catalog", acc + ".json")
    # O cache vale por CATALOG_TTL_H horas: depois disso o catálogo é buscado de novo, e arquivos
    # novos publicados no PSI entram na próxima ingestão. Sem rede, o cache vale mesmo vencido.
    fresh = os.path.exists(cached) and time.time() - os.path.getmtime(cached) < config.CATALOG_TTL_H * 3600
    if os.path.exists(cached) and (fresh or not online()):
        return json.load(open(cached)), "cache"
    if online():
        try:
            r = _s.get(f"{config.PSI}/repo/investigations/{acc}", timeout=90)
            r.raise_for_status()
            cat = _slim(r.json())
            if not cat.get("accession"):
                raise ValueError("catálogo sem accession")
            os.makedirs(os.path.dirname(cached), exist_ok=True)
            with open(cached + ".part", "w") as f:
                json.dump(cat, f, ensure_ascii=False)
            os.replace(cached + ".part", cached)
            return cat, "psi"
        except Exception as e:
            print(f"  [psi] catálogo {acc} indisponível ({type(e).__name__})")
    if os.path.exists(cached):  # PSI fora do ar: fica o cache vencido
        return json.load(open(cached)), "cache"
    seed = os.path.join(config.SEED, "catalog", acc + ".json")
    if os.path.exists(seed):
        return json.load(open(seed)), "semente"
    return None, "ausente"


def cached_ok(path, size=None):
    """O arquivo está em cache e, se o catálogo informa o tamanho, é a mesma versão do PSI."""
    if not os.path.exists(path) or os.path.getsize(path) == 0:
        return False
    return not size or os.path.getsize(path) == size


def download(acc, file_name, dest_dir, max_mb=None, size=None):
    """Baixa um arquivo do PSI para dest_dir (cache). Devolve o caminho ou None.

    size é o tamanho que o catálogo informa: um arquivo em cache com outro tamanho foi substituído no PSI
    e é baixado de novo. Sem rede, fica a versão em cache."""
    dest = os.path.join(dest_dir, safe(file_name))
    if cached_ok(dest, size):
        return dest
    if not online():
        return dest if cached_ok(dest) else None
    url = f"{config.PSI}/studies/{acc}/download?source=datamanager&file={quote(file_name)}"
    keep = dest if cached_ok(dest) else None  # se o download falhar, a versão antiga continua valendo
    try:
        r = _s.get(url, timeout=180, allow_redirects=True)
    except requests.RequestException:
        return keep
    if r.status_code != 200 or r.headers.get("Content-Type", "").startswith("text/html"):
        return keep
    if max_mb and len(r.content) > max_mb * 1e6:
        return keep
    os.makedirs(dest_dir, exist_ok=True)
    with open(dest + ".part", "wb") as f:
        f.write(r.content)
    os.replace(dest + ".part", dest)
    return dest


def table_path(acc, file_name, size=None):
    """Tabela experimental: cache, depois PSI, depois semente."""
    p = download(acc, file_name, os.path.join(config.CACHE, "tables"), size=size)
    if p:
        return p, "psi/cache"
    seed = os.path.join(config.SEED, "tables", file_name)
    return (seed, "semente") if os.path.exists(seed) else (None, "ausente")
