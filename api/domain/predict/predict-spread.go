package predict

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"atlas-api/domain/entities"
	"atlas-api/domain/ptbr"
)

// Velocidade de propagação da chama num filme fino de acrílico em 0 g, pelo modelo neural da equipe
// (stars-beyond/Atlas-MachineL, treinado nas gravações infravermelhas da torre de queda do ZARM).
//
// As redes não rodam aqui. ml/spread_table.py as roda numa grade de O₂ × pressão × fluxo e grava
// spread.json; este caso de uso interpola na grade e diz o quanto confiar:
//   - "interpolado": O₂, pressão, fluxo e pressão parcial de O₂ dentro da faixa dos ensaios;
//   - "extrapolado": dentro da grade, mas fora da faixa de algum deles (o valor sai com o aviso);
//   - "fora": além da grade. Não há número, porque o modelo não aprendeu a extinção: a 15% de O₂
//     ou sem vento ele ainda faz a chama andar.
//
// A pressão parcial entra na conta porque nos ensaios O₂ e pressão variam juntos; fora dessa linha
// o modelo não separa o efeito de um do outro.

const (
	spreadSchema = "atlas.spread/1"
	stationO2    = 21.0    // ar da estação, a referência da comparação
	stationKPa   = 101.325 //
	kPaPerAtm    = 101.325
	kPaPerPsi    = 6.894757
	defaultWind  = 10.0
)

// SpreadFiles lê a tabela gerada pelo modelo.
type SpreadFiles interface {
	LoadSpread() ([]byte, error)
}

// PredictSpreadUseCase prevê a velocidade de propagação para o ar do cenário.
type PredictSpreadUseCase struct {
	Files SpreadFiles
}

func (uc *PredictSpreadUseCase) Predict(in entities.SpreadIn) entities.SpreadOut {
	t, err := LoadTable(uc.Files)
	if err != nil {
		return entities.SpreadOut{Reason: err.Error(), OutOfRange: []string{}, Caveats: []string{}}
	}
	return Spread(t, in)
}

// LoadTable lê e confere a tabela do modelo.
func LoadTable(files SpreadFiles) (*entities.SpreadTable, error) {
	raw, err := files.LoadSpread()
	if err != nil {
		return nil, errors.New("The spread model table is not loaded.")
	}
	var t entities.SpreadTable
	if json.Unmarshal(raw, &t) != nil || !validSpread(&t) {
		return nil, errors.New("The spread model table is not in a known format.")
	}
	return &t, nil
}

func validSpread(t *entities.SpreadTable) bool {
	n := len(t.Axes.O2) * len(t.Axes.KPa) * len(t.Axes.Flow)
	if t.Schema != spreadSchema || len(t.Axes.O2) < 2 || len(t.Axes.KPa) < 2 || len(t.Axes.Flow) < 2 {
		return false
	}
	if len(t.V) != n || len(t.VLo) != n || len(t.VHi) != n || len(t.Stalls) != n {
		return false
	}
	for _, k := range []string{"o2_pct", "pressure_kpa", "flow_cm_s", "ppo2_kpa"} {
		if _, ok := t.TrainRange[k]; !ok {
			return false
		}
	}
	return true
}

// Spread interpola a tabela no ar do cenário.
func Spread(t *entities.SpreadTable, in entities.SpreadIn) entities.SpreadOut {
	s := in.Scenario.Clean()
	out := entities.SpreadOut{OutOfRange: []string{}, Caveats: []string{}}
	if s.Family != "solido" || s.Material != t.Sample.Material {
		out.Reason = "The model only knows thin acrylic (PMMA) film."
		return out
	}
	wind := defaultWind
	if in.Wind != nil {
		wind = math.Max(0, *in.Wind)
	}
	out.O2, out.KPa, out.Flow = ptbr.Round3(s.O2*100), ptbr.Round3(s.P*kPaPerAtm), ptbr.Round3(wind)
	out.PPO2 = ptbr.Round3(out.O2 * out.KPa / 100)

	// fora da faixa dos ensaios
	tol := t.Tolerance
	within := func(k string, v float64) bool {
		r := t.TrainRange[k]
		return v >= r[0]*(1-tol) && v <= r[1]*(1+tol)
	}
	ro, rp, rf, rpp := t.TrainRange["o2_pct"], t.TrainRange["pressure_kpa"], t.TrainRange["flow_cm_s"], t.TrainRange["ppo2_kpa"]
	if !within("o2_pct", out.O2) {
		out.OutOfRange = append(out.OutOfRange, fmt.Sprintf("Oxygen is %s%%; the tests went from %s%% to %s%%.", ptbr.Num(out.O2, 0), ptbr.Num(ro[0], 0), ptbr.Num(ro[1], 0)))
	}
	if !within("pressure_kpa", out.KPa) {
		out.OutOfRange = append(out.OutOfRange, fmt.Sprintf("Pressure is %s psi; the tests went from %s to %s psi.", ptbr.Num(out.KPa/kPaPerPsi, 1), ptbr.Num(rp[0]/kPaPerPsi, 1), ptbr.Num(rp[1]/kPaPerPsi, 1)))
	}
	if !within("flow_cm_s", out.Flow) {
		out.OutOfRange = append(out.OutOfRange, fmt.Sprintf("Airflow is %s cm/s; the tests went from %s to %s cm/s.", ptbr.Num(out.Flow, 0), ptbr.Num(rf[0], 0), ptbr.Num(rf[1], 0)))
	}
	// só faz sentido cobrar a pressão parcial quando O₂ e pressão, cada um, estão na faixa
	if within("o2_pct", out.O2) && within("pressure_kpa", out.KPa) && !within("ppo2_kpa", out.PPO2) {
		out.OutOfRange = append(out.OutOfRange, fmt.Sprintf("In the tests, oxygen went up as pressure went down (oxygen partial pressure from %s to %s kPa). This air is off that line, at %s kPa, where the model cannot tell the two effects apart.",
			ptbr.Num(rpp[0], 0), ptbr.Num(rpp[1], 0), ptbr.Num(out.PPO2, 0)))
	}
	out.InRange = len(out.OutOfRange) == 0

	v, lo, hi, stalls, ok := interp(t, out.O2, out.KPa, out.Flow)
	if !ok {
		out.Level = "fora"
		out.Reason = "No number here. " + strings.Join(out.OutOfRange, " ") +
			" The model does not know when a flame goes out, so it is not read this far from its tests."
		return out
	}
	out.Available = true
	out.Level = "interpolado"
	if !out.InRange {
		out.Level = "extrapolado"
	}
	out.V, out.Nets, out.Stalls = ptbr.Round3(v), [2]float64{ptbr.Round3(math.Min(lo, v)), ptbr.Round3(math.Max(hi, v))}, stalls
	if out.V > 0.05 {
		out.Per10cm = math.Round(100 / out.V)
	}
	if b, _, _, _, ok := interp(t, stationO2, stationKPa, out.Flow); ok && b > 0.05 {
		out.Baseline = ptbr.Round3(b)
		out.Ratio = ptbr.Round3(v / b)
	}

	out.Headline = "Flame spreads at " + ptbr.Num(out.V, 1) + " mm/s"
	switch {
	case out.Ratio == 0:
	case math.Abs(out.O2-stationO2) < 0.5 && math.Abs(out.KPa-stationKPa) < 2:
		out.Compare = "This is station air, the reference for the other habitats."
	case out.Ratio >= 1.05:
		out.Compare = ptbr.Num(out.Ratio, 1) + " times faster than in station air (" + ptbr.Num(out.Baseline, 1) + " mm/s at the same airflow)."
	case out.Ratio <= 0.95:
		out.Compare = ptbr.Num((1-out.Ratio)*100, 0) + "% slower than in station air (" + ptbr.Num(out.Baseline, 1) + " mm/s at the same airflow)."
	default:
		out.Compare = "About the same as in station air (" + ptbr.Num(out.Baseline, 1) + " mm/s at the same airflow)."
	}
	out.Detail = fmt.Sprintf("Thin acrylic film (%s mm) in weightlessness, with the flame moving against a %s cm/s airflow. The %d networks give %s to %s mm/s.",
		ptbr.Num(t.Sample.Thickness, 3), ptbr.Num(out.Flow, 0), t.Model.Nets, ptbr.Num(out.Nets[0], 1), ptbr.Num(out.Nets[1], 1))
	if out.Per10cm > 0 {
		out.Detail += " At this speed, 10 cm of film burn in " + ptbr.Num(out.Per10cm, 0) + " s."
	}

	if s.G > 0.005 {
		out.GravityIgnored = true
		out.Caveats = append(out.Caveats, "Gravity is not an input of the model: every test was in weightlessness. The number is for this air in 0 g, not for "+ptbr.GLabel(s.G)+".")
	}
	if stalls {
		out.Caveats = append(out.Caveats, "Here the model's flame front stops before the end of the sample. The model was not validated for extinction, so this is not evidence that the fire goes out.")
	}
	out.Caveats = append(out.Caveats,
		"It says how fast, not whether. The model almost never predicts that a flame goes out, so it cannot say the fire will stop.",
		"One material and one thickness. The number does not carry over to thicker parts or to other materials.",
		"The spread between the networks shows where they disagree. It is narrower than the real error and is not a confidence interval.")

	out.Note = fmt.Sprintf("Neural model of the team (%d small U-Nets) trained on %d infrared recordings from the ZARM drop tower in Bremen. These are not NASA tests. On runs the networks never saw, the spread speed is off by %s mm/s on average (model README); on the %d continuous runs measured, most of which the networks saw in training, the table reproduces the speed within %s mm/s on average.",
		t.Model.Nets, t.Model.Trials, ptbr.Num(t.Reported.Barriers, 1), len(t.Check.Rows), ptbr.Num(t.Check.MAE, 1))
	out.Model = &entities.SpreadModel{Name: t.Model.Name, URL: t.Model.URL, Commit: t.Model.Commit, Date: t.Date, Nets: t.Model.Nets, Trials: t.Model.Trials,
		Sample:     fmt.Sprintf("%s, %s mm, %s mm long, 0 g, opposed flow", t.Sample.Form, ptbr.Num(t.Sample.Thickness, 3), ptbr.Num(t.Sample.Length, 0)),
		HeldoutMAE: t.Reported.Barriers, CheckMAE: t.Check.MAE, License: t.License}
	out.Check = t.Check.Rows
	out.Sources = t.Attribution
	return out
}

// bracket acha o intervalo do eixo que contém x: índice de baixo e fração. ok = false fora do eixo.
func bracket(ax []float64, x float64) (int, float64, bool) {
	n := len(ax)
	if x < ax[0]-1e-9 || x > ax[n-1]+1e-9 {
		return 0, 0, false
	}
	for i := 0; i < n-1; i++ {
		if x <= ax[i+1] {
			return i, math.Min(1, math.Max(0, (x-ax[i])/(ax[i+1]-ax[i]))), true
		}
	}
	return n - 2, 1, true
}

// interp faz a interpolação trilinear da média e dos extremos entre as redes. stalls = algum canto
// da célula tem a frente parada.
func interp(t *entities.SpreadTable, o2, kpa, flow float64) (v, lo, hi float64, stalls, ok bool) {
	io, fo, ok1 := bracket(t.Axes.O2, o2)
	ip, fp, ok2 := bracket(t.Axes.KPa, kpa)
	iu, fu, ok3 := bracket(t.Axes.Flow, flow)
	if !ok1 || !ok2 || !ok3 {
		return 0, 0, 0, false, false
	}
	np, nu := len(t.Axes.KPa), len(t.Axes.Flow)
	for a := 0; a < 2; a++ {
		for b := 0; b < 2; b++ {
			for c := 0; c < 2; c++ {
				w := pick(fo, a) * pick(fp, b) * pick(fu, c)
				if w == 0 {
					continue
				}
				k := ((io+a)*np+(ip+b))*nu + (iu + c)
				v += w * t.V[k]
				lo += w * t.VLo[k]
				hi += w * t.VHi[k]
				stalls = stalls || t.Stalls[k] != 0
			}
		}
	}
	return v, lo, hi, stalls, true
}

func pick(f float64, upper int) float64 {
	if upper == 1 {
		return f
	}
	return 1 - f
}
