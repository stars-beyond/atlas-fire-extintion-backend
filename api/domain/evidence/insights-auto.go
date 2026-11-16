package evidence

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"atlas-api/domain/entities"
	"atlas-api/domain/ptbr"
)

// Cartões gerados dos dados, sem texto escrito à mão: "Novidades do PSI" (o que a última ingestão trouxe) e um cartão
// por investigação nova, ainda sem perfil nem cartão próprio. Os cinco achados escritos à mão continuam em insights.go.

type changes struct {
	Since        *string                                      `json:"since"`
	NewInvs      []struct{ ID, Name, Title string }           `json:"new_investigations"`
	RemovedInvs  []string                                     `json:"removed_investigations"`
	NewTests     map[string]int                               `json:"new_tests"`
	NewDocuments []struct{ Inv, Name, File, Category string } `json:"new_documents"`
}

// NewsInsight resume o que entrou desde a ingestão anterior. Nada novo (ou primeira carga): sem cartão.
func NewsInsight(cat *entities.Catalog) *entities.Insight {
	var c changes
	if len(cat.Changes) == 0 || json.Unmarshal(cat.Changes, &c) != nil || c.Since == nil {
		return nil
	}
	nTests := 0
	for _, n := range c.NewTests {
		nTests += n
	}
	if len(c.NewInvs) == 0 && nTests == 0 && len(c.NewDocuments) == 0 && len(c.RemovedInvs) == 0 {
		return nil
	}
	var parts []string
	if len(c.NewInvs) > 0 {
		var names []string
		for _, v := range c.NewInvs {
			names = append(names, fmt.Sprintf("%s (%s)", v.Name, v.ID))
		}
		parts = append(parts, plural(len(c.NewInvs), "new investigation", "new investigations")+": "+strings.Join(names, ", "))
	}
	if nTests > 0 {
		var where []string
		for _, id := range sortedKeys(c.NewTests) {
			where = append(where, fmt.Sprintf("%d in %s", c.NewTests[id], invName(cat, id)))
		}
		parts = append(parts, plural(nTests, "new test", "new tests")+" ("+strings.Join(where, ", ")+")")
	}
	if len(c.NewDocuments) > 0 {
		byInv := map[string]int{}
		for _, d := range c.NewDocuments {
			byInv[d.Name]++
		}
		var where []string
		for _, n := range sortedKeys(byInv) {
			where = append(where, fmt.Sprintf("%d from %s", byInv[n], n))
		}
		parts = append(parts, plural(len(c.NewDocuments), "new document", "new documents")+" ("+strings.Join(where, ", ")+")")
	}
	if len(c.RemovedInvs) > 0 {
		parts = append(parts, plural(len(c.RemovedInvs), "investigation left", "investigations left")+" the PSI list: "+strings.Join(c.RemovedInvs, ", "))
	}
	return &entities.Insight{ID: "novidades", Title: "What's new from PSI since the last update",
		Finding: "Added to the collection: " + strings.Join(parts, "; ") + ". The findings and the verdict already take it into account.",
		Source:  "automatic comparison between the ingest of " + day(*c.Since) + " and the current one",
		Data:    c}
}

// AutoInvInsights descreve, a partir das estatísticas da ingestão, cada investigação que ainda não tem perfil
// escrito à mão (investigação nova no PSI).
func AutoInvInsights(cat *entities.Catalog) []entities.Insight {
	var meta struct {
		NoProfile []string `json:"investigations_without_profile"`
	}
	_ = json.Unmarshal(cat.Ingest, &meta)
	var out []entities.Insight
	for _, id := range meta.NoProfile {
		inv := cat.InvByID[id]
		if inv == nil {
			continue
		}
		var st struct {
			NTests   int            `json:"n_tests"`
			NPlot    int            `json:"n_plot"`
			O2       []float64      `json:"o2"`
			P        []float64      `json:"pressure_atm"`
			Outcomes map[string]int `json:"outcomes"`
			Varies   []string       `json:"varies"`
			Fuels    []string       `json:"fuels"`
		}
		_ = json.Unmarshal(inv.Stats, &st)
		var bits []string
		if st.NTests == 0 {
			bits = append(bits, "no test table the Atlas can read yet; for now it comes in through its documents and catalog entry")
		} else {
			bits = append(bits, plural(st.NTests, "test", "tests")+fmt.Sprintf(" (%d with O₂ and pressure, on the map)", st.NPlot))
			if len(st.O2) == 2 {
				bits = append(bits, "O₂ from "+ptbr.Pct(st.O2[0])+" to "+ptbr.Pct(st.O2[1]))
			}
			if len(st.P) == 2 {
				bits = append(bits, "pressure from "+ptbr.Num(st.P[0], 2)+" to "+ptbr.Num(st.P[1], 2)+" atm")
			}
			if len(st.Fuels) > 0 {
				bits = append(bits, "fuels: "+strings.Join(st.Fuels, ", "))
			}
			var outs []string
			for _, k := range sortedKeys(st.Outcomes) {
				if k != "desconhecido" {
					outs = append(outs, fmt.Sprintf("%d %s", st.Outcomes[k], ptbr.Outcome[k]))
				}
			}
			if len(outs) > 0 {
				bits = append(bits, "outcomes: "+strings.Join(outs, ", "))
			}
			if len(st.Varies) > 0 {
				bits = append(bits, "varies across tests: "+strings.Join(st.Varies, ", "))
			}
		}
		title := inv.Title
		if title == "" {
			title = inv.Acronym
		}
		out = append(out, entities.Insight{ID: "auto_" + id, Title: fmt.Sprintf("%s (%s): new investigation in the collection", invName(cat, id), id),
			Finding: ptbr.Shorten(title, 160) + ". " + upperFirst(strings.Join(bits, "; ")) +
				". Automatic summary: the team has not yet written a profile or finding for this investigation.",
			Source: id + " · statistics computed at ingest", Data: st})
	}
	return out
}

func invName(cat *entities.Catalog, id string) string {
	if inv := cat.InvByID[id]; inv != nil {
		if inv.Name != "" {
			return inv.Name
		}
		if inv.Acronym != "" {
			return inv.Acronym
		}
	}
	return id
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func day(iso string) string {
	if len(iso) >= 10 {
		return iso[8:10] + "/" + iso[5:7] + "/" + iso[0:4]
	}
	return iso
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}
