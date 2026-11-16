package sim

import (
	"encoding/json"
	"fmt"
	"math"

	"atlas-api/domain/ptbr"
)

// A segunda conferência do simulador: a escala de tempo. O banco de testes (bench.js, B.spreadValidation)
// acende uma placa de acrílico pela borda de jusante, no ar de cada ensaio contínuo do ZARM, e mede a que
// velocidade a frente anda contra o vento. O arquivo (lab3d.spread/1) guarda só o que foi medido; a
// comparação com a velocidade medida nos ensaios é feita aqui.
//
// Não é uma validação do simulador contra a NASA: os ensaios são do ZARM, a amostra de lá é um filme de
// 0,075 mm e a do simulador é uma placa com a espessura de uma célula da grade. Serve para dar um número
// ao aviso "os tempos não são fiéis".

type spreadCheckFile struct {
	Schema    string  `json:"schema"`
	Date      string  `json:"date"`
	Engine    string  `json:"engine"`
	Tier      string  `json:"tier"`
	BurnTime  float64 `json:"burn_time_s"`
	Criterion string  `json:"criterion"`
	Sample    struct {
		Material  string  `json:"material"`
		Shape     string  `json:"shape"`
		Length    float64 `json:"length_cm"`
		Thickness float64 `json:"thickness_cm"`
		Width     float64 `json:"width_cm"`
	} `json:"sample"`
	Physics  map[string]float64 `json:"physics"`
	Material map[string]float64 `json:"material_params"`
	Measured struct {
		What    string `json:"what"`
		Source  string `json:"source"`
		License string `json:"license"`
	} `json:"measured"`
	Points []struct {
		Trial    string     `json:"trial"`
		O2       float64    `json:"o2_pct"`
		KPa      float64    `json:"pressure_kpa"`
		Flow     float64    `json:"flow_cm_s"`
		Measured float64    `json:"measured_mm_s"`
		Surface  *float64   `json:"sim_surface_mm_s"`
		Flame    *float64   `json:"sim_flame_mm_s"`
		Reached  [2]float64 `json:"reached_cm"`
		Burning  bool       `json:"burning_end"`
	} `json:"points"`
}

type spreadCheckRow struct {
	Trial    string   `json:"trial"`
	O2       float64  `json:"o2_pct"`
	Psi      float64  `json:"pressure_psi"`
	Flow     float64  `json:"flow_cm_s"`
	Measured float64  `json:"measured_mm_s"`
	Sim      *float64 `json:"sim_mm_s"`   // frente da superfície (250 °C), como nos ensaios
	Flame    *float64 `json:"flame_mm_s"` // frente da chama no gás (1200 K)
	Ratio    *float64 `json:"ratio"`      // Sim / Measured
	Reached  float64  `json:"reached_cm"` // até onde a frente da superfície chegou
	Burning  bool     `json:"burning_end"`
}

// spreadCheck devolve a comparação pronta, ou {"available": false} sem o arquivo.
func spreadCheck(files Files) map[string]any {
	raw, err := files.LoadSpreadCheck()
	var f spreadCheckFile
	if err != nil || json.Unmarshal(raw, &f) != nil || f.Schema != "lab3d.spread/1" || len(f.Points) == 0 {
		return map[string]any{"available": false}
	}
	rows := make([]spreadCheckRow, 0, len(f.Points))
	lo, hi, mlo, mhi := math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
	rlo, rhi := math.Inf(1), math.Inf(-1)
	n := 0
	for _, p := range f.Points {
		r := spreadCheckRow{Trial: p.Trial, O2: p.O2, Psi: ptbr.Round3(p.KPa / 6.894757), Flow: p.Flow, Measured: p.Measured,
			Sim: p.Surface, Flame: p.Flame, Reached: p.Reached[0], Burning: p.Burning}
		mlo, mhi = math.Min(mlo, p.Measured), math.Max(mhi, p.Measured)
		if p.Surface != nil && p.Measured > 0 {
			q := ptbr.Round3(*p.Surface / p.Measured)
			r.Ratio = &q
			lo, hi = math.Min(lo, *p.Surface), math.Max(hi, *p.Surface)
			rlo, rhi = math.Min(rlo, q), math.Max(rhi, q)
			n++
		}
		rows = append(rows, r)
	}
	var stale []string
	if cfg, err := files.LoadConfig(); err == nil {
		if ph, ok := cfg["physics"].(map[string]any); ok {
			stale = append(stale, changed(ph, f.Physics)...)
		}
		if mats, ok := cfg["materials"].([]any); ok {
			for _, m := range mats {
				if mm, ok := m.(map[string]any); ok && mm["id"] == f.Sample.Material {
					stale = append(stale, changed(mm, f.Material)...)
				}
			}
		}
	}
	if stale == nil {
		stale = []string{}
	}
	meas := fmt.Sprintf("%s to %s mm/s", ptbr.Num(mlo, 1), ptbr.Num(mhi, 1))
	var headline string
	switch {
	case n == 0:
		headline = fmt.Sprintf("In the simulator the flame does not spread along the sample: the front stayed near the igniter in all %d airs, where real tests measured %s.", len(rows), meas)
	case n < len(rows):
		headline = fmt.Sprintf("In the simulator the flame front spread in %d of %d airs, at %s to %s mm/s, where real tests measured %s.", n, len(rows), ptbr.Num(lo, 1), ptbr.Num(hi, 1), meas)
	default:
		headline = fmt.Sprintf("Along a flat acrylic sample, the simulator's flame front moves at %s to %s mm/s where real tests measured %s: %s.",
			ptbr.Num(lo, 1), ptbr.Num(hi, 1), meas, times(rlo, rhi))
	}
	if len(stale) > 0 {
		headline = "The simulator constants changed after this measurement; it needs to be redone. " + headline
	}
	return map[string]any{
		"available": true, "date": f.Date, "engine": f.Engine, "tier": f.Tier, "burn_time_s": f.BurnTime, "criterion": f.Criterion,
		"sample": f.Sample, "rows": rows, "n": n, "headline": headline, "stale": stale,
		"measured": f.Measured,
		"caveats": []string{
			"The real tests are from the ZARM drop tower, not from NASA, and burned a 0.075 mm film. The simulator's sample is a slab one grid cell thick (" + ptbr.Num(f.Sample.Thickness*10, 1) + " mm), because it cannot resolve a film.",
			"The simulator's solid heats up through its neighbours and is not consumed, so this speed comes from its gas flame, not from heating and burning away a real material.",
			"In the leanest airs the simulated front slows down along the slab, so that speed depends on where it is measured: two nearly identical airs can differ by a third.",
			"This is the only check of the simulator's time scale. It says nothing about how long a flame takes to go out.",
		},
	}
}

// times descreve a razão entre a velocidade do simulador e a medida.
func times(lo, hi float64) string {
	one := func(r float64) string {
		if r >= 1 {
			return ptbr.Num(r, 1) + " times as fast"
		}
		return ptbr.Num(1/r, 1) + " times slower"
	}
	switch {
	case lo > 0.8 && hi < 1.25:
		return "about the same speed"
	case (lo >= 1) != (hi >= 1):
		return "from " + one(lo) + " to " + one(hi)
	case lo >= 1:
		if hi-lo < 0.15 {
			return one(hi)
		}
		return ptbr.Num(lo, 1) + " to " + ptbr.Num(hi, 1) + " times as fast"
	}
	if hi-lo < 0.05 {
		return one(lo)
	}
	return ptbr.Num(1/hi, 1) + " to " + ptbr.Num(1/lo, 1) + " times slower"
}
