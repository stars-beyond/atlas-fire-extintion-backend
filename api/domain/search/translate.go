package search

import (
	"regexp"
	"strings"
)

// Os documentos do PSI estão em inglês. Este glossário leva os termos da pergunta em português
// para a busca por palavra; a busca vetorial já é multilíngue.
var glossary = map[string]string{
	"fogo": "fire", "incêndio": "fire", "incendio": "fire", "chama": "flame", "chamas": "flames", "fumaça": "smoke",
	"fumaca": "smoke", "fuligem": "soot", "gota": "droplet", "gotas": "droplets", "tecido": "fabric", "tecidos": "fabrics",
	"algodão": "cotton", "algodao": "cotton", "extinção": "extinction", "extincao": "extinction", "apaga": "extinguish",
	"apagar": "extinguish", "supressão": "suppression", "supressao": "suppression", "oxigênio": "oxygen",
	"oxigenio": "oxygen", "pressão": "pressure", "pressao": "pressure", "gravidade": "gravity", "microgravidade": "microgravity",
	"lua": "lunar", "lunar": "lunar", "marte": "mars", "nave": "spacecraft", "estação": "station", "estacao": "station",
	"inflamabilidade": "flammability", "inflamável": "flammable", "queima": "burning", "queimar": "burn", "ignição": "ignition",
	"ignicao": "ignition", "fluxo": "flow", "ventilação": "ventilation", "ventilacao": "ventilation", "hélio": "helium",
	"helio": "helium", "detector": "detector", "detecção": "detection", "deteccao": "detection", "material": "material",
	"materiais": "materials", "sólido": "solid", "solido": "solid", "sólidos": "solids", "limite": "limit", "limites": "limits",
	"propagação": "spread", "propagacao": "spread", "vela": "candle", "plástico": "plastic", "plastico": "plastic",
	"acrílico": "PMMA", "acrilico": "PMMA", "combustível": "fuel", "combustivel": "fuel", "taxa": "rate", "temperatura": "temperature",
	"aerossol": "aerosol", "poeira": "dust", "extintor": "extinguisher", "névoa": "mist", "nevoa": "mist", "água": "water", "agua": "water",
	"baixo": "low", "baixa": "low", "alto": "high", "alta": "high", "velocidade": "velocity", "ar": "air", "espacial": "space",
	"queimam": "burn", "espalha": "spreads", "espalhamento": "spread", "cabine": "cabin", "tripulação": "crew", "tripulacao": "crew",
	"concentração": "concentration", "concentracao": "concentration", "diluição": "dilution", "diluicao": "dilution", "risco": "hazard",
	"partículas": "particles", "particulas": "particles", "alarme": "alarm", "base": "base", "atmosfera": "atmosphere",
	"bombeiro": "firefighter", "traje": "spacesuit", "rover": "rover", "habitat": "habitat", "segurança": "safety", "seguranca": "safety",
}
var wordRe = regexp.MustCompile(`[\p{L}\p{N}][\p{L}\p{N}\-]*`)
var asciiRe = regexp.MustCompile(`^[a-z0-9]+$`)
var stop = map[string]bool{"what": true, "which": true, "with": true, "that": true, "this": true, "from": true, "have": true,
	"does": true, "para": true, "como": true, "qual": true, "quais": true, "onde": true, "sobre": true, "mais": true, "numa": true,
	"numa ": true, "esta": true, "este": true, "pode": true, "there": true, "about": true, "when": true, "will": true, "uma": true,
	"the": true, "and": true, "for": true, "are": true, "com": true, "que": true, "dos": true, "das": true, "num": true, "nos": true}

// ToEnglish troca os termos conhecidos por inglês antes de gerar o embedding da pergunta:
// o acervo está em inglês e o modelo aproxima melhor frases na mesma língua.
func ToEnglish(q string) string {
	return wordRe.ReplaceAllStringFunc(q, func(w string) string {
		if en, ok := glossary[strings.ToLower(w)]; ok {
			return en
		}
		return w
	})
}

// FTSQuery monta a consulta por palavra (to_tsquery) em inglês, com até 12 termos.
func FTSQuery(q string) string {
	seen := map[string]bool{}
	var terms []string
	for _, w := range wordRe.FindAllString(strings.ToLower(q), -1) {
		if en, ok := glossary[w]; ok {
			w = strings.ToLower(en)
		}
		w = strings.ReplaceAll(w, "-", "")
		if len(w) < 3 || stop[w] || seen[w] || !asciiRe.MatchString(w) {
			continue
		}
		seen[w] = true
		terms = append(terms, w)
		if len(terms) >= 12 {
			break
		}
	}
	return strings.Join(terms, " | ")
}
