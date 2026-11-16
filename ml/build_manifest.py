"""Gera ml/manifest.json: a lista do que baixar para treinar modelos.

Só biblioteca padrão. Lê os catálogos de arquivos do NASA PSI (os
`catalogo.json` que o baixar.py do time já salvou, ou direto da API com
--live) e os registros do Zenodo, e escolhe só o que cabe no orçamento de
disco: séries por ensaio, tabelas e vídeos por teste. Os zips brutos da NASA
(140 GB a 2 TB por investigação) nunca entram.

Uso:
  python3 ml/build_manifest.py                      # catálogos locais
  python3 ml/build_manifest.py --live               # catálogos direto do PSI
  python3 ml/build_manifest.py --catalogs /caminho  # outra pasta de catálogos
"""
import glob
import json
import os
import re
import sys
from urllib.parse import quote

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from net import baixar_json  # noqa: E402

AQUI = os.path.dirname(os.path.abspath(__file__))
PSI = "https://psi.nasa.gov/geode-py/ws"
ZENODO = "https://zenodo.org/api/records"

# Grupos em ordem de prioridade: o downloader segue esta ordem e para no
# orçamento. `pega(nome)` decide se o arquivo entra.
GRUPOS_PSI = [
    dict(grupo="spice_tabelas", prioridade=2, psi="PSI-107", modalidade="tabela",
         nota="planilhas de voo do SPICE, inclui comprimento de chama",
         pega=lambda n: "_Raw Data_" in n and n.lower().endswith((".xls", ".xlsx"))),
    dict(grupo="cfig_series", prioridade=3, psi="PSI-159", modalidade="serie_temporal",
         nota="um _data.csv e um _mfcSetpoints.csv por chama do CFI-G",
         pega=lambda n: "_Raw Data_" in n and n.lower().endswith(".csv")),
    dict(grupo="flamedesign_series", prioridade=7, psi="PSI-10", modalidade="serie_temporal",
         nota="séries por chama do ACME Flame Design",
         pega=lambda n: "_Raw Data_" in n and n.lower().endswith((".csv", ".asc"))),
    dict(grupo="sflame_series", prioridade=8, psi="PSI-23", modalidade="serie_temporal",
         nota="séries por chama do ACME s-Flame",
         pega=lambda n: "_Raw Data_" in n and n.lower().endswith((".csv", ".asc"))),
    dict(grupo="bassii_video", prioridade=9, psi="PSI-25", modalidade="video",
         nota="um vídeo por teste do BASS-II (sem as sequências de fotos duplicadas)",
         pega=lambda n: n.lower().endswith(".mp4") and "Still_Sequence" not in n),
    # ---- segunda leva (+10 GB), para máquina com 50 GB livres
    dict(grupo="tabelas_extra", prioridade=10, psi="PSI-26", modalidade="tabela",
         nota="matrizes de teste do BASS, tabelas do Saffire, curvas de gota e resumos por ensaio do CFI",
         pega=lambda n: n.lower().endswith((".xlsx", ".xls"))),
    dict(grupo="tabelas_extra", prioridade=10, psi="PSI-39", modalidade="tabela",
         pega=lambda n: "_Analyzed Data_" in n and n.lower().endswith(".pdf")),
    dict(grupo="tabelas_extra", prioridade=10, psi="PSI-117", modalidade="tabela",
         pega=lambda n: n.lower().endswith((".csv", ".xlsx"))),
    dict(grupo="tabelas_extra", prioridade=10, psi="PSI-142", modalidade="tabela",
         pega=lambda n: n.lower().endswith((".csv", ".xlsx"))),
    dict(grupo="tabelas_extra", prioridade=10, psi="PSI-98", modalidade="tabela",
         pega=lambda n: n.lower().endswith((".csv", ".xlsx"))),
    dict(grupo="tabelas_extra", prioridade=10, psi="PSI-99", modalidade="tabela",
         pega=lambda n: n.lower().endswith((".csv", ".xlsx"))),
    dict(grupo="tabelas_extra", prioridade=10, psi="PSI-100", modalidade="tabela",
         pega=lambda n: n.lower().endswith((".csv", ".xlsx"))),
    dict(grupo="bassii_extra", prioridade=11, psi="PSI-25", modalidade="video",
         nota="segunda vista dos testes do BASS-II: sequências de fotos e folhas de quadros",
         pega=lambda n: "Still_Sequence" in n or "ProofSheet" in n
         or n.lower().endswith((".xlsx", ".xls"))),
    dict(grupo="spice_video", prioridade=12, psi="PSI-107", modalidade="video",
         nota="vídeos das chamas do SPICE",
         pega=lambda n: n.lower().endswith(".mp4")),
    dict(grupo="saffire1_imagens", prioridade=13, psi="PSI-98", modalidade="imagens",
         nota="Saffire-I: fotos corrigidas da chama em amostra grande (um único experimento)",
         pega=lambda n: n.lower().endswith(".zip") and "mapped" in n.lower()
         and "jp" in n.lower() and "raw2tiff" not in n.lower() and "grid" not in n.lower()),
    dict(grupo="bass_video", prioridade=14, psi="PSI-26", modalidade="video",
         nota=("vídeos do BASS, sessões inteiras em ordem de data até o limite; "
               "as sessões seguintes (cerca de 16 GB) ficam de fora"),
         pega=lambda n: n.lower().endswith(".mp4"),
         ordem=lambda a: (a.get("subcategory") or "").split("/")[-1].strip(), limite_gb=6.7),
]

GRUPOS_ZENODO = [
    dict(grupo="zarm_meta", prioridade=1, registro="18479713", modalidade="tabela",
         nota="índice, formas das amostras e tabelas das figuras (barreiras de metal)",
         pega=lambda n: n.lower().endswith(".csv")),
    dict(grupo="zarm_meta", prioridade=1, registro="14959942", modalidade="tabela",
         nota="índice dos ensaios com vãos de ar",
         pega=lambda n: n.lower().endswith(".csv")),
    dict(grupo="zarm_barreiras", prioridade=4, registro="18479713", modalidade="termico_2d",
         nota="infravermelho, PMMA com barreiras de metal",
         pega=lambda n: n.lower().endswith(".mat")),
    dict(grupo="zarm_vaos_ar", prioridade=5, registro="14959942", modalidade="termico_2d",
         nota="infravermelho, PMMA com vãos de ar e fluxo de 10 a 20 cm/s",
         pega=lambda n: n.lower().endswith(".hdf5")),
    dict(grupo="fds_surrogate", prioridade=6, registro="21314513", modalidade="simulacao",
         nota="500 simulações FDS de propagação de chama (SIMULAÇÃO, não medição)",
         pega=lambda n: True),
]


def catalogo_psi(acc, pasta, live):
    if not live:
        achados = glob.glob(os.path.join(pasta, acc + "_*", "catalogo.json"))
        if achados:
            return json.load(open(achados[0], encoding="utf-8"))
        print(f"  {acc}: catálogo local não encontrado, buscando no PSI")
    return baixar_json(f"{PSI}/repo/investigations/{acc}")


def id_do_teste(psi, nome):
    """Extrai o identificador do ensaio do nome do arquivo, quando existe."""
    if psi in ("PSI-159", "PSI-10", "PSI-23"):
        m = re.search(r"_Raw Data_(\d{5}[A-Z]\d+)_", nome)
        return m.group(1) if m else ""
    if psi == "PSI-25":
        m = (re.search(r"_TEST_?([A-Z]\d+[a-z]?)", nome, re.I)
             or re.search(r"_TP([A-Z]\d+[a-z]?)_ProofSheet", nome))
        return m.group(1).upper() if m else ""
    return ""


def itens_psi(g, pasta, live):
    cat = catalogo_psi(g["psi"], pasta, live)
    licenca = "CC0-1.0"
    arquivos = list(cat.get("experimentalTable") or []) + list(cat.get("added") or [])
    vistos, saida, soma = set(), [], 0
    if g.get("ordem"):
        arquivos.sort(key=g["ordem"])
    sessao_cortada = None
    for a in arquivos:
        nome = a.get("file_name", "")
        if g.get("limite_gb") and g["pega"](nome) and nome not in vistos:
            # Corta em sessão inteira: não escolhe vídeo por tamanho, para não enviesar.
            sessao = g["ordem"](a)
            if sessao_cortada is None and soma + float(a.get("file_size") or 0) > g["limite_gb"] * 1e9:
                sessao_cortada = sessao
                saida = [i for i in saida if i["sessao"] != sessao]
            if sessao_cortada is not None:
                continue
            soma += float(a.get("file_size") or 0)
        if not nome or nome in vistos or not g["pega"](nome):
            continue
        if a.get("restricted") or a.get("visible") is False:
            continue
        vistos.add(nome)
        saida.append(dict(
            grupo=g["grupo"], prioridade=g["prioridade"], modalidade=g["modalidade"],
            fonte=g["psi"], nome=nome, bytes=int(float(a.get("file_size") or 0)),
            md5="", licenca=licenca, teste=id_do_teste(g["psi"], nome),
            pasta_psi=(a.get("subcategory") or "").strip(),
            sessao=g["ordem"](a) if g.get("ordem") else "",
            url=f"{PSI}/studies/{g['psi']}/download?source=datamanager&file={quote(nome)}",
            destino=f"{g['grupo']}/{nome}",
        ))
    return saida


def itens_zenodo(g, cache):
    rec = cache.get(g["registro"]) or baixar_json(f"{ZENODO}/{g['registro']}")
    cache[g["registro"]] = rec
    licenca = ((rec.get("metadata") or {}).get("license") or {}).get("id", "")
    saida = []
    for f in rec.get("files", []):
        nome = f["key"]
        if not g["pega"](nome):
            continue
        m = re.match(r"(?:exp)?(\d+)\.(?:mat|hdf5)$", nome)
        saida.append(dict(
            grupo=g["grupo"], prioridade=g["prioridade"], modalidade=g["modalidade"],
            fonte=f"zenodo:{g['registro']}", nome=nome, bytes=int(f["size"]),
            md5=(f.get("checksum") or "").replace("md5:", ""), licenca=licenca,
            teste=m.group(1) if m else "", pasta_psi="",
            url=f"{ZENODO}/{g['registro']}/files/{quote(nome)}/content",
            destino=f"{g['grupo']}/{g['registro']}/{nome}",
        ))
    return saida


def main(args):
    live = "--live" in args
    pasta = os.path.join(AQUI, "..", "..", "cflame-in-freefall", "dados")
    if "--catalogs" in args:
        pasta = args[args.index("--catalogs") + 1]

    itens, notas, cache = [], {}, {}
    for g in GRUPOS_ZENODO:
        itens += itens_zenodo(g, cache)
        notas.setdefault(g["grupo"], g["nota"])
    for g in GRUPOS_PSI:
        itens += itens_psi(g, pasta, live)
        if g.get("nota"):
            notas.setdefault(g["grupo"], g["nota"])
    itens.sort(key=lambda i: (i["prioridade"], i["grupo"], i["bytes"]))

    grupos, total = [], 0
    for nome in dict.fromkeys(i["grupo"] for i in itens):
        do_grupo = [i for i in itens if i["grupo"] == nome]
        tam = sum(i["bytes"] for i in do_grupo)
        total += tam
        grupos.append(dict(
            grupo=nome, prioridade=do_grupo[0]["prioridade"],
            modalidade=do_grupo[0]["modalidade"], arquivos=len(do_grupo),
            gb=round(tam / 1e9, 2), acumulado_gb=round(total / 1e9, 2),
            licencas=sorted({i["licenca"] for i in do_grupo}), nota=notas[nome],
        ))

    manifesto = dict(
        formato="atlas.ml.manifest/1",
        total_gb=round(total / 1e9, 2),
        aviso=("Tamanhos vêm dos catálogos; os arquivos não foram abertos ao gerar "
               "esta lista. ZARM é CC-BY-NC-4.0: uso não comercial, com atribuição."),
        grupos=grupos,
        arquivos=itens,
    )
    destino = os.path.join(AQUI, "manifest.json")
    json.dump(manifesto, open(destino, "w", encoding="utf-8"), indent=1, ensure_ascii=False)

    print(f"{'grupo':22s} {'arq':>5s} {'GB':>7s} {'acum.':>7s}  modalidade")
    for g in grupos:
        print(f"{g['grupo']:22s} {g['arquivos']:5d} {g['gb']:7.2f} {g['acumulado_gb']:7.2f}  {g['modalidade']}")
    print(f"\n{len(itens)} arquivos, {total / 1e9:.2f} GB -> {os.path.relpath(destino)}")


if __name__ == "__main__":
    main(sys.argv[1:])
