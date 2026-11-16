package ask

import (
	"fmt"
	"strings"

	"atlas-api/domain/entities"
	"atlas-api/domain/ptbr"
)

// ragCitations transforma as fontes do RAG em citações; se alguma foi citada, só as citadas entram.
func ragCitations(sources []entities.RagSource) []entities.Citation {
	anyCited := false
	for _, s := range sources {
		if s.Cited != nil && *s.Cited {
			anyCited = true
		}
	}
	out := []entities.Citation{}
	for _, s := range sources {
		if anyCited && (s.Cited == nil || !*s.Cited) {
			continue
		}
		where := s.SourceType
		if s.Page != nil {
			where += fmt.Sprintf(", p. %d", *s.Page)
		}
		if s.Row != nil {
			where += fmt.Sprintf(", row %d", *s.Row)
		}
		title := s.Title
		if s.Experiment != "" && !strings.Contains(strings.ToLower(title), strings.ToLower(s.Experiment)) {
			title = s.Experiment + " · " + title
		}
		c := entities.Citation{N: s.Citation, Ref: fmt.Sprintf("R%d", s.Citation), Kind: "documento", Title: title, Where: strings.TrimPrefix(where, ", "),
			URL: s.URL, Text: ptbr.Shorten(strings.Join(strings.Fields(s.Excerpt), " "), 620), Warnings: s.DataWarnings}
		switch s.FormulaValidation {
		case "":
		case "machine_transcription_unreviewed":
			c.Note = "Formula transcribed automatically, not reviewed; check against the original page"
		case "visual_transcription_available":
			c.Note = "Formula checked against the PDF image"
		default:
			c.Note = "Formula: " + s.FormulaValidation
		}
		if s.RetrievalMethod == "conditions_table" {
			c.Note = "Conditions table extracted automatically; check against the original"
		}
		out = append(out, c)
	}
	return out
}
