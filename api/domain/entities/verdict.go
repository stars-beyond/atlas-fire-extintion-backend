package entities

// Neighbor é um teste do acervo perto do cenário, com as diferenças já calculadas.
type Neighbor struct {
	Test     *Test   `json:"test"`
	InvName  string  `json:"investigation"`
	Distance float64 `json:"distance"`
	DO2pp    float64 `json:"d_o2_pp"`
	DPpct    float64 `json:"d_pressure_pct"`
	DG       float64 `json:"d_gravity"`
	Inside   bool    `json:"inside"`
}

// Verdict é o nível de evidência de um cenário, decidido pela regra fixa de domain/evidence.
type Verdict struct {
	Tier        string         `json:"tier"` // medido | delimitado | parcial | sem_evidencia
	Label       string         `json:"label"`
	Headline    string         `json:"headline"`
	Explanation string         `json:"explanation"`
	SameG       int            `json:"same_g"`
	ZeroG       int            `json:"zero_g"`
	OneG        int            `json:"one_g"`
	InAtm       int            `json:"in_atmosphere"` // testes nesta atmosfera, em qualquer gravidade (cada um contado uma vez)
	Outcomes    map[string]int `json:"outcomes"`
	Nearest     []Neighbor     `json:"nearest"`
	Scenario    Scenario       `json:"scenario"`
	Empirical   *Empirical     `json:"empirical"` // só existe quando o veredito é "medido"
	// ensaios de fora da NASA nesta atmosfera; não entram no nível de evidência (só no veredito pedido pela tela)
	External *ExternalEvidence `json:"external"`
}

// Empirical é a taxa empírica: a contagem do que aconteceu nos testes reais desta condição.
// Substitui a "probabilidade de ignição" do agente da equipe, que misturava uma fórmula escrita
// à mão com os dados. Aqui não há modelo: só ensaios com desfecho tabelado, e só quando o
// cenário cai numa condição que a NASA de fato testou.
type Empirical struct {
	Total        int      `json:"total"`        // testes nesta atmosfera e nesta gravidade
	Known        int      `json:"known"`        // dos quais com desfecho na tabela
	Burned       int      `json:"burned"`       // a chama se sustentou na condição de partida ("queimou")
	Extinguished int      `json:"extinguished"` // acenderam e apagaram sozinhos
	Disrupted    int      `json:"disrupted"`    // a gota estourou antes do fim
	NoIgnition   int      `json:"no_ignition"`  // não acenderam
	Unknown      int      `json:"unknown"`      // sem desfecho tabelado
	Rate         *float64 `json:"rate"`         // sustentaram a chama / com desfecho; nulo se nenhum tem desfecho
	Headline     string   `json:"headline"`
	Caveat       string   `json:"caveat"`
	By           []EmpInv `json:"by_investigation"`
}

type EmpInv struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Total  int    `json:"total"`
	Known  int    `json:"known"`
	Burned int    `json:"burned"`
}

// MatrixCell é o veredito do mesmo cenário para uma família de combustível.
type MatrixCell struct {
	Family string `json:"family"`
	Tier   string `json:"tier"`
	Label  string `json:"label"`
	N      int    `json:"n"`
}

// NextTest é um ensaio sugerido para melhorar os vereditos dos habitats-alvo.
type NextTest struct {
	O2       float64  `json:"o2"`
	P        float64  `json:"pressure_atm"`
	G        float64  `json:"gravity"`
	Platform string   `json:"platform"`
	Gain     float64  `json:"gain"`
	Closes   []string `json:"closes"`
	Why      string   `json:"why"`
}

// RankParts é a nota de uma investigação aberta em quatro partes.
type RankParts struct {
	Proximity float64 `json:"proximidade"`
	Relevance float64 `json:"relevancia"`
	Data      float64 `json:"dados"`
	Priority  float64 `json:"enunciado"`
}

// Ranked é uma investigação no ranking do cenário.
type Ranked struct {
	ID     string    `json:"id"`
	Name   string    `json:"name"`
	Family string    `json:"family"`
	Role   string    `json:"role"`
	Score  float64   `json:"score"`
	Parts  RankParts `json:"parts"`
	Reason string    `json:"reason"`
}

// GNode e GEdge formam o grafo de relações entre o cenário e os testes mais parecidos.
type GNode struct {
	ID     string `json:"id"`
	Type   string `json:"type"` // cenario | teste | investigacao | material | gravidade | desfecho
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
	TestID string `json:"test_id,omitempty"`
	Family string `json:"family,omitempty"`
	Inside bool   `json:"inside,omitempty"`
}

type GEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"` // PROXIMO | DA_INVESTIGACAO | QUEIMOU | EM_GRAVIDADE | TERMINOU
	Label  string `json:"label,omitempty"`
}

// Insight é um achado calculado sobre os testes, com a frase que o resume.
type Insight struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Finding string `json:"finding"`
	Source  string `json:"source"`
	Data    any    `json:"data"`
}
