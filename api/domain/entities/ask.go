package entities

// AskIn é uma pergunta em linguagem natural sobre o cenário da tela.
type AskIn struct {
	Question     string    `json:"question"`
	Scenario     *Scenario `json:"scenario"`
	Conversation string    `json:"conversation_id"`
}

// Citation é uma fonte numerada citada na resposta.
type Citation struct {
	N        int      `json:"n"`
	Ref      string   `json:"ref"`  // T1, D2...
	Kind     string   `json:"kind"` // teste | documento | catalogo | recurso | externo
	Title    string   `json:"title"`
	Where    string   `json:"where"` // arquivo e linha, ou página
	Inv      string   `json:"investigation_id,omitempty"`
	Test     string   `json:"test_id,omitempty"`
	URL      string   `json:"url,omitempty"`
	Text     string   `json:"text"`
	Note     string   `json:"note,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// AskOut é a resposta com veredito, citações e de onde veio o texto.
type AskOut struct {
	Answer          string         `json:"answer"`
	Citations       []Citation     `json:"citations"`
	Scenario        Scenario       `json:"scenario"`
	ScenarioChanged bool           `json:"scenario_changed"`
	Understood      []string       `json:"understood"`
	Verdict         Verdict        `json:"verdict"`
	Ranking         []Ranked       `json:"ranking"`
	Mode            string         `json:"mode"` // rag | si | extrativo
	Conversation    string         `json:"conversation_id,omitempty"`
	Provider        string         `json:"provider"`
	Model           string         `json:"model"`
	Sources         map[string]int `json:"sources"`
	Abstained       bool           `json:"abstained"`
	TopSimilarity   float64        `json:"top_similarity"`
	Warning         string         `json:"warning,omitempty"`
	TookMs          int64          `json:"took_ms"`
}

// AskContext é o primeiro evento do streaming: cenário entendido e veredito, antes do texto.
type AskContext struct {
	Scenario        Scenario `json:"scenario"`
	ScenarioChanged bool     `json:"scenario_changed"`
	Understood      []string `json:"understood"`
	Verdict         Verdict  `json:"verdict"`
}

// Hit é um trecho recuperado, pronto para virar citação.
type Hit struct {
	ID       string  `json:"id"`
	Origin   string  `json:"origin"` // atlas (pgvector)
	Kind     string  `json:"kind"`   // catalogo | documento | recurso | teste | externo
	Title    string  `json:"title"`
	Category string  `json:"category"`
	Inv      *string `json:"investigation_id"`
	TestID   *string `json:"test_id"`
	File     string  `json:"source_file"`
	Page     *int    `json:"page"`
	URL      string  `json:"url,omitempty"`
	Text     string  `json:"text"`
	Score    float64 `json:"score"`
	Sim      float64 `json:"similarity"` // cosseno com a pergunta (só nos trechos do Atlas)
}

// RagStatus é o estado do RAG da equipe (rag-Carlin).
type RagStatus struct {
	Enabled bool   `json:"enabled"`
	Ready   bool   `json:"ready"`
	Chunks  int    `json:"chunks"`
	Detail  string `json:"detail,omitempty"`
}

// RagSource é uma fonte devolvida pelo RAG da equipe.
type RagSource struct {
	Citation          int      `json:"citation"`
	DocumentID        string   `json:"document_id"`
	Title             string   `json:"title"`
	Experiment        string   `json:"experiment"`
	Page              *int     `json:"page"`
	Row               *int     `json:"row"`
	SourceType        string   `json:"source_type"`
	Source            string   `json:"source"`
	URL               string   `json:"url"`
	Cited             *bool    `json:"cited"`
	RetrievalMethod   string   `json:"retrieval_method"`
	FormulaValidation string   `json:"formula_validation"`
	Excerpt           string   `json:"excerpt"`
	DataWarnings      []string `json:"data_warnings"`
}

// RagAnswer é a resposta validada do RAG: texto, fontes e a conversa em que ela ficou.
type RagAnswer struct {
	Content      string
	Sources      []RagSource
	Conversation string
}
