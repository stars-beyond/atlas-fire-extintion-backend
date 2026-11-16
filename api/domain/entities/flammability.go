package entities

import (
	"encoding/json"
	"math"
)

// FlamDoc são os desfechos lidos do relatório da NASA para uma investigação (hoje, o BASS-II).
// Os desfechos não vêm da tabela do PSI (ela não os traz): foram lidos do relatório por regras de texto,
// com a frase e a página de cada um, e entram no banco pela ingestão (brain/atlas/outcomes.py).
type FlamDoc struct {
	Investigation string          `json:"investigation"`
	Report        json.RawMessage `json:"report"`
	Method        string          `json:"method"`
	Summary       json.RawMessage `json:"summary"`
	Tests         []FlamTest      `json:"tests"`
	Audit         json.RawMessage `json:"audit"`
	Boundaries    []Boundary      `json:"boundaries"`
}

type FlamTest struct {
	Test        string          `json:"test"`
	AtlasID     string          `json:"atlas_id"`
	PI          string          `json:"pi"`
	Material    string          `json:"material"`
	Form        string          `json:"form"`
	Geometry    *string         `json:"geometry"`
	O2Initial   *float64        `json:"o2_initial"`
	O2Final     *float64        `json:"o2_final"`
	End         string          `json:"end"`
	EndLabel    string          `json:"end_label"`
	Observation *string         `json:"observation"`
	ReportPage  *int            `json:"report_page"`
	Limit       json.RawMessage `json:"limit"`
	Points      json.RawMessage `json:"points"`
	Flags       []string        `json:"flags"`
	Reviewed    *string         `json:"reviewed"`
}

// Boundary é a fronteira publicada no relatório (Eq. 6.1 do BASS-II) e a conferência dela
// contra os pontos extraídos. Os coeficientes vêm do arquivo de desfechos; a API não ajusta nada.
type Boundary struct {
	Form      string `json:"form"`
	Published struct {
		Equation  string `json:"equation"`
		Stretch   string `json:"stretch"`
		Source    string `json:"source"`
		URL       string `json:"url"`
		Minimum   string `json:"minimum"`
		AppliesTo string `json:"applies_to"`
		Coef      struct {
			A  float64 `json:"A"`
			K  float64 `json:"k"`
			B  float64 `json:"B"`
			C  float64 `json:"C"`
			R0 float64 `json:"r0_cm"`
		} `json:"coef"`
	} `json:"published"`
	Check  json.RawMessage `json:"check"`
	Band   float64         `json:"band_o2"`
	Tested struct {
		O2      []float64 `json:"o2"`
		FlowMax float64   `json:"flow_cm_s_max"`
		P       float64   `json:"pressure_atm"`
		G       float64   `json:"gravity"`
	} `json:"tested"`
	Curve    []CurvePt `json:"curve,omitempty"`
	Buoyancy struct {
		Flow1g  []float64       `json:"flow_1g_cm_s"`
		Super   string          `json:"superposition"`
		Sources json.RawMessage `json:"sources"`
		Partial string          `json:"partial_gravity"`
		Earth   json.RawMessage `json:"earth_check"`
	} `json:"buoyancy"`
}

type CurvePt struct {
	Flow float64 `json:"flow_cm_s"`
	O2   float64 `json:"o2"`
}

// LimitO2 é o oxigênio mínimo (em %) para a chama se sustentar com vento u (cm/s), pela curva publicada,
// para o bastão de referência (raio r0).
func (b *Boundary) LimitO2(u float64) float64 { return b.LimitO2r(u, 0) }

// LimitO2r faz a mesma conta para uma amostra de raio r (cm). O relatório escreve a curva na taxa de
// estiramento a = 3/2 · (U/r) · (r/r0)^½, que é como ele junta bastões de tamanhos diferentes numa curva só.
func (b *Boundary) LimitO2r(u, r float64) float64 {
	c := b.Published.Coef
	if r <= 0 {
		r = c.R0
	}
	a := 1.5 * u / math.Sqrt(r*c.R0)
	return c.A*math.Exp(-c.K*a) + c.B*a + c.C
}

// LimitRange é o menor e o maior limite de O₂ entre dois ventos (a curva tem um mínimo no meio).
func (b *Boundary) LimitRange(u0, u1, r float64) (lo, hi float64) {
	lo, hi = math.Inf(1), math.Inf(-1)
	for i := 0; i <= 200; i++ {
		v := b.LimitO2r(u0+(u1-u0)*float64(i)/200, r)
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	return
}

// Buoyant é o vento natural que a gravidade cria junto à chama, somado ao vento forçado como o relatório
// faz na Fig. 6.22. Em 1 g vale a faixa das duas fontes da NASA; em gravidade parcial ninguém mediu, então
// a faixa vai do caso do espaço (zero) ao da Terra. est é a faixa de 1 g escalada por g^(1/3), só
// como referência.
func (b *Boundary) Buoyant(g float64) (rng, est [2]float64) {
	f1 := b.Buoyancy.Flow1g
	if g <= 0.005 || len(f1) < 2 {
		return
	}
	if g >= 0.95 {
		rng = [2]float64{f1[0], f1[1]}
		return rng, rng
	}
	k := math.Cbrt(g)
	return [2]float64{0, f1[1]}, [2]float64{f1[0] * k, f1[1] * k}
}

// SampledCurve devolve a fronteira já amostrada (do ar quase parado até 60 cm/s), para a tela só desenhar.
func (b *Boundary) SampledCurve() []CurvePt {
	const n, u0, u1 = 160, 0.02, 60.0
	out := make([]CurvePt, n)
	for i := range out {
		u := math.Exp(math.Log(u0) + float64(i)/float64(n-1)*(math.Log(u1)-math.Log(u0)))
		out[i] = CurvePt{u, b.LimitO2(u)}
	}
	return out
}

// Assessment coloca um cenário no mapa de inflamabilidade. Nada aqui é previsão de modelo novo:
// é a curva do relatório lida na velocidade do ar do cenário, com as três fontes de incerteza à vista
// (a faixa em que os testes se espalham, o vento natural da gravidade e o que ficou fora da faixa testada).
type Assessment struct {
	Form       string     `json:"form"`
	Status     string     `json:"status"` // queima | no_limite | nao_sustenta
	Headline   string     `json:"headline"`
	Detail     string     `json:"detail"`
	O2         float64    `json:"o2"`
	LimitO2    [2]float64 `json:"limit_o2"`
	Band       float64    `json:"band_o2"`
	Margin     [2]float64 `json:"margin_o2"`
	Forced     float64    `json:"forced_cm_s"`
	Buoyant    [2]float64 `json:"buoyant_cm_s"`
	BuoyantEst [2]float64 `json:"buoyant_scaled_cm_s"` // escala g^(1/3), só como referência
	Flow       [2]float64 `json:"flow_cm_s"`
	// Dilution é quanto gás inerte (fração do ar) tira oxigênio suficiente para a chama apagar: do ponto em
	// que a extinção já é possível até o ponto em que ela é esperada. É só a conta de diluição do oxigênio.
	Dilution [2]float64 `json:"dilution_min"`
	Inside   bool       `json:"inside_tested"`
	Outside  []string   `json:"outside"`
	Source   string     `json:"source"`
}

// FormCount é quantos testes com pontos no mapa há para uma forma de amostra.
type FormCount struct {
	Form string `json:"form"`
	N    int    `json:"n"`
}
