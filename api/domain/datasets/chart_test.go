package datasets

import (
	"testing"

	"atlas-api/domain/entities"
)

func f(v float64) *float64 { return &v }
func s(v string) *string   { return &v }

func chartFixture() (entities.Dataset, []entities.Record) {
	ds := entities.Dataset{ID: "t", Numeric: []entities.VarDef{{Key: "o2_pct"}, {Key: "k"}}, Categorical: []entities.VarDef{{Key: "fuel"}}}
	recs := []entities.Record{
		{ID: "a", Num: map[string]*float64{"o2_pct": f(21), "k": f(1)}, Cat: map[string]*string{"fuel": s("hept")}},
		{ID: "b", Num: map[string]*float64{"o2_pct": f(30), "k": f(3)}, Cat: map[string]*string{"fuel": s("hept")}},
		{ID: "c", Num: map[string]*float64{"o2_pct": f(25), "k": nil}, Cat: map[string]*string{"fuel": s("meth")}},
		{ID: "d", Num: map[string]*float64{"o2_pct": f(40), "k": f(2)}, Cat: map[string]*string{"fuel": nil}},
	}
	return ds, recs
}

func TestChartScatterCountsMissingAndGroups(t *testing.T) {
	ds, recs := chartFixture()
	c := BuildChart(ds, recs, entities.ChartConfig{Type: "scatter", X: "o2_pct", Y: "k", Group: "fuel"})
	if c.Missing != 1 || c.Used != 3 {
		t.Fatalf("missing/used = %d/%d", c.Missing, c.Used)
	}
	if len(c.Series) != 2 || c.Series[0].Name != "hept" || c.Series[1].Name != "Missing" {
		t.Fatalf("series = %+v", c.Series)
	}
}

func TestChartBarMeanAndFilter(t *testing.T) {
	ds, recs := chartFixture()
	c := BuildChart(ds, recs, entities.ChartConfig{Type: "bar", Y: "k", Group: "fuel", FilterKey: "fuel", FilterValue: "hept"})
	if len(c.Bars) != 1 || *c.Bars[0].Mean != 2 || *c.Bars[0].N != 2 || *c.Bars[0].Min != 1 || *c.Bars[0].Max != 3 {
		t.Fatalf("bars = %+v", c.Bars)
	}
}

func TestChartHistogramBins(t *testing.T) {
	ds, recs := chartFixture()
	c := BuildChart(ds, recs, entities.ChartConfig{Type: "histogram", Y: "o2_pct", Bins: 2})
	if len(c.Bars) != 2 || *c.Bars[0].Count != 3 || *c.Bars[1].Count != 1 || c.Bars[0].Name != "21–30.5" {
		t.Fatalf("bars = %+v", c.Bars)
	}
	if fmtBR(1234.5) != "1,234.5" {
		t.Fatalf("fmtBR = %s", fmtBR(1234.5))
	}
}
