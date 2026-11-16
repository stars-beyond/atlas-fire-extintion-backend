"""Um adaptador por tabela experimental do PSI.

Cada linha vira um teste com as mesmas colunas (família, combustível, O₂, pressão,
gravidade, fluxo, desfecho) e guarda de onde veio: arquivo, linha e o registro bruto.
Nada é corrigido em silêncio: o que foi inferido entra em `assumed`.
"""
import csv
import io
import re

# ----------------------------------------------------------------- leitura


def read_table(path):
    with open(path, "rb") as f:
        raw = f.read()
    try:
        text = raw.decode("utf-8-sig")
    except UnicodeDecodeError:
        # o cp1252 não define 5 bytes (0x81, 0x8D, 0x8F, 0x90, 0x9D): viram "?" em vez de abortar a ingestão inteira
        text = raw.decode("cp1252", errors="replace")
    rows = list(csv.reader(io.StringIO(text)))
    if not rows:   # arquivo vazio: tabela sem linhas, não um erro
        return []
    head = [re.sub(r"\s+", " ", c).strip() for c in rows[0]]
    out = []
    for i, r in enumerate(rows[1:], start=2):  # linha 1 é o cabeçalho
        if any(x.strip() for x in r):
            out.append((i, dict(zip(head, [v.strip() for v in r]))))
    return out


def num(v):
    if v is None:
        return None
    m = re.search(r"-?\d+(?:\.\d+)?|-?\.\d+", str(v).replace(",", "."))
    return float(m.group()) if m else None


def col(row, *prefixes):
    """Valor da primeira coluna cujo nome começa com um dos prefixos (cabeçalhos do PSI variam)."""
    for p in prefixes:
        for k, v in row.items():
            if k.lower().startswith(p.lower()):
                return v
    return ""


def frac(v):
    x = num(v)
    if x is None:
        return None
    return x / 100 if x > 1 else x


def slug(s):
    return re.sub(r"[^A-Za-z0-9]+", "-", str(s)).strip("-")[:40] or "x"


def solid_material(text):
    t = text.lower()
    if "pmma" in t or "rod" in t or "sphere" in t or "acrylic" in t or "film" in t:
        return "pmma"
    if "sibal" in t or "cotton" in t:
        return "sibal"
    if "nomex" in t:
        return "nomex"
    if "silicone" in t:
        return "silicone"
    if "wax" in t or "candle" in t:
        return "cera"
    if "delrin" in t:
        return "delrin"
    return "outro"


def T(acc, file, row_n, raw, label, family, **kw):
    """Monta um teste. `assumed` lista os campos preenchidos por inferência."""
    t = {
        "id": f"{acc}:{slug(label)}:{row_n}", "investigation_id": acc, "label": str(label), "family": family,
        "fuel": None, "material": None, "o2": None, "pressure_atm": None, "gravity": 0.0, "flow_cm_s": None,
        "co2": None, "he": None, "outcome": "desconhecido", "outcome_raw": None, "metrics": {}, "assumed": [],
        "source_file": file, "source_row": row_n, "raw": raw,
    }
    t.update(kw)
    t["metrics"] = {k: v for k, v in t["metrics"].items() if v not in (None, "")}
    return t


# ----------------------------------------------------------------- gotas

def flex(acc, file, rows):
    fim = {"Extinction": "extinguiu", "Disruption": "disrupcao", "Completion": "queimou"}
    for n, r in rows:
        p = num(r.get("Ambient pressure; mmHg"))
        if not p:
            continue
        co2 = num(col(r, "CO initial")) or 0.0   # a coluna chama "CO", mas é o CO₂ diluente do FLEX
        he = num(col(r, "He initial")) or 0.0
        yield T(acc, file, n, r, r.get("FLEX Identifier") or r.get("FLEX Test #"), "gota",
                fuel=r.get("Fuel"), o2=num(col(r, "O initial")), pressure_atm=round(p / 760, 4), co2=co2, he=he,
                outcome=fim.get(r.get("Test end"), "desconhecido"), outcome_raw=r.get("Test end"),
                metrics={"d0_mm": num(col(r, "Droplet initial")), "d_ext_mm": num(col(r, "Visible flame extinction")),
                         "k_mm2_s": num(col(r, "Burning rate")), "burn_time_s": num(col(r, "Burn time")),
                         "date": r.get("Test Date"), "n2": num(col(r, "N initial"))})


def cfi(acc, file, rows):
    for n, r in rows:
        yield T(acc, file, n, r, r.get("Flex Number"), "gota", fuel=(r.get("Fuel") or "").strip(),
                o2=num(r.get("Oxygen")), pressure_atm=num(r.get("Pressure")), he=num(r.get("Helium")) or 0.0,
                metrics={"fiber": r.get("Fiber"), "date": r.get("Test Date"), "cir": r.get("CIR")})


def flex2(acc, file, rows):
    # A tabela descreve a matriz planejada (faixas), não testes individuais.
    for n, r in rows:
        yield T(acc, file, n, r, col(r, "Sample"), "gota", fuel=col(r, "Sample"), outcome_raw="test matrix",
                metrics={"o2_declared": col(r, "Oxygen Mole"), "p_declared_atm": col(r, "Ambient Pressure"),
                         "d0_mm": col(r, "Initial Droplet"), "flow_declared_cm_s": col(r, "Flow Velocity"),
                         "gas": col(r, "Ambient Gas")})


# ----------------------------------------------------------------- chamas gasosas

def _ignition(r):
    ig = (r.get("Hot ignition") or "").strip().lower()
    term = (col(r, "Self-ext", "Self -ext") or "").strip().upper()
    if ig == "yes":
        return ("extinguiu" if term == "SE" else "queimou"), f"ignition: yes; end: {term or '-'}"
    if ig == "no":
        return "nao_acendeu", "ignition: no"
    if ig == "flash":
        return "nao_acendeu", "flash (momentary ignition)"
    return "desconhecido", ig or None


def _pct(v):
    """Fração molar (0 a 1) como porcentagem, para as métricas."""
    x = num(v)
    return round(x * 100, 3) if x is not None and 0 <= x <= 1 else None


def cfig(acc, file, rows):
    inverse = "Inverse" in file
    for n, r in rows:
        p = num(r.get("approx p (bar)"))
        out, raw_out = _ignition(r)
        # Chama inversa: o O₂ sai do queimador para uma câmara de combustível + N₂. "XO2" é a fração de O₂ do
        # gás do queimador, não a atmosfera; por isso não entra em `o2` (fica em metrics.o2_burner_pct).
        yield T(acc, file, n, r, r.get("Test"), "gas", fuel=r.get("Fuel"),
                o2=None if inverse else num(col(r, "XO2 before")),
                pressure_atm=round(p / 1.01325, 4) if p else None, outcome=out, outcome_raw=raw_out,
                metrics={"flame": "inverse" if inverse else "normal", "o2_burner_pct": _pct(r.get("XO2")) if inverse else None,
                         "tad_k": num(r.get("Tad (K)")),
                         "zst": num(r.get("Zst")), "burn_time_s": num(r.get("burn time (sec)")),
                         "cool_flame": r.get("Cool flame?"), "color": r.get("Color"), "day": r.get("Day"),
                         "comments": r.get("Comments")})


def flame_design(acc, file, rows):
    inverse = "Inverse" in file
    for n, r in rows:
        yield T(acc, file, n, r, r.get("Test"), "gas", fuel=r.get("Fuel") or "C2H4",
                # chama inversa: "XO2" é o oxidante do queimador (ver cfig); a câmara tem combustível + N₂
                o2=None if inverse else num(r.get("XO2 (init)")), pressure_atm=num(r.get("pmean (atm)")),
                metrics={"flame": "inverse" if inverse else "normal", "o2_burner_pct": _pct(r.get("XO2")) if inverse else None,
                         "tad_k": num(r.get("Tad (K)")),
                         "zst": num(r.get("mean Zst")), "day": r.get("Day")})


def bre(acc, file, rows):
    # Resumo por dia: lista de O₂ dos testes que acenderam. Uma linha por (dia, O₂); a pressão variou e não consta.
    for n, r in rows:
        day = f"{r.get('Year')}-{r.get('Month')}-{r.get('Date')}"
        for o in re.findall(r"\d+(?:\.\d+)?", r.get("Oxygen Conc (percent)") or ""):
            yield T(acc, file, n, r, f"{day} O2 {o}", "gas", fuel=r.get("Fuel Bottle"), o2=float(o) / 100,
                    outcome="queimou", outcome_raw="daily summary: flames ignited",
                    metrics={"burner_mm": num(r.get("Burner Size (mm)")), "flames_ignited_day": num(r.get("Flames Ignited"))})


def acme_summary(acc, file, rows):
    for n, r in rows:
        day = col(r, "Test Day")
        atm = col(r, "Atmosphere")
        kw = {}
        if atm and "21%" in atm:
            kw = {"o2": 0.21, "pressure_atm": 1.0, "assumed": ["pressure_atm"]}
        yield T(acc, file, n, r, day, "gas", fuel=r.get("Fuel Bottle"), outcome_raw="daily summary",
                metrics={"flames_ignited_day": num(col(r, "Flames ignited", "Flames Ignited")),
                         "test_type": r.get("Test Type"), "polarity": r.get("Mesh Polarity"),
                         "fuel_conc": col(r, "Fuel Conc", "Fuel conc")}, **kw)


def spice(acc, file, rows):
    seen = set()
    for n, r in rows:
        fuel = re.sub(r"\s*%\s*", "% ", r.get("Fuel", "")).strip()
        key = (fuel, r.get("Nozzle (mm)"), r.get("Fuel Rate (mg/s)"), r.get("Air Rate (cm/s)"))
        if key in seen:
            continue
        seen.add(key)
        air = num(r.get("Air Rate (cm/s)"))
        yield T(acc, file, n, r, f"{fuel} {r.get('Nozzle (mm)')}mm {r.get('Fuel Rate (mg/s)')}", "gas", fuel=fuel,
                flow_cm_s=air if air and air > 0 else None, outcome_raw="smoke point",
                metrics={"nozzle_mm": num(r.get("Nozzle (mm)")), "fuel_mg_s": num(r.get("Fuel Rate (mg/s)")),
                         "gmt_day": r.get("GMT date")})


def slice_(acc, file, rows):
    for n, r in rows:
        yield T(acc, file, n, r, r.get("Test Date"), "gas", fuel=r.get("Fuel Type"), outcome_raw="daily summary")


# ----------------------------------------------------------------- sólidos

def bass2(acc, file, rows):
    for n, r in rows:
        mat = r.get("Fuel Sample Material", "")
        o2f = num(col(r, "Calibrated final O2"))
        yield T(acc, file, n, r, r.get("Test #"), "solido", fuel=mat, material=solid_material(mat),
                o2=frac(col(r, "Calibrated initial O2")), pressure_atm=1.0, assumed=["pressure_atm"],
                metrics={"pi": r.get("PI"), "o2_final_pct": o2f, "co2_initial_pct": num(col(r, "Initial CO2")),
                         "co2_final_pct": num(col(r, "Final CO2")), "co_final_ppm": num(col(r, "Final CO (ppm)")),
                         "fan_display": col(r, "Fan display"), "flow_restrictor": col(r, "Flow restrictor"),
                         "date": r.get("Date"), "sample": r.get("Sample #")})


def bass(acc, file, rows):
    for n, r in rows:
        mat = r.get("Fuel Sample Material", "")
        yield T(acc, file, n, r, r.get("As Run Test #") or r.get("Test #"), "solido", fuel=mat,
                material=solid_material(mat), metrics={"date": r.get("Date"), "sample": r.get("Sample #")})


def _burn_length_outcome(text, sample_cm):
    """Desfecho a partir do comprimento queimado ("Complete", "7.6 cm", "~ 0"), no vocabulário do Atlas.
    Queima parcial ou nula é "extinguiu", como o "Insignificant" do lado de 0 g."""
    t = text.lower()
    if "(" in t:   # "~ 0 (Nomex)": a nota fala de uma parte da amostra e não diz o que houve com o resto
        return "desconhecido"
    if "complete" in t:
        return "queimou"
    x = num(t)
    if x is None:
        return "desconhecido"
    if x == 0:
        return "extinguiu"
    if not sample_cm:
        return "desconhecido"
    return "queimou" if x >= 0.95 * sample_cm else "extinguiu"


def saffire(acc, file, rows):
    for n, r in rows:
        sid = col(r, "Sample Number")
        mat = r.get("Material", "")
        if not re.match(r"^(S\d|\d-\d)", sid):
            continue  # linhas de visualização de fluxo e notas
        burn = col(r, "Burn Time")
        o2 = frac(col(r, "Percent O2"))
        out = "desconhecido"
        if burn:
            out = "extinguiu" if "insignificant" in burn.lower() else "queimou"
        direction = col(r, "Flow Direction", "Direction").strip()
        base = dict(fuel=mat or "(same material as the previous row)", material=solid_material(mat) if mat else None,
                    flow_cm_s=num(col(r, "Air Flow")))
        yield T(acc, file, n, r, sid, "solido", o2=o2, pressure_atm=1.0 if o2 else None,
                assumed=["pressure_atm"] if o2 else [], outcome=out, outcome_raw=burn or None,
                metrics={"direction": direction, "thickness": col(r, "Sample Thickness", "Samle Thickness"),
                         "burn_length_0g": col(r, "?-g Burn Length"), "ignition_w": num(col(r, "Ignition Power"))}, **base)
        g1 = col(r, "1-g Burn Length")
        if g1 and g1.lower() not in ("n/a", ""):
            yield T(acc, file, n, r, f"{sid} (1 g)", "solido", o2=0.209, pressure_atm=1.0, gravity=1.0,
                    assumed=["o2", "pressure_atm"], outcome=_burn_length_outcome(g1, num(col(r, "Sample Length"))),
                    outcome_raw=f"1 g: {g1}",
                    metrics={"direction": direction, "burn_length_1g": g1, "spread_rate_1g": col(r, "1-g Spread Rate")},
                    **base)


# ----------------------------------------------------------------- fumaça e aerossóis

def same(acc, file, rows):
    for n, r in rows:
        mat = col(r, "Sample Material")
        yield T(acc, file, n, r, f"{r.get('Sample #')} {mat}", "fumaca", fuel=mat, flow_cm_s=num(col(r, "Air Flow")),
                outcome_raw="pyrolysis (smoke)", metrics={"weight_loss_mg_min": num(col(r, "Weight loss")),
                                                         "aging_min": num(col(r, "Aging Duration"))})


def same_r(acc, file, rows):
    for n, r in rows:
        yield T(acc, file, n, r, f"{r.get('Sample Type')} {r.get('SAME Test #')}", "fumaca", fuel=r.get("Sample Type"),
                flow_cm_s=num(col(r, "Inlet velocity")), outcome_raw="pyrolysis (smoke)",
                metrics={"rt_r0": num(r.get("Rt/R0")), "duration_s": num(col(r, "Duration")),
                         "aging_s": num(col(r, "Net aging")), "description": r.get("Description")})


def daft(acc, file, rows):
    for n, r in rows:
        yield T(acc, file, n, r, r.get("Session Name"), "fumaca", fuel=col(r, "Sample Material")[:80],
                outcome_raw="dust measurement", metrics={"day": r.get("Actual Day"), "hardware": col(r, "Hardware")})


def reference_only(acc, file, rows):
    for n, r in rows:
        first = next(iter(r.values()), n)
        yield T(acc, file, n, r, first, "gas", outcome_raw="index of published data",
                metrics={"description": col(r, "Data Description"), "link": col(r, "Reference Publication")})


ADAPTERS = [
    ("FLEX.csv", flex), ("Flex2", flex2), ("_CFI.csv", cfi),
    ("CFI-G", cfig), ("Flame Design", flame_design), ("ACME BRE", bre),
    ("CLD Flame", acme_summary), ("E-FIELD", acme_summary), ("s-Flame", acme_summary),
    ("Spice", spice), ("Slice", slice_),
    ("BASS-II", bass2), ("_BASS.csv", bass), ("SAFFIRE", saffire),
    ("SAME-R", same_r), ("_SAME.csv", same), ("DAFT", daft), ("Data Summary Table", reference_only),
]


def adapter_for(file_name):
    for key, fn in ADAPTERS:
        if key in file_name:
            return fn
    return None


# ----------------------------------------------------------------- adaptador genérico (reserva)
# Para uma tabela nova do PSI sem adaptador próprio: reconhece as colunas pelo nome e converte unidades.
# Tudo o que ele lê fica marcado (metrics.leitura e assumed), e um adaptador escrito à mão continua sendo melhor.

_O2 = re.compile(r"^(xo2|x o2|percent o2|% ?o2|o2\b|oxygen\b(?!.*\bmg\b)|o2 mole|oxygen mole|oxygen conc|o initial)", re.I)
_P = re.compile(r"(pressure|^approx p\b|^p \(|^p$)", re.I)
_FLOW = re.compile(r"(air flow|flow velocity|inlet velocity|air rate|flow \(cm/s\)|velocity \(cm/s\))", re.I)
_G = re.compile(r"(gravity|g-level|g level)", re.I)
_OUT = re.compile(r"(ignited|extinct|outcome|result|burn ?out|ignition\?)", re.I)
# coluna de medida (diâmetro de extinção em mm, tempo em s...) não é desfecho, mesmo com "extinction" no nome
_MEASURE = re.compile(r"(;|\(|\b(mm|cm|mg|sec|s|ms|diameter|time|rate|temperature)\b)", re.I)
_FUEL = re.compile(r"(^fuel\b(?! rate| bottle)|material|^sample \(fuel)", re.I)
_LABEL = re.compile(r"(test ?#|test number|^test$|sample number|sample #|run|^id$|identifier)", re.I)
_DROPLET = ("droplet", "heptane", "methanol", "ethanol", "decane", "dodecane", "hexanol", "jet-a", "jp-8")
_GAS = ("methane", "ethane", "ethylene", "propane", "c2h4", "ch4", "c3h8", "hydrogen", "dme", "gas")


def _header(row, rx, skip=None):
    for k in row:
        if k and rx.search(k) and not (skip and skip.search(k)):
            return k
    return None


def _pressure_atm(header, value):
    x = num(value)
    if x is None or x <= 0:
        return None
    h = header.lower()
    if "mmhg" in h or "torr" in h:
        return x / 760
    if "kpa" in h:
        return x / 101.325
    if "psi" in h:
        return x / 14.696
    if "bar" in h:
        return x / 1.01325
    if "atm" in h:
        return x
    return x if x <= 10 else None  # sem unidade: só aceita o que já parece atm


def _outcome(text, header=""):
    t = (text or "").strip().lower()
    if not t:
        return "desconhecido"
    if "ignit" in header.lower() and re.fullmatch(r"\d+", t):  # "Flames Ignited: 2" é contagem de chamas acesas
        return "queimou" if int(t) > 0 else "nao_acendeu"
    if re.search(r"not? ?ignit|no ignition|failed|\bno\b|^n$", t):
        return "nao_acendeu"
    if "extingu" in t:
        return "extinguiu"
    if re.search(r"yes|ignit|burn|^y$", t):
        return "queimou"
    return "desconhecido"


def generic(acc, file, rows):
    if not rows:
        return
    sample = rows[0][1]
    h_o2, h_p, h_flow, h_g = _header(sample, _O2), _header(sample, _P), _header(sample, _FLOW), _header(sample, _G)
    h_out, h_fuel, h_label = _header(sample, _OUT, _MEASURE), _header(sample, _FUEL), _header(sample, _LABEL)
    if not (h_o2 or h_p or h_out):
        return  # nada que o Atlas saiba usar: melhor nenhum teste do que testes inventados
    for n, r in rows:
        fuel = r.get(h_fuel, "") if h_fuel else ""
        low = fuel.lower()
        if any(d in low for d in _DROPLET):
            family = "gota"
        elif solid_material(fuel) != "outro":
            family = "solido"
        elif any(g in low for g in _GAS):
            family = "gas"
        else:
            family = "gas"
        o2 = frac(r.get(h_o2)) if h_o2 else None
        p = _pressure_atm(h_p, r.get(h_p)) if h_p else None
        g = num(r.get(h_g)) if h_g else None
        out_raw = r.get(h_out) if h_out else None
        yield T(acc, file, n, r, (r.get(h_label) if h_label else "") or f"row {n}", family,
                fuel=fuel or None, material=solid_material(fuel) if family == "solido" else None,
                o2=o2, pressure_atm=p, gravity=g if g is not None else 0.0,
                flow_cm_s=num(r.get(h_flow)) if h_flow else None,
                outcome=_outcome(out_raw, h_out or ""), outcome_raw=out_raw or None,
                assumed=["family"] + ([] if h_g else ["gravity"]),
                metrics={"reading": "automatic (generic adapter)",
                         "columns": ", ".join(f"{k}←{v}" for k, v in (("O₂", h_o2), ("pressure", h_p), ("flow", h_flow),
                                                                      ("outcome", h_out)) if v)})


def parse(acc, file_name, path):
    fn = adapter_for(file_name) or generic
    seen, out = set(), []
    for t in fn(acc, file_name, read_table(path)):
        while t["id"] in seen:
            t["id"] += "b"
        seen.add(t["id"])
        if t["o2"] is not None and not (0 < t["o2"] <= 1):
            t["o2"] = None
        if t["pressure_atm"] is not None and t["pressure_atm"] <= 0:
            t["pressure_atm"] = None
        out.append(t)
    return out


FAMILY_EN = {"solido": "solid fuel sample", "gota": "liquid fuel droplet", "gas": "gaseous fuel flame", "fumaca": "smoke / aerosol"}
OUTCOME_EN = {"queimou": "burned (flame sustained)", "extinguiu": "flame extinguished", "disrupcao": "droplet disruption",
              "nao_acendeu": "did not ignite", "desconhecido": "outcome not tabulated"}


def card(t, inv_name):
    """Texto de uma ficha de teste para o índice vetorial (em inglês, como os documentos do PSI)."""
    bits = [f"{inv_name} ({t['investigation_id']}) test {t['label']}: {FAMILY_EN[t['family']]}"]
    if t["fuel"]:
        bits.append(f"fuel or material {t['fuel']}")
    if t["o2"] is not None:
        bits.append(f"oxygen {t['o2'] * 100:.1f}%")
    if t["pressure_atm"] is not None:
        bits.append(f"pressure {t['pressure_atm']:.2f} atm ({t['pressure_atm'] * 14.696:.1f} psi)")
    bits.append("normal gravity 1 g" if t["gravity"] >= 0.95 else "microgravity")
    if t["flow_cm_s"]:
        bits.append(f"air flow {t['flow_cm_s']:.0f} cm/s")
    if t["co2"]:
        bits.append(f"CO2 diluent {t['co2'] * 100:.0f}%")
    if t["he"]:
        bits.append(f"helium diluent {t['he'] * 100:.0f}%")
    bits.append(f"result: {OUTCOME_EN[t['outcome']]}" + (f" ({t['outcome_raw']})" if t["outcome_raw"] else ""))
    extra = ", ".join(f"{k}={v}" for k, v in t["metrics"].items() if k not in ("comments", "description") and v not in (None, ""))
    if extra:
        bits.append(extra)
    if t["metrics"].get("comments"):
        bits.append(f"comments: {t['metrics']['comments']}")
    return "; ".join(bits)
