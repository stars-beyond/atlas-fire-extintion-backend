package ask

import (
	"regexp"
	"strconv"
	"strings"

	"atlas-api/domain/entities"
	"atlas-api/domain/ptbr"
)

var (
	reO2a  = regexp.MustCompile(`(\d+(?:[.,]\d+)?)\s*(?:%|percent|por\s*cento)\s*(?:de\s+|of\s+)?(?:o2|o₂|oxig|oxyg)`)
	reO2b  = regexp.MustCompile(`(?:o2|o₂|oxig[êe]nio|oxygen)\D{0,14}?(\d+(?:[.,]\d+)?)\s*(?:%|percent|por\s*cento)`)
	rePsi  = regexp.MustCompile(`(\d+(?:[.,]\d+)?)\s*psi`)
	reAtm  = regexp.MustCompile(`(\d+(?:[.,]\d+)?)\s*atm`)
	reKpa  = regexp.MustCompile(`(\d+(?:[.,]\d+)?)\s*kpa`)
	reG    = regexp.MustCompile(`(\d(?:[.,]\d+)?)\s*g(?:\b|$)`)
	reGb   = regexp.MustCompile(`(?:gravidade|gravity)\D{0,12}?(\d(?:[.,]\d+)?)`)
	reMark = regexp.MustCompile(`\[([TD]\d+)\]`)
	reISS  = regexp.MustCompile(`\biss\b`)
	// a pergunta aponta para o cenário montado na tela ("este material", "aqui", "neste ambiente")
	reDeictic = regexp.MustCompile(`\b(este|esta|esse|essa|neste|nesta|nesse|nessa|deste|desta|aqui|this|here)\b|\b(cen[áa]rio|ambiente|habitat|material|scenario)\b`)
)

func num(s string) float64 {
	f, _ := strconv.ParseFloat(strings.Replace(s, ",", ".", 1), 64)
	return f
}

func hasAny(s string, words ...string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// ParseScenario lê condições escritas na pergunta e ajusta o cenário atual.
func ParseScenario(q string, cur entities.Scenario) (entities.Scenario, []string) {
	s := cur
	l := " " + strings.ToLower(q) + " "
	var got []string
	preset := func(id string) {
		for _, h := range entities.Habitats {
			if h.ID == id {
				s.O2, s.P, s.G, s.Habitat = h.O2, h.P, h.G, h.ID
				got = append(got, "habitat: "+h.Name)
			}
		}
	}
	switch {
	case hasAny(l, "rover"):
		preset("rover")
	case hasAny(l, " lua", "lunar", "moon"):
		preset("lua")
	case hasAny(l, "marte", "mars", "marcian"):
		preset("marte")
	case hasAny(l, "traje", "spacesuit", "space suit", " eva "):
		preset("traje")
	case reISS.MatchString(l) || hasAny(l, "estação espacial", "estacao espacial", "space station", "em órbita", "em orbita", "in orbit"):
		preset("iss")
	case hasAny(l, "na terra", "on earth", "bateria", "battery", "servidor", " server", "data center"):
		preset("terra")
	}
	if m := reO2a.FindStringSubmatch(l); m != nil {
		s.O2 = num(m[1]) / 100
		got = append(got, "O₂: "+ptbr.Pct(s.O2))
	} else if m := reO2b.FindStringSubmatch(l); m != nil {
		s.O2 = num(m[1]) / 100
		got = append(got, "O₂: "+ptbr.Pct(s.O2))
	}
	if m := rePsi.FindStringSubmatch(l); m != nil {
		s.P = num(m[1]) / 14.696
		got = append(got, "pressure: "+ptbr.Psi(s.P))
	} else if m := reKpa.FindStringSubmatch(l); m != nil {
		s.P = num(m[1]) / 101.325
		got = append(got, "pressure: "+ptbr.Psi(s.P))
	} else if m := reAtm.FindStringSubmatch(l); m != nil {
		s.P = num(m[1])
		got = append(got, "pressure: "+ptbr.Psi(s.P))
	}
	if m := reG.FindStringSubmatch(l); m != nil && num(m[1]) <= 1 {
		s.G = num(m[1])
		got = append(got, "gravity: "+ptbr.GLabel(s.G))
	} else if hasAny(l, "microgravidade", "microgravity", "zero gravity", "zero-g", "weightless") {
		s.G = 0
		got = append(got, "gravity: 0 g")
	} else if m := reGb.FindStringSubmatch(l); m != nil && num(m[1]) <= 1 {
		s.G = num(m[1])
		got = append(got, "gravity: "+ptbr.GLabel(s.G))
	}
	mat := map[string][]string{
		"nomex": {"nomex"}, "pmma": {"pmma", "acrílico", "acrilico", "acrylic"},
		"sibal": {"sibal", "algodão", "algodao", "cotton"}, "silicone": {"silicone"},
	}
	for id, words := range mat {
		if hasAny(l, words...) {
			s.Family, s.Material = "solido", id
			got = append(got, "material: "+id)
		}
	}
	switch {
	case s.Material != "" && s.Family == "solido":
	case hasAny(l, "tecido", "fabric", "plástico", "plastico", "plastic", "sólido", "solido", "solid", "material", "materiais"):
		s.Family, s.Material = "solido", ""
		got = append(got, "family: solids")
	case hasAny(l, "gota", "droplet", "heptano", "heptane", "metanol", "methanol", "líquido", "liquido", "liquid", "flex", "hélio", "helio", "helium"):
		s.Family, s.Material = "gota", ""
		got = append(got, "family: droplets")
	case hasAny(l, "gás", " gas ", "gasos", "gaseous", "propano", "etileno", "metano", "methane", "ethylene", "propane"):
		s.Family, s.Material = "gas", ""
		got = append(got, "family: gas flames")
	}
	return s.Clean(), got
}
