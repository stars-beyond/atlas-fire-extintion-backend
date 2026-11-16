package sim

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"

	"atlas-api/domain/entities"
	"atlas-api/domain/flammability"
	"atlas-api/domain/ptbr"
)

// simValidation é o que o banco de testes do simulador mede (bench.html, B.validation()): o oxigênio
// mínimo que sustenta a chama em cada vento, por bisseção. O arquivo guarda só o que foi medido e as
// constantes usadas na medição; a comparação com a curva da NASA é feita aqui, a cada pedido.
type simValidation struct {
	Schema    string  `json:"schema"`
	Date      string  `json:"date"`
	Engine    string  `json:"engine"`
	GPU       string  `json:"gpu"`
	Tier      string  `json:"tier"`
	BurnTime  float64 `json:"burn_time_s"`
	Criterion string  `json:"criterion"`
	Sample    struct {
		Material string  `json:"material"`
		Shape    string  `json:"shape"`
		Radius   float64 `json:"radius_cm"`
	} `json:"sample"`
	Pressure float64            `json:"pressure_psi"`
	Physics  map[string]float64 `json:"physics"`
	Material map[string]float64 `json:"material_params"`
	Points   []struct {
		G    float64 `json:"gravity"`
		Wind float64 `json:"wind_cm_s"`
		O2   float64 `json:"o2_min"`
		PM   float64 `json:"plus_minus"`
	} `json:"points"`
}

type valRow struct {
	G      float64    `json:"gravity"`
	Wind   float64    `json:"wind_cm_s"`
	Sim    float64    `json:"sim_o2"`
	PM     float64    `json:"plus_minus"`
	Nasa   [2]float64 `json:"nasa_o2"` // sem gravidade os dois valores coincidem
	Diff   *float64   `json:"diff_o2"` // só sem gravidade: simulador menos curva
	Inside bool       `json:"inside"`  // dentro da curva mais ou menos o espalhamento dos testes
}

// changed lista as constantes que hoje diferem das usadas na medição.
func changed(now map[string]any, then map[string]float64) []string {
	var out []string
	for k, v := range then {
		cur, ok := now[k].(float64)
		if !ok || math.Abs(cur-v) > 1e-9*math.Max(1, math.Abs(v)) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// GetValidationUseCase compara a medição do simulador com a curva publicada, a cada pedido.
type GetValidationUseCase struct {
	Catalog *entities.Catalog
	Files   Files
}

func (uc *GetValidationUseCase) Get() map[string]any {
	raw, err := uc.Files.LoadValidation()
	var v simValidation
	if err != nil || json.Unmarshal(raw, &v) != nil || v.Schema != "lab3d.validation/1" {
		return map[string]any{"available": false}
	}
	var b *entities.Boundary
	if d := uc.Catalog.Outcomes[flammability.BassII]; d != nil {
		for i := range d.Boundaries {
			if simMaterial[d.Boundaries[i].Form] == v.Sample.Material {
				b = &d.Boundaries[i]
			}
		}
	}
	if b == nil {
		return map[string]any{"available": false}
	}
	rows := make([]valRow, 0, len(v.Points))
	var sq, worst float64
	n, inside := 0, 0
	for _, p := range v.Points {
		rng, _ := b.Buoyant(p.G)
		lo, hi := b.LimitRange(p.Wind+rng[0], p.Wind+rng[1], v.Sample.Radius)
		row := valRow{G: p.G, Wind: p.Wind, Sim: p.O2, PM: p.PM, Nasa: [2]float64{lo, hi},
			Inside: p.O2 >= lo-b.Band && p.O2 <= hi+b.Band}
		if p.G <= 0.005 {
			d := p.O2 - lo
			row.Diff = &d
			sq += d * d
			worst = math.Max(worst, math.Abs(d))
			n++
			if row.Inside {
				inside++
			}
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].G != rows[j].G {
			return rows[i].G < rows[j].G
		}
		return rows[i].Wind < rows[j].Wind
	})
	var stale []string
	if cfg, err := uc.Files.LoadConfig(); err == nil {
		if ph, ok := cfg["physics"].(map[string]any); ok {
			stale = append(stale, changed(ph, v.Physics)...)
		}
		if mats, ok := cfg["materials"].([]any); ok {
			for _, m := range mats {
				if mm, ok := m.(map[string]any); ok && mm["id"] == v.Sample.Material {
					stale = append(stale, changed(mm, v.Material)...)
				}
			}
		}
	}
	if stale == nil {
		stale = []string{}
	}
	rms := 0.0
	if n > 0 {
		rms = math.Sqrt(sq / float64(n))
	}
	headline := fmt.Sprintf("Across %d airflows without gravity, the simulator's minimum oxygen is on average %s point from the curve published by NASA (worst case, %s). The tests themselves scatter %s point around it.",
		n, ptbr.Num(rms, 1), ptbr.Num(worst, 1), ptbr.Num(b.Band, 1))
	if len(stale) > 0 {
		headline = "The simulator constants changed after this measurement; it needs to be redone. " + headline
	}
	return map[string]any{
		"spread":    spreadCheck(uc.Files),
		"available": true, "date": v.Date, "engine": v.Engine, "gpu": v.GPU, "tier": v.Tier, "criterion": v.Criterion,
		"sample": v.Sample, "pressure_psi": v.Pressure, "rows": rows, "n": n, "inside": inside, "rms_o2": rms, "max_abs_o2": worst,
		"band_o2": b.Band, "headline": headline, "stale": stale, "source": b.Published.Source, "url": b.Published.URL,
		"equation": b.Published.Equation, "stretch": b.Published.Stretch,
		"how": "The curve is read at the stretch rate of the simulator's sample (a sphere), as the report does to bring rods of different sizes onto one curve. With gravity, the natural airflow is added to the forced airflow: 20 to 35 cm/s at 1 g; from zero to 35 cm/s in partial gravity, because nobody has measured it.",
		"caveats": []string{
			"Times are not faithful. The simulator's solid does not heat up or get consumed as in a real test, so how long the flame takes to ignite or go out cannot be quoted.",
			"The reaction, radiation and buoyancy constants were tuned by hand. None came from a measurement.",
			"Only acrylic was compared. There is no published curve for the cotton fabric or the flame-retardant fabric.",
			"The heat released, in watts, was not compared with any NASA data.",
			"The comparison was made at 14.7 psi. The effect of pressure in the simulator was not checked.",
		},
	}
}
