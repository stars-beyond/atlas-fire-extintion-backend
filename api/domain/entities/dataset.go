package entities

// VarDef descreve uma variável de um conjunto de dados do construtor de gráficos.
type VarDef struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Unit   string `json:"unit,omitempty"`
	Column string `json:"column"` // de onde vem na tabela do PSI
	N      int    `json:"n"`      // registros com valor
}

// Dataset é um conjunto: uma investigação com tabela experimental, ou "todos".
type Dataset struct {
	ID            string   `json:"id"`
	Label         string   `json:"label"`
	Investigation string   `json:"investigation"`
	PSI           string   `json:"psi"`
	Rows          int      `json:"rows"`
	Files         []string `json:"files"`
	Numeric       []VarDef `json:"numeric"`
	Categorical   []VarDef `json:"categorical"`
}

// Record é uma linha do conjunto. Valor ausente fica nulo; nunca vira zero.
type Record struct {
	ID     string              `json:"id"`
	Label  string              `json:"label"`
	Inv    string              `json:"inv"`
	Num    map[string]*float64 `json:"num"`
	Cat    map[string]*string  `json:"cat"`
	File   string              `json:"source_file"`
	Row    int                 `json:"source_row"`
	Assume []string            `json:"assumed"`
}

// ChartConfig é o que a interface pede ao construtor de gráficos.
type ChartConfig struct {
	Dataset     string `json:"dataset"`
	Type        string `json:"type"` // scatter | bar | histogram
	X           string `json:"x"`
	Y           string `json:"y"`
	Group       string `json:"group"`
	FilterKey   string `json:"filterKey"`
	FilterValue string `json:"filterValue"`
	Bins        int    `json:"bins"`
}

type ChartPoint struct {
	X  float64 `json:"x"`
	Y  float64 `json:"y"`
	ID string  `json:"id"`
}

type ChartSeries struct {
	Name   string       `json:"name"`
	Points []ChartPoint `json:"points"`
}

type ChartBar struct {
	Name  string   `json:"name"`
	Mean  *float64 `json:"mean,omitempty"`
	N     *int     `json:"n,omitempty"`
	Min   *float64 `json:"min,omitempty"`
	Max   *float64 `json:"max,omitempty"`
	Count *int     `json:"count,omitempty"`
}

// BuiltChart é o gráfico pronto: a interface só desenha.
type BuiltChart struct {
	Kind    string        `json:"kind"`
	Series  []ChartSeries `json:"series,omitempty"`
	Bars    []ChartBar    `json:"bars,omitempty"`
	Missing int           `json:"missing"`
	Used    int           `json:"used"`
}
