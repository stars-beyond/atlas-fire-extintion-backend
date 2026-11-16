package ask

import (
	"fmt"
	"strings"

	"atlas-api/domain/entities"
	"atlas-api/domain/ptbr"
)

// verdictText é a parte da resposta que não depende de busca nenhuma: o veredito e os três testes mais próximos.
func verdictText(v entities.Verdict) string {
	var b strings.Builder
	b.WriteString(v.Headline + " " + v.Explanation)
	if n := len(v.Nearest); n > 0 {
		k := min(n, 3)
		b.WriteString("\n\nTests nearest to the scenario:")
		for i := 0; i < k; i++ {
			nb := v.Nearest[i]
			fmt.Fprintf(&b, "\n- %s %s: %s O₂ at %s in %s, outcome: %s [T%d]", nb.InvName, nb.Test.Label, ptbr.Pct(*nb.Test.O2),
				ptbr.Psi(*nb.Test.P), ptbr.GLabel(nb.Test.G), ptbr.Outcome[nb.Test.Outcome], i+1)
		}
	}
	return b.String()
}

// extractive monta a resposta sem modelo de linguagem: veredito, testes vizinhos e os trechos mais relevantes.
func extractive(o entities.AskOut, hits []entities.Hit) string {
	var b strings.Builder
	b.WriteString(verdictText(o.Verdict))
	if o.Abstained {
		b.WriteString("\n\nThe archive search found no excerpts close to this question. PSI covers combustion in microgravity; rockets and propellants are outside its scope.")
		return b.String()
	}
	b.WriteString("\n\nWhat the archive says about the question:")
	for i := 0; i < min(len(hits), 3); i++ {
		fmt.Fprintf(&b, "\n- %s: “%s” [D%d]", hits[i].Title, ptbr.Shorten(strings.Join(strings.Fields(hits[i].Text), " "), 230), i+1)
	}
	if len(o.Ranking) > 0 {
		fmt.Fprintf(&b, "\n\nMost relevant investigation for this scenario: %s. %s", o.Ranking[0].Name, o.Ranking[0].Reason)
	}
	return b.String()
}
