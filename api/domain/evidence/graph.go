package evidence

import (
	"fmt"

	"atlas-api/domain/entities"
	"atlas-api/domain/ptbr"
)

// Grafo de relações: o cenário no centro, os testes mais parecidos ao redor e, ligados a eles,
// a investigação, o material, a gravidade e o desfecho. A estrutura (nós tipados e arestas)
// segue o agente da equipe (agent-fire-space, tools/relationship_graph.py). A diferença é que
// aqui cada teste entra só com o que a tabela da NASA traz: não há nota de risco calculada.

// GraphUseCase monta o grafo do cenário com os seis testes mais parecidos.
type GraphUseCase struct {
	Catalog *entities.Catalog
}

func (uc *GraphUseCase) Build(s entities.Scenario) ([]entities.GNode, []entities.GEdge) {
	return Graph(uc.Catalog, s, 6)
}

// Graph monta o grafo com os k testes mais parecidos.
func Graph(cat *entities.Catalog, s entities.Scenario, k int) ([]entities.GNode, []entities.GEdge) {
	v := Verdict(cat, s, nil, k)
	nodes := []entities.GNode{{ID: "cenario", Type: "cenario", Label: "Your scenario", Detail: fmt.Sprintf("%s O₂ · %s · %s", ptbr.Pct(v.Scenario.O2), ptbr.Psi(v.Scenario.P), ptbr.GLabel(v.Scenario.G))}}
	var edges []entities.GEdge
	seen := map[string]bool{}
	add := func(n entities.GNode) {
		if !seen[n.ID] {
			seen[n.ID] = true
			nodes = append(nodes, n)
		}
	}
	for _, n := range v.Nearest {
		t := n.Test
		id := "t:" + t.ID
		add(entities.GNode{ID: id, Type: "teste", Label: t.Label, TestID: t.ID, Family: t.Family, Inside: n.Inside,
			Detail: fmt.Sprintf("%s · %s O₂ · %s · %s", n.InvName, ptbr.Pct(*t.O2), ptbr.Psi(*t.P), ptbr.GLabel(t.G))})
		diff := "same condition"
		if !n.Inside {
			diff = ptbr.SignedPP(n.DO2pp) + " of O₂"
		}
		edges = append(edges, entities.GEdge{Source: "cenario", Target: id, Type: "PROXIMO", Label: diff})

		inv := "i:" + t.Inv
		add(entities.GNode{ID: inv, Type: "investigacao", Label: n.InvName, Detail: t.Inv})
		edges = append(edges, entities.GEdge{Source: id, Target: inv, Type: "DA_INVESTIGACAO"})

		mat := ""
		if t.Material != nil && ptbr.Material[*t.Material] != "" {
			mat = ptbr.Material[*t.Material]
		} else if t.Fuel != nil && *t.Fuel != "" {
			mat = ptbr.Shorten(*t.Fuel, 22)
		}
		if mat != "" {
			mid := "m:" + mat
			add(entities.GNode{ID: mid, Type: "material", Label: mat})
			edges = append(edges, entities.GEdge{Source: id, Target: mid, Type: "QUEIMOU"})
		}
		gid := "g:" + ptbr.GLabel(t.G)
		add(entities.GNode{ID: gid, Type: "gravidade", Label: ptbr.GLabel(t.G)})
		edges = append(edges, entities.GEdge{Source: id, Target: gid, Type: "EM_GRAVIDADE"})

		oid := "o:" + t.Outcome
		add(entities.GNode{ID: oid, Type: "desfecho", Label: ptbr.UpperFirst(ptbr.Outcome[t.Outcome])})
		edges = append(edges, entities.GEdge{Source: id, Target: oid, Type: "TERMINOU"})
	}
	return nodes, edges
}
