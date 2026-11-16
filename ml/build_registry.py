"""Gera o registro único de ensaios para ML: uma linha por teste.

Só biblioteca padrão. Junta, com as mesmas colunas e unidades:
  - ZARM (dois registros Zenodo): PMMA fino em torre de queda
  - BASS-II (PSI-25): sólidos na ISS, com os desfechos já extraídos do relatório
  - FLEX (PSI-69): gotas
  - CFI-G e Flame Design (PSI-159, PSI-10): chamas esféricas de gás
  - SPICE (PSI-107) e CFI (PSI-39): só condições, sem desfecho na tabela

Saídas em ml/:
  registry.csv           uma linha por ensaio
  registry_barriers.csv  uma linha por barreira de metal do ZARM
  registry_summary.json  contagens para conferência

Regras:
  - nada é inventado: campo que a fonte não traz fica vazio;
  - `outcome` vazio significa "a tabela não diz", não "queimou";
  - `split_group` é o que deve ficar junto na divisão treino/teste;
  - `files` aponta para os caminhos de ml/manifest.json (relativos ao --dest).

Uso:
  python3 ml/build_registry.py
"""
import csv
import io
import json
import os
import re
import sys

AQUI = os.path.dirname(os.path.abspath(__file__))
RAIZ = os.path.dirname(AQUI)
sys.path.insert(0, AQUI)
from net import baixar_texto  # noqa: E402

TABELAS = os.path.join(RAIZ, "data", "seed", "tables")
FONTES = os.path.join(AQUI, "sources", "zarm")
ZENODO = "https://zenodo.org/api/records"

COLUNAS = [
    "trial_id", "source", "investigation", "test_id", "family", "fuel", "form",
    "thickness_mm", "o2_pct", "pressure_kpa", "flow_cm_s", "flow_dir", "gravity_g",
    "co2_pct", "he_pct", "d0_mm", "interruption", "interruption_material",
    "ignited", "outcome", "outcome_raw", "spread_rate_mm_s", "burn_rate_mm2_s",
    "ext_diam_mm", "burn_time_s", "peak_temp_c", "label_source", "split_group",
    "same_run_as", "simulated", "modality", "files", "license", "provenance", "notes",
]


def num(v):
    """Número ou None; aceita vírgula de milhar e espaços."""
    if v is None:
        return None
    try:
        return float(str(v).replace(",", "").strip())
    except ValueError:
        return None


def arred(v, casas=3):
    return "" if v is None else round(v, casas)


def ler_tabela(nome):
    caminho = os.path.join(TABELAS, nome)
    with open(caminho, encoding="utf-8-sig", errors="replace", newline="") as f:
        return [{(k or "").strip(): (v or "").strip() for k, v in linha.items()}
                for linha in csv.DictReader(f)]


def fonte_zarm(registro, arquivo):
    """Tabelas pequenas do ZARM: baixa uma vez e guarda em ml/sources/zarm."""
    caminho = os.path.join(FONTES, registro, arquivo)
    if not os.path.exists(caminho):
        os.makedirs(os.path.dirname(caminho), exist_ok=True)
        texto = baixar_texto(f"{ZENODO}/{registro}/files/{arquivo}/content")
        open(caminho, "w", encoding="utf-8").write(texto)
    texto = open(caminho, encoding="utf-8").read()
    return [{k.strip(): (v or "").strip() for k, v in linha.items()}
            for linha in csv.DictReader(io.StringIO(texto))]


def linha(**campos):
    base = dict.fromkeys(COLUNAS, "")
    base["simulated"] = "no"
    base.update({k: ("" if v is None else v) for k, v in campos.items()})
    return base


# ---------------------------------------------------------------- arquivos

def indice_arquivos():
    """(fonte, id do teste) -> caminhos do manifesto."""
    caminho = os.path.join(AQUI, "manifest.json")
    if not os.path.exists(caminho):
        return {}
    indice = {}
    for a in json.load(open(caminho, encoding="utf-8"))["arquivos"]:
        if a["teste"]:
            indice.setdefault((a["fonte"], a["teste"]), []).append(a["destino"])
    return indice


def arquivos(indice, fonte, teste):
    return ";".join(sorted(indice.get((fonte, teste), [])))


# -------------------------------------------------------------------- ZARM

def zarm_barreiras(indice):
    """Zenodo 18479713: barreiras de metal, fluxo fixo perto de 10 cm/s."""
    reg, lic = "18479713", "CC-BY-NC-4.0"
    ensaios = fonte_zarm(reg, "index.csv")
    chave = lambda v: round(float(v), 9)  # O2 medido identifica o ensaio nas figuras
    taxa_fig = {chave(r["xO2 [-]"]): num(r["vf [mm/s]"]) for r in fonte_zarm(reg, "Figure_07.csv")}
    por_o2, barreiras = {}, []
    for r in fonte_zarm(reg, "Figure_09.csv"):
        por_o2.setdefault(chave(r["x_O2 [mol/mol]"]), []).append(r)

    linhas = []
    for e in ensaios:
        eid, o2 = e["Experiment [ID]"], float(e["Oxygen [mole/mole]"])
        vaos, material = e["Gaps [Sequence]"], e["Filler [Material]"]
        taxa, fonte_rotulo = num(e["Propagation Rate [mm/s]"]), "index.csv"
        nota = ""
        if taxa is None and chave(o2) in taxa_fig:
            taxa, fonte_rotulo = taxa_fig[chave(o2)], "Figure_07.csv"
            nota = "taxa da Figura 7; o significado para amostra com barreira não foi conferido"
        do_ensaio = por_o2.get(chave(o2), []) if vaos else []
        estados = [b["Extinction [Yes/No/Critical]"] for b in do_ensaio]
        if not vaos:
            desfecho, bruto = ("burned", "amostra contínua") if taxa is not None else ("", "")
        elif not estados:
            desfecho, bruto = "", ""
        elif "Yes" in estados:
            desfecho, bruto = "extinguished", "apagou em uma barreira"
        else:
            desfecho, bruto = "burned", "atravessou todas as barreiras medidas"
        tid = f"zarm{reg}:{eid}"
        linhas.append(linha(
            trial_id=tid, source=f"zenodo:{reg}", investigation="ZARM barreiras de metal",
            test_id=eid, family="solid", fuel="PMMA", form="filme fino",
            thickness_mm=num(e["Thickness [mm]"]), o2_pct=arred(o2 * 100, 2),
            pressure_kpa=arred(num(e["Pressure [kPa]"]), 2),
            flow_cm_s=arred(num(e["Flow Velocity [mm/s]"]) / 10, 2),
            flow_dir=e["Flow Direction [opposed/concurrent]"], gravity_g=0,
            interruption=vaos, interruption_material=material, ignited="yes",
            outcome=desfecho, outcome_raw=bruto, spread_rate_mm_s=arred(taxa),
            label_source=fonte_rotulo if (taxa is not None or desfecho) else "",
            split_group=tid, modality="termico_2d",
            files=arquivos(indice, f"zenodo:{reg}", eid), license=lic,
            provenance=f"index.csv, ensaio {eid}, {e['Date [yyyy-mm-dd]']}", notes=nota,
            _data=e["Date [yyyy-mm-dd]"],
        ))
        for b in do_ensaio:
            barreiras.append(dict(
                trial_id=tid, test_id=eid, o2_pct=arred(o2 * 100, 2),
                pressure_kpa=arred(num(b["p [kPa]"]), 2), flow_cm_s=arred(num(b["v_g [mm/s]"]) / 10, 2),
                material=material, gap_mm=arred(num(b["g [mm]"])),
                gap_eff_mm=arred(num(b["g_eff [mm]"])), extinction=b["Extinction [Yes/No/Critical]"],
                provenance="Figure_09.csv", notes="",
            ))
    # Linhas da Figura 9 sem ensaio com barreira correspondente: são os valores
    # "Critical", um por atmosfera, com O2 arredondado. Não são vãos ensaiados.
    com_barreira = {chave(float(e["Oxygen [mole/mole]"])) for e in ensaios if e["Gaps [Sequence]"]}
    for o2, do_o2 in por_o2.items():
        if o2 in com_barreira:
            continue
        for b in do_o2:
            barreiras.append(dict(
                trial_id="", test_id="", o2_pct=arred(o2 * 100, 2),
                pressure_kpa=arred(num(b["p [kPa]"]), 2), flow_cm_s=arred(num(b["v_g [mm/s]"]), 2),
                material="", gap_mm=arred(num(b["g [mm]"])), gap_eff_mm=arred(num(b["g_eff [mm]"])),
                extinction=b["Extinction [Yes/No/Critical]"], provenance="Figure_09.csv",
                notes=("sem ensaio correspondente: parece o vão crítico estimado pelos autores "
                       "para esta atmosfera, não uma barreira ensaiada; não usar como medição"),
            ))
    return linhas, barreiras


def zarm_vaos_ar(indice):
    """Zenodo 14959942: vãos de ar, fluxo de 10 a 20 cm/s. Sem rótulos prontos."""
    reg, lic = "14959942", "CC-BY-NC-4.0"
    linhas = []
    for e in fonte_zarm(reg, "index.csv"):
        eid = re.search(r"(\d+)\.", e["Data Location [File]"]).group(1)
        tid = f"zarm{reg}:{eid}"
        linhas.append(linha(
            trial_id=tid, source=f"zenodo:{reg}", investigation="ZARM vãos de ar",
            test_id=eid, family="solid", fuel=e["Sample Material [Material]"], form="filme fino",
            thickness_mm=num(e["Thickness [mm]"]), o2_pct=num(e["Oxygen [%]"]),
            pressure_kpa=arred(num(e["Pressure [bar]"]) * 100, 2),
            flow_cm_s=num(e["Flow Velocity [cm/s]"]),
            flow_dir=e["Flow Direction [opposed/concurrent]"], gravity_g=0,
            interruption=e["Gaps [Sequence]"], interruption_material=e["Gap Material [Material]"],
            ignited="yes", split_group=tid, modality="termico_2d",
            files=arquivos(indice, f"zenodo:{reg}", eid), license=lic,
            provenance=f"index.csv, {e['Data Location [File]']}, {e['Date']}",
            notes="condições nominais; taxa de propagação e travessia dos vãos ainda por extrair do infravermelho",
            _data=e["Date"],
        ))
    return linhas


def marcar_duplicatas_zarm(a, b):
    """Mesmo dia, O2 a menos de 1 ponto e fluxo a menos de 20%: provável mesmo ensaio
    publicado nos dois registros (um com valores medidos, outro com nominais)."""
    pares = 0
    for x in a:
        if x["interruption"]:
            continue
        candidatos = [y for y in b if not y["interruption"] and y["_data"] == x["_data"]
                      and abs(y["o2_pct"] - x["o2_pct"]) < 1.0
                      and abs(y["flow_cm_s"] - x["flow_cm_s"]) <= 0.2 * y["flow_cm_s"]]
        if len(candidatos) == 1:
            y = candidatos[0]
            x["same_run_as"], y["same_run_as"] = y["trial_id"], x["trial_id"]
            y["split_group"] = x["split_group"]
            aviso = "provável mesmo ensaio físico nos dois registros ZARM (mesma data e condições)"
            for z in (x, y):
                z["notes"] = "; ".join(t for t in (z["notes"], aviso) if t)
            pares += 1
    return pares


# ------------------------------------------------------------------ BASS-II

FIM_BASSII = {  # desfecho do relatório -> rótulo comum
    "burned": "burned", "burnout": "burned", "fan_off": "burned",
    "quench": "burned", "blowoff": "burned",        # a chama se manteve na condição inicial
    "self": "extinguished", "not_sustained": "extinguished",
    "not_ignited": "not_ignited", "unknown": "",
}
DIRECAO = {"contra o vento": "opposed", "a favor do vento": "concurrent"}


def bassii(indice):
    caminho = os.path.join(RAIZ, "data", "seed", "outcomes", "PSI-25.json")
    doc = json.load(open(caminho, encoding="utf-8"))
    linhas = []
    for t in doc["tests"]:
        pontos = t.get("points") or []
        fluxo = pontos[0].get("flow_cm_s") if pontos else None
        fim = t.get("end") or ""
        nota = "pressão assumida: cabine da ISS, cerca de 1 atm"
        if fim in ("quench", "blowoff"):
            nota += "; teste de limite: queimou na partida e apagou quando o ar foi alterado (ver outcome_raw)"
        linhas.append(linha(
            trial_id=f"PSI-25:{t['test']}", source="PSI-25", investigation="BASS-II",
            test_id=t["test"], family="solid", fuel=t.get("material"), form=t.get("form"),
            o2_pct=t.get("o2_initial"), pressure_kpa=101.3, flow_cm_s=fluxo,
            flow_dir=DIRECAO.get(t.get("geometry") or "", ""), gravity_g=0,
            ignited={True: "yes", False: "no"}.get(t.get("ignited"), ""),
            outcome=FIM_BASSII.get(fim, ""), outcome_raw=fim,
            label_source="relatório NASA/TM-20210011385, Apêndice A" if t.get("found") else "",
            split_group=f"PSI-25:amostra-{t.get('sample')}", modality="video",
            files=arquivos(indice, "PSI-25", t["test"].upper()), license="CC0-1.0",
            provenance=f"tabela PSI linha {t.get('table_row')}; relatório p. {t.get('report_page')}",
            notes=nota,
        ))
    return linhas


# --------------------------------------------------------------------- FLEX

FIM_FLEX = {"Extinction": "extinguished", "Completion": "burned", "Disruption": "disrupted"}


def flex():
    linhas = []
    for r in ler_tabela("PSI-69_Experimental table_FLEX.csv"):
        fim = r["Test end"]
        p = num(r["Ambient pressure; mmHg"])
        fr = lambda nome: num(r[nome])
        linhas.append(linha(
            trial_id=f"PSI-69:{r['FLEX Test #']}", source="PSI-69", investigation="FLEX",
            test_id=r["FLEX Identifier"], family="droplet", fuel=r["Fuel"], form="gota",
            o2_pct=arred(fr("O initial ambient composition; mole fraction") * 100, 2),
            pressure_kpa=arred(p * 0.133322, 2) if p else "",
            gravity_g=0,
            co2_pct=arred((fr("CO initial ambient composition; mole fraction") or 0) * 100, 2),
            he_pct=arred((fr("He initial ambient composition; mole fraction") or 0) * 100, 2),
            d0_mm=fr("Droplet initial diameter; mm"), ignited="yes",
            outcome=FIM_FLEX.get(fim, ""), outcome_raw=fim,
            burn_rate_mm2_s=fr("Burning rate; mm"),
            ext_diam_mm=fr("Visible flame extinction diameter; mm"),
            burn_time_s=fr("Burn time; s"), label_source="tabela PSI",
            split_group=f"PSI-69:{r['Test Date']}", modality="tabela", license="CC0-1.0",
            provenance=f"tabela PSI, teste {r['FLEX Test #']}",
            notes="a coluna 'CO' da tabela é CO2 no relatório NASA/TP-2015-216046",
        ))
    return linhas


# ----------------------------------------------------------- ACME (gás)

def desfecho_cfig(ignicao, termino):
    ig, t = ignicao.strip().lower(), termino.strip().upper()
    if ig in ("no",):
        return "no", "not_ignited"
    if ig == "flash":
        return "no", "not_ignited"
    if t == "SE":
        return "yes", "extinguished"
    if t == "FT":
        return "yes", "sustained_until_stop"   # a chama ainda existia quando o fluxo foi cortado
    return ("yes" if ig == "yes" else ""), ""


def cfig(indice):
    linhas = []
    tabelas = [
        ("PSI-159_Experimental table_CFI-G_Normal Flames.csv", "normal", "XO2 before test",
         "Self-ext or flow term", "Peak burner T (C )"),
        ("PSI-159_Experimental table_CFI-G_Inverse Flames.csv", "inversa", "XO2",
         "Self -ext or flow term", "Peak burner Temp (C)"),
    ]
    for arquivo, tipo, col_o2, col_fim, col_t in tabelas:
        for r in ler_tabela(arquivo):
            if not r.get("Test"):
                continue                       # a tabela inversa tem centenas de linhas vazias
            teste = r["Test"].replace(" ", "")
            ig, desfecho = desfecho_cfig(r["Hot ignition"], r[col_fim])
            p = num(r["approx p (bar)"])
            o2 = num(r[col_o2])
            linhas.append(linha(
                trial_id=f"PSI-159:{teste}", source="PSI-159", investigation="ACME CFI-G",
                test_id=teste, family="gas", fuel=r["Fuel"], form=f"chama esférica {tipo}",
                o2_pct=arred(o2 * 100, 2) if o2 is not None else "",
                pressure_kpa=arred(p * 100, 2) if p is not None else "", gravity_g=0,
                ignited=ig, outcome=desfecho,
                outcome_raw=f"ignição quente: {r['Hot ignition']}; término: {r[col_fim]}",
                burn_time_s=num(r["burn time (sec)"]), peak_temp_c=arred(num(r[col_t]), 1),
                label_source="tabela PSI", split_group=f"PSI-159:{tipo}-dia-{r['Day']}",
                modality="serie_temporal", files=arquivos(indice, "PSI-159", teste),
                license="CC0-1.0", provenance=f"{arquivo}, teste {r['Test']}",
                notes="SE = autoextinção; FT = fluxo cortado com a chama acesa",
            ))
    return linhas


def flame_design(indice):
    linhas = []
    tabelas = [
        ("PSI-10_Experimental table_Flame Design_Normal Flame.csv", "normal", "XO2 (init)", "pinit (atm)"),
        ("PSI-10_Experimental table_Flame Design_Inverse Flame.csv", "inversa", "XO2", "pinit (atm)"),
    ]
    for arquivo, tipo, col_o2, col_p in tabelas:
        for r in ler_tabela(arquivo):
            if not r.get("Test"):
                continue
            teste = r["Test"].replace(" ", "")
            o2, p = num(r[col_o2]), num(r[col_p])
            linhas.append(linha(
                trial_id=f"PSI-10:{teste}", source="PSI-10", investigation="ACME Flame Design",
                test_id=teste, family="gas", fuel=r.get("Fuel") or "ethylene",
                form=f"chama esférica {tipo}",
                o2_pct=arred(o2 * 100, 2) if o2 is not None else "",
                pressure_kpa=arred(p * 101.325, 2) if p is not None else "", gravity_g=0,
                split_group=f"PSI-10:{tipo}-dia-{r['Day']}", modality="serie_temporal",
                files=arquivos(indice, "PSI-10", teste), license="CC0-1.0",
                provenance=f"{arquivo}, teste {r['Test']}",
                notes="a tabela só traz condições; o desfecho teria de sair das séries por ensaio",
            ))
    return linhas


# --------------------------------------------------- só condições (sem rótulo)

def spice():
    linhas = []
    for n, r in enumerate(ler_tabela("PSI-107_Experimental table_Spice.csv"), 2):
        linhas.append(linha(
            trial_id=f"PSI-107:linha-{n}", source="PSI-107", investigation="SPICE",
            test_id=f"{r['GMT date']} {r['GMT ignition']}".strip(), family="gas",
            fuel=r["Fuel"], form=f"jato com co-fluxo, bocal {r['Nozzle (mm)']} mm",
            o2_pct=21, pressure_kpa=101.3, flow_cm_s=num(r["Air Rate (cm/s)"]),
            flow_dir="concurrent", gravity_g=0, ignited="yes",
            split_group=f"PSI-107:dia-{r['GMT date']}", modality="tabela", license="CC0-1.0",
            provenance=f"tabela PSI linha {n}",
            notes=("ar da cabine assumido (21% O2, 1 atm); vazão de combustível "
                   f"{r['Fuel Rate (mg/s)']} mg/s; comprimento de chama está em "
                   "Spice_Flight_Flamelength.xls, ainda não ligado a esta linha"),
        ))
    return linhas


def cfi():
    linhas, vistos = [], set()
    for r in ler_tabela("PSI-39_Experimental table_CFI.csv"):
        if tuple(r.values()) in vistos:
            continue                           # a tabela da NASA repete 8 linhas idênticas
        vistos.add(tuple(r.values()))
        m = re.search(r"([\d.]+)\s*atm", r["Pressure"], re.I)
        o2 = num(r["Oxygen"])
        if o2 is not None and o2 <= 1:
            o2 *= 100
        he = num(r["Helium"])
        if he is not None and he <= 1:
            he *= 100
        linhas.append(linha(
            trial_id=f"PSI-39:{r['Flex Number']}", source="PSI-39", investigation="CFI",
            test_id=f"FLEX-{r['Flex Number']}", family="droplet", fuel=r["Fuel"],
            form="gota em fibra" if r["Fiber"] else "gota",
            o2_pct=arred(o2, 2), pressure_kpa=arred(float(m.group(1)) * 101.325, 2) if m else "",
            gravity_g=0, he_pct=arred(he, 2), split_group=f"PSI-39:{r['Test Date']}",
            modality="tabela", license="CC0-1.0", provenance=f"tabela PSI, FLEX-{r['Flex Number']}",
            notes=f"a tabela só traz condições; pressão original: '{r['Pressure']}'",
        ))
    return linhas


# --------------------------------------------------------------------- main

def contar(linhas, campo):
    saida = {}
    for r in linhas:
        saida[r[campo]] = saida.get(r[campo], 0) + 1
    return dict(sorted(saida.items(), key=lambda kv: -kv[1]))


def main():
    indice = indice_arquivos()
    za, barreiras = zarm_barreiras(indice)
    zb = zarm_vaos_ar(indice)
    pares = marcar_duplicatas_zarm(za, zb)
    linhas = za + zb + bassii(indice) + flex() + cfig(indice) + flame_design(indice) + spice() + cfi()

    ids = [r["trial_id"] for r in linhas]
    repetidos = sorted({i for i in ids if ids.count(i) > 1})
    if repetidos:
        sys.exit(f"trial_id repetido: {repetidos[:10]}")

    with open(os.path.join(AQUI, "registry.csv"), "w", encoding="utf-8", newline="") as f:
        w = csv.DictWriter(f, fieldnames=COLUNAS, extrasaction="ignore")
        w.writeheader()
        w.writerows(linhas)
    with open(os.path.join(AQUI, "registry_barriers.csv"), "w", encoding="utf-8", newline="") as f:
        w = csv.DictWriter(f, fieldnames=list(barreiras[0].keys()))
        w.writeheader()
        w.writerows(barreiras)

    por_inv = {}
    for r in linhas:
        e = por_inv.setdefault(r["investigation"], dict(
            ensaios=0, com_desfecho=0, com_alvo_numerico=0, com_arquivos=0, grupos=set()))
        e["ensaios"] += 1
        e["com_desfecho"] += bool(r["outcome"])
        e["com_alvo_numerico"] += any(r[c] != "" for c in (
            "spread_rate_mm_s", "burn_rate_mm2_s", "ext_diam_mm", "peak_temp_c"))
        e["com_arquivos"] += bool(r["files"])
        e["grupos"].add(r["split_group"])
    for e in por_inv.values():
        e["grupos_de_divisao"] = len(e.pop("grupos"))

    resumo = dict(
        formato="atlas.ml.registry/1", ensaios=len(linhas),
        com_desfecho=sum(bool(r["outcome"]) for r in linhas),
        por_investigacao=por_inv, desfechos=contar([r for r in linhas if r["outcome"]], "outcome"),
        familias=contar(linhas, "family"),
        zarm=dict(pares_duplicados=pares, barreiras=sum(bool(b["trial_id"]) for b in barreiras),
                  barreiras_por_resultado=contar(barreiras, "extinction"),
                  ensaios_unicos=len(za) + len(zb) - pares),
    )
    json.dump(resumo, open(os.path.join(AQUI, "registry_summary.json"), "w", encoding="utf-8"),
              indent=1, ensure_ascii=False)

    print(f"{'investigação':26s} {'ensaios':>7s} {'desfecho':>8s} {'alvo num.':>9s} {'arquivos':>8s} {'grupos':>6s}")
    for nome, e in por_inv.items():
        print(f"{nome:26s} {e['ensaios']:7d} {e['com_desfecho']:8d} {e['com_alvo_numerico']:9d} "
              f"{e['com_arquivos']:8d} {e['grupos_de_divisao']:6d}")
    print(f"\n{len(linhas)} ensaios, {resumo['com_desfecho']} com desfecho; "
          f"{resumo['zarm']['barreiras']} barreiras ZARM medidas; {pares} pares duplicados entre os registros ZARM")
    print("desfechos:", resumo["desfechos"])


if __name__ == "__main__":
    main()
