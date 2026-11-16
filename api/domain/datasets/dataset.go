// Package datasets é o construtor de gráficos (vindo do dashboard COSMOEX da equipe): conjuntos de dados,
// registros, o gráfico pronto e o CSV. Um conjunto por investigação com tabela experimental, mais "todos"
// com as variáveis comuns. Regra herdada do COSMOEX: célula vazia ou textual fica ausente; nunca vira zero.
package datasets

import (
	"sort"

	"atlas-api/domain/entities"
	"atlas-api/domain/ptbr"
)

var commonNumeric = []entities.VarDef{
	{Key: "o2_pct", Label: "Oxygen", Unit: "% vol", Column: "initial atmosphere O₂"},
	{Key: "pressure_psi", Label: "Pressure", Unit: "psi", Column: "ambient pressure"},
	{Key: "gravity_g", Label: "Gravity", Unit: "g", Column: "test platform"},
	{Key: "flow_cm_s", Label: "Air velocity", Unit: "cm/s", Column: "airflow"},
	{Key: "co2_pct", Label: "CO₂ diluent", Unit: "% vol", Column: "initial atmosphere CO₂"},
	{Key: "he_pct", Label: "Helium diluent", Unit: "% vol", Column: "initial atmosphere He"},
}

var metricLabels = map[string][3]string{ // chave -> rótulo, unidade, coluna de origem
	"k_mm2_s":            {"Burning rate K", "mm²/s", "Burning rate"},
	"d0_mm":              {"Initial droplet diameter", "mm", "Droplet initial diameter"},
	"d_ext_mm":           {"Diameter at extinction", "mm", "Visible flame extinction diameter"},
	"burn_time_s":        {"Burn time", "s", "Burn time"},
	"n2":                 {"N₂ in the atmosphere", "fraction", "N initial ambient composition"},
	"tad_k":              {"Adiabatic temperature", "K", "Tad (K)"},
	"o2_burner_pct":      {"O₂ in the burner gas (inverse flames)", "% vol", "XO2"},
	"zst":                {"Stoichiometric mixture fraction", "", "Zst"},
	"o2_final_pct":       {"Final O₂", "% vol", "Calibrated final O2 % by vol"},
	"co2_initial_pct":    {"Initial CO₂", "% vol", "Initial CO2 % by vol"},
	"co2_final_pct":      {"Final CO₂", "% vol", "Final CO2 % by vol"},
	"co_final_ppm":       {"Final CO", "ppm", "Final CO (ppm)"},
	"nozzle_mm":          {"Nozzle diameter", "mm", "Nozzle (mm)"},
	"fuel_mg_s":          {"Fuel flow rate", "mg/s", "Fuel Rate (mg/s)"},
	"rt_r0":              {"Rt/R0 ratio", "", "Rt/R0"},
	"duration_s":         {"Duration", "s", "Duration (sec)"},
	"aging_s":            {"Aging time", "s", "Net aging time (sec)"},
	"aging_min":          {"Aging time", "min", "Aging Duration (minutes)"},
	"weight_loss_mg_min": {"Mass loss rate", "mg/min", "Weight loss rate (mg/min)"},
	"burner_mm":          {"Burner diameter", "mm", "Burner Size (mm)"},
	"flames_ignited_day": {"Flames ignited that day", "", "Flames Ignited"},
	"ignition_w":         {"Ignition power", "W", "Ignition Power (W)"},
}

var catLabels = map[string][2]string{ // chave -> rótulo, coluna de origem
	"fuel": {"Fuel or material", "Fuel / Material"}, "material": {"Material class", "derived from the material"},
	"outcome": {"Outcome", "tabulated outcome"}, "diluent": {"Diluent", "derived from CO₂ and He"},
	"gravity": {"Gravity", "test platform"}, "investigation": {"Investigation", "PSI investigation"},
	"family": {"Fuel family", "PSI investigation"},
	"pi":     {"Investigator (PI)", "PI"}, "flame": {"Flame type", "Flame Type"}, "direction": {"Flow direction", "Flow Direction"},
	"color": {"Flame color", "Color"}, "fiber": {"Support fiber", "Fiber"}, "cool_flame": {"Cool flame", "Cool flame?"},
	"day": {"Test day", "Day"}, "date": {"Test date", "Test Date"}, "test_type": {"Test type", "Test Type"},
	"polarity": {"Mesh polarity", "Mesh Polarity"},
}

func ptr[T any](v T) *T { return &v }

func record(cat *entities.Catalog, t *entities.Test, all bool) entities.Record {
	r := entities.Record{ID: t.ID, Label: t.Label, Inv: t.Inv, Num: map[string]*float64{}, Cat: map[string]*string{}, File: t.File, Row: t.Row, Assume: t.Assumed}
	if t.O2 != nil {
		r.Num["o2_pct"] = ptr(ptbr.Round3(*t.O2 * 100))
	}
	if t.P != nil {
		r.Num["pressure_psi"] = ptr(ptbr.Round3(*t.P * 14.696))
	}
	r.Num["gravity_g"] = ptr(t.G)
	r.Num["flow_cm_s"] = t.Flow
	if t.CO2 != nil {
		r.Num["co2_pct"] = ptr(ptbr.Round3(*t.CO2 * 100))
	}
	if t.He != nil {
		r.Num["he_pct"] = ptr(ptbr.Round3(*t.He * 100))
	}
	r.Cat["fuel"], r.Cat["material"] = t.Fuel, t.Material
	if t.Outcome != "desconhecido" {
		r.Cat["outcome"] = ptr(ptbr.Outcome[t.Outcome])
	}
	if t.CO2 != nil || t.He != nil {
		d := "N₂ only"
		if t.He != nil && *t.He > 0 {
			d = "with He"
		} else if t.CO2 != nil && *t.CO2 > 0 {
			d = "with CO₂"
		}
		r.Cat["diluent"] = &d
	}
	r.Cat["gravity"] = ptr(ptbr.GLabel(t.G))
	if all {
		if inv := cat.InvByID[t.Inv]; inv != nil {
			r.Cat["investigation"] = ptr(inv.Name)
		}
		r.Cat["family"] = ptr(ptbr.Family[t.Family])
		return r
	}
	for k, v := range t.Metrics {
		switch x := v.(type) {
		case float64:
			if _, ok := metricLabels[k]; ok {
				r.Num[k] = ptr(x)
			}
		case string:
			if _, ok := catLabels[k]; ok && x != "" {
				r.Cat[k] = ptr(x)
			}
		}
	}
	return r
}

// Build monta o conjunto (uma investigação ou "todos") e os registros. false se não existe ou está vazio.
func Build(cat *entities.Catalog, id string) (entities.Dataset, []entities.Record, bool) {
	all := id == "todos"
	ds := entities.Dataset{ID: id, Label: "All tests with tabulated O₂", Investigation: "All", PSI: ""}
	if !all {
		inv := cat.InvByID[id]
		if inv == nil {
			return ds, nil, false
		}
		ds.Label, ds.Investigation, ds.PSI = inv.Name+" ("+inv.ID+")", inv.Name, inv.ID
	}
	var recs []entities.Record
	files := map[string]bool{}
	for _, t := range cat.Tests {
		if (all && t.O2 == nil) || (!all && t.Inv != id) {
			continue
		}
		recs = append(recs, record(cat, t, all))
		files[t.File] = true
	}
	if len(recs) == 0 {
		return ds, nil, false
	}
	ds.Rows = len(recs)
	for f := range files {
		ds.Files = append(ds.Files, f)
	}
	sort.Strings(ds.Files)
	// variáveis: só as que têm valor em pelo menos dois registros (e variam, no caso das categóricas)
	for _, v := range commonNumeric {
		if n, distinct := countNum(recs, v.Key); n >= 2 && distinct >= 2 {
			v.N = n
			ds.Numeric = append(ds.Numeric, v)
		}
	}
	var mkeys []string
	for k := range metricLabels {
		mkeys = append(mkeys, k)
	}
	sort.Strings(mkeys)
	for _, k := range mkeys {
		if n, distinct := countNum(recs, k); n >= 2 && distinct >= 2 {
			l := metricLabels[k]
			ds.Numeric = append(ds.Numeric, entities.VarDef{Key: k, Label: l[0], Unit: l[1], Column: l[2], N: n})
		}
	}
	order := []string{"investigation", "family", "fuel", "material", "outcome", "diluent", "gravity", "flame", "direction", "pi", "color", "cool_flame", "fiber", "test_type", "polarity", "day", "date"}
	for _, k := range order {
		n, vals := 0, map[string]bool{}
		for _, r := range recs {
			if v := r.Cat[k]; v != nil {
				n++
				vals[*v] = true
			}
		}
		if n >= 2 && len(vals) >= 2 && len(vals) <= 40 {
			l := catLabels[k]
			ds.Categorical = append(ds.Categorical, entities.VarDef{Key: k, Label: l[0], Column: l[1], N: n})
		}
	}
	return ds, recs, true
}

func countNum(recs []entities.Record, key string) (int, int) {
	n, vals := 0, map[float64]bool{}
	for _, r := range recs {
		if v := r.Num[key]; v != nil {
			n++
			vals[*v] = true
		}
	}
	return n, len(vals)
}
