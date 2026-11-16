package ask

import (
	"fmt"

	"atlas-api/domain/entities"
)

// renumber troca [T1]/[D2] por [1], [2]... na ordem em que aparecem e devolve só as citações usadas.
func renumber(answer string, all []entities.Citation) (string, []entities.Citation) {
	byRef := map[string]entities.Citation{}
	for _, c := range all {
		byRef[c.Ref] = c
	}
	order := map[string]int{}
	var used []entities.Citation
	out := reMark.ReplaceAllStringFunc(answer, func(m string) string {
		ref := m[1 : len(m)-1]
		c, ok := byRef[ref]
		if !ok {
			return ""
		}
		if _, seen := order[ref]; !seen {
			order[ref] = len(used) + 1
			c.N = order[ref]
			used = append(used, c)
		}
		return fmt.Sprintf("[%d]", order[ref])
	})
	if used == nil {
		used = []entities.Citation{}
	}
	return out, used
}
