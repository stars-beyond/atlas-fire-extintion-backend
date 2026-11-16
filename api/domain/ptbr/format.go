// Package ptbr formata números e rótulos como a interface mostra (pt-BR).
package ptbr

import (
	"fmt"
	"math"
	"strings"

	"atlas-api/domain/entities"
)

// Num escreve v com dec casas e vírgula decimal.
func Num(v float64, dec int) string {
	return fmt.Sprintf("%.*f", dec, v)
}

// Pct escreve uma fração como porcentagem ("21%", "30,5%").
func Pct(f float64) string {
	v := f * 100
	if math.Abs(v-math.Round(v)) < 0.05 {
		return fmt.Sprintf("%.0f%%", v)
	}
	return Num(v, 1) + "%"
}

// Psi escreve uma pressão em atm como psi.
func Psi(atm float64) string { return Num(atm*14.696, 1) + " psi" }

// GLabel dá nome à gravidade: 0 g, 1 g, lunar, marciana ou o número.
func GLabel(g float64) string {
	switch {
	case g <= entities.TolG:
		return "0 g"
	case g >= 1-entities.TolG:
		return "1 g"
	case math.Abs(g-0.165) < 0.01:
		return "lunar gravity (0.17 g)"
	case math.Abs(g-0.378) < 0.01:
		return "Mars gravity (0.38 g)"
	}
	return Num(g, 2) + " g"
}

// Plural escreve "1 teste" ou "3 testes".
func Plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// SignedPP escreve uma diferença em pontos percentuais com sinal ("+2,0 pontos").
func SignedPP(v float64) string {
	s := "+"
	if v < 0 {
		s = "−"
	}
	return s + Num(math.Abs(v), 1) + " points"
}

// SignedPct escreve uma diferença relativa com sinal ("−30%").
func SignedPct(v float64) string {
	s := "+"
	if v < 0 {
		s = "−"
	}
	return s + Num(math.Abs(v), 0) + "%"
}

// Shorten corta o texto em n caracteres com reticências.
func Shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// Round3 arredonda em três casas.
func Round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// UpperFirst põe a primeira letra em maiúscula.
func UpperFirst(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

// LowerFirst põe a primeira letra em minúscula.
func LowerFirst(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	// siglas ("NASA", "PSI", "ISS") ficam como estão
	if len(r) > 1 && strings.ToUpper(string(r[1])) == string(r[1]) && strings.ToLower(string(r[1])) != string(r[1]) {
		return s
	}
	return strings.ToLower(string(r[0])) + string(r[1:])
}

// JoinList junta itens como em português: "a", "a e b", "a, b e c".
func JoinList(list []string) string {
	switch len(list) {
	case 0:
		return ""
	case 1:
		return list[0]
	}
	s := list[0]
	for _, x := range list[1 : len(list)-1] {
		s += ", " + x
	}
	return s + " and " + list[len(list)-1]
}
