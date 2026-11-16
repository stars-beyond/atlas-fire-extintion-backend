// Package entities tem os tipos do Atlas: o cenário, o acervo (investigações e testes) e as respostas da API.
package entities

import (
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Scenario é o ambiente que o usuário quer avaliar: a "sonda" no território.
type Scenario struct {
	O2       float64 `json:"o2"`           // fração molar, 0 a 1
	P        float64 `json:"pressure_atm"` // atm
	G        float64 `json:"gravity"`      // em g terrestres
	Family   string  `json:"family"`       // solido | gota | gas | todas
	Material string  `json:"material"`     // só para sólidos; vazio = qualquer
	Habitat  string  `json:"habitat,omitempty"`
}

// Vizinhança: o quanto um teste pode diferir do cenário e ainda contar como "mesma condição".
const (
	TolO2 = 0.02 // 2 pontos percentuais de O₂
	TolP  = 0.20 // razão de pressão até e^0,2 (cerca de ±20%)
	TolG  = 0.05 // gravidade
)

// maxLabel limita os textos do cenário (família, material, habitat). São códigos curtos, e vão para o prompt do
// modelo e para o título do veredito: sem o limite, um texto longo ali contorna o limite de tamanho da pergunta.
const maxLabel = 64

// label tira espaços das pontas e caracteres de controle (quebras de linha) e corta em maxLabel caracteres.
func label(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if utf8.RuneCountInString(s) > maxLabel {
		s = string([]rune(s)[:maxLabel])
	}
	return s
}

// Clean põe o cenário dentro das faixas aceitas e completa a família.
func (s Scenario) Clean() Scenario {
	s.O2 = math.Min(1, math.Max(0.01, s.O2))
	s.P = math.Min(6, math.Max(0.1, s.P))
	s.G = math.Min(1, math.Max(0, s.G))
	s.Family, s.Material, s.Habitat = label(s.Family), label(s.Material), label(s.Habitat)
	if s.Family == "" {
		s.Family = "solido"
	}
	return s
}

// Matches diz se o teste é do tipo de combustível do cenário e tem O₂ e pressão tabelados.
func (s Scenario) Matches(t *Test) bool {
	if !t.Plot() {
		return false
	}
	if s.Family != "todas" && t.Family != s.Family {
		return false
	}
	if s.Material != "" && s.Family == "solido" && (t.Material == nil || *t.Material != s.Material) {
		return false
	}
	return true
}

// DO2 é a diferença de oxigênio (fração) entre o teste e o cenário.
func (s Scenario) DO2(t *Test) float64 { return *t.O2 - s.O2 }

// DP é a diferença de pressão em log da razão.
func (s Scenario) DP(t *Test) float64 { return math.Log(*t.P / s.P) }

// DAtm é a distância na atmosfera, em "vizinhanças": 1 = na borda da tolerância.
func (s Scenario) DAtm(t *Test) float64 {
	return math.Hypot(s.DO2(t)/TolO2, s.DP(t)/TolP)
}

// NearAtm diz se o teste está dentro da tolerância de O₂ e de pressão.
func (s Scenario) NearAtm(t *Test) bool {
	return math.Abs(s.DO2(t)) <= TolO2+1e-9 && math.Abs(s.DP(t)) <= TolP+1e-9
}
