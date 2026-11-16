package entities

import "encoding/json"

// InvestigationDetail é a ficha de uma investigação: o resumo do acervo e o que a ingestão leu do PSI.
type InvestigationDetail struct {
	Investigation *Investigation    `json:"investigation"`
	Objective     string            `json:"objective"`
	Approach      string            `json:"approach"`
	Hypothesis    string            `json:"hypothesis"`
	Impacts       string            `json:"impacts"`
	DOI           string            `json:"doi"`
	Publications  []Publication     `json:"publications"`
	Documents     []DocumentSummary `json:"documents"`
}

type Publication struct {
	Title   string `json:"title"`
	Authors string `json:"authors"`
	DOI     string `json:"doi"`
}

type DocumentSummary struct {
	File     string `json:"file"`
	Title    string `json:"title"`
	Category string `json:"category"`
	Chunks   int    `json:"chunks"`
	Pages    *int   `json:"pages"`
}

// TestDetail é um teste com as métricas e a linha original da tabela do PSI.
type TestDetail struct {
	Test          *Test           `json:"test"`
	Investigation string          `json:"investigation"`
	Metrics       json.RawMessage `json:"metrics"`
	Raw           json.RawMessage `json:"raw"`
	Source        string          `json:"source"`
}

// CompactTests são todos os testes em colunas + linhas, para o território 3D.
type CompactTests struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}
