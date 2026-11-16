package entities

// Habitat é um ambiente pronto do configurador.
type Habitat struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	O2     float64 `json:"o2"`
	P      float64 `json:"pressure_atm"`
	G      float64 `json:"gravity"`
	Note   string  `json:"note"`
	Target bool    `json:"target"` // entra na conta dos "próximos testes"
}

// Habitats são os seis ambientes do configurador, na ordem da tela.
var Habitats = []Habitat{
	{"iss", "Space station", 0.21, 1.0, 0, "Sea-level air in orbit: 21% O₂ at 14.7 psi.", true},
	{"lua", "Lunar base", 0.30, 0.694, 0.165, "Limit cited in the challenge statement: 30% O₂ or more, 10.2 psi or less.", true},
	{"rover", "Pressurized rover", 0.34, 0.558, 0.165, "Exploration atmosphere: 34% O₂ at 8.2 psi, to shorten pre-breathing before going outside.", true},
	{"marte", "Mars base", 0.34, 0.558, 0.378, "Same exploration atmosphere, at 0.38 g.", true},
	{"traje", "Spacesuit", 1.0, 0.2925, 0, "Pure oxygen at 4.3 psi, as described in the challenge statement.", false},
	{"terra", "Closed room on Earth", 0.21, 1.0, 1, "Battery or server room with gas suppression.", false},
}

// VentForced é a ventilação de cabine usada nas leituras e no simulador, em cm/s.
const VentForced = 10.0
