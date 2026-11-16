package datasets

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"atlas-api/domain/entities"
)

func filterRecords(recs []entities.Record, key, value string) []entities.Record {
	if key == "" || value == "" {
		return recs
	}
	out := make([]entities.Record, 0, len(recs))
	for _, r := range recs {
		if v := r.Cat[key]; v != nil && *v == value {
			out = append(out, r)
		}
	}
	return out
}

func hasVar(defs []entities.VarDef, key string) bool {
	for _, v := range defs {
		if v.Key == key {
			return true
		}
	}
	return false
}

// rótulo de número no padrão pt-BR, até duas casas (o mesmo das classes do histograma no COSMOEX)
func fmtBR(v float64) string {
	s := strconv.FormatFloat(math.Round(v*100)/100, 'f', -1, 64)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	intPart, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, c := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	out := b.String()
	if frac != "" {
		out += "." + frac
	}
	if neg {
		out = "-" + out
	}
	return out
}

// ordem estável de primeira aparição, como o Map do JavaScript que o COSMOEX usava
type orderedGroups[T any] struct {
	keys []string
	vals map[string][]T
}

func (g *orderedGroups[T]) add(k string, v T) {
	if g.vals == nil {
		g.vals = map[string][]T{}
	}
	if _, ok := g.vals[k]; !ok {
		g.keys = append(g.keys, k)
	}
	g.vals[k] = append(g.vals[k], v)
}

// BuildChart filtra, agrupa e calcula o gráfico pedido.
func BuildChart(ds entities.Dataset, all []entities.Record, cfg entities.ChartConfig) entities.BuiltChart {
	rows := filterRecords(all, cfg.FilterKey, cfg.FilterValue)
	hasGroup := hasVar(ds.Categorical, cfg.Group)
	switch cfg.Type {
	case "scatter":
		var g orderedGroups[entities.ChartPoint]
		missing := 0
		for _, r := range rows {
			x, y := r.Num[cfg.X], r.Num[cfg.Y]
			if x == nil || y == nil {
				missing++
				continue
			}
			k := "All"
			if hasGroup {
				k = "Missing"
				if v := r.Cat[cfg.Group]; v != nil {
					k = *v
				}
			}
			g.add(k, entities.ChartPoint{X: *x, Y: *y, ID: r.ID})
		}
		series := make([]entities.ChartSeries, 0, len(g.keys))
		for _, k := range g.keys {
			series = append(series, entities.ChartSeries{Name: k, Points: g.vals[k]})
		}
		return entities.BuiltChart{Kind: "scatter", Series: series, Missing: missing, Used: len(rows) - missing}
	case "bar":
		gk := cfg.Group
		if !hasGroup {
			gk = ""
			if len(ds.Categorical) > 0 {
				gk = ds.Categorical[0].Key
			}
		}
		var g orderedGroups[float64]
		missing := 0
		for _, r := range rows {
			y := r.Num[cfg.Y]
			if y == nil {
				missing++
				continue
			}
			k := "Missing"
			if gk != "" {
				if v := r.Cat[gk]; v != nil {
					k = *v
				}
			}
			g.add(k, *y)
		}
		bars := make([]entities.ChartBar, 0, len(g.keys))
		for _, k := range g.keys {
			v := g.vals[k]
			sum, lo, hi := 0.0, math.Inf(1), math.Inf(-1)
			for _, x := range v {
				sum += x
				lo, hi = math.Min(lo, x), math.Max(hi, x)
			}
			mean := math.Round(sum/float64(len(v))*1000) / 1000
			n := len(v)
			bars = append(bars, entities.ChartBar{Name: k, Mean: &mean, N: &n, Min: &lo, Max: &hi})
		}
		sort.SliceStable(bars, func(i, j int) bool { return *bars[i].N > *bars[j].N })
		if len(bars) > 14 {
			bars = bars[:14]
		}
		return entities.BuiltChart{Kind: "bar", Bars: bars, Missing: missing, Used: len(rows) - missing}
	default: // histogram
		vals := make([]float64, 0, len(rows))
		for _, r := range rows {
			if v := r.Num[cfg.Y]; v != nil {
				vals = append(vals, *v)
			}
		}
		missing := len(rows) - len(vals)
		if len(vals) == 0 {
			return entities.BuiltChart{Kind: "histogram", Bars: []entities.ChartBar{}, Missing: missing}
		}
		lo, hi := math.Inf(1), math.Inf(-1)
		for _, v := range vals {
			lo, hi = math.Min(lo, v), math.Max(hi, v)
		}
		n := cfg.Bins
		if n < 1 {
			n = 1
		}
		if n > 50 {
			n = 50
		}
		w := (hi - lo) / float64(n)
		if w == 0 {
			w = 1
		}
		counts := make([]int, n)
		for _, v := range vals {
			i := int(math.Floor((v - lo) / w))
			if i > n-1 {
				i = n - 1
			}
			counts[i]++
		}
		bars := make([]entities.ChartBar, n)
		for i := range bars {
			c := counts[i]
			bars[i] = entities.ChartBar{Name: fmtBR(lo+float64(i)*w) + "–" + fmtBR(lo+float64(i+1)*w), Count: &c}
		}
		return entities.BuiltChart{Kind: "histogram", Bars: bars, Missing: missing, Used: len(vals)}
	}
}

// valores distintos de uma variável categórica no conjunto inteiro (opções do filtro)
func categoryValues(recs []entities.Record, key string) []string {
	if key == "" {
		return []string{}
	}
	seen := map[string]bool{}
	out := []string{}
	for _, r := range recs {
		if v := r.Cat[key]; v != nil && *v != "" && !seen[*v] {
			seen[*v] = true
			out = append(out, *v)
		}
	}
	sort.Strings(out)
	return out
}
