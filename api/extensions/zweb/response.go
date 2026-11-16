// Package zweb reúne o que todo handler HTTP usa: ler JSON, responder JSON, erro padronizado, CORS e SSE.
package zweb

import (
	"encoding/json"
	"log"
	"net/http"
	"runtime/debug"
)

// JSON responde com o valor em JSON e o código dado. O corpo é montado antes do cabeçalho: um valor que não vira
// JSON (NaN, Inf) sai como 500 com a causa, e não como um 200 de corpo vazio.
func JSON(w http.ResponseWriter, code int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		log.Printf("zweb: resposta não serializável: %v", err)
		code = http.StatusInternalServerError
		b, _ = json.Marshal(map[string]string{"error": "the server could not encode this answer"})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write(append(b, '\n'))
}

// Error responde {"error": msg}, o formato de erro que a interface lê.
func Error(w http.ResponseWriter, code int, msg string) {
	JSON(w, code, map[string]any{"error": msg})
}

// Recover transforma um pânico de handler em 500 no formato de erro da API e registra a pilha. Sem isto o
// net/http só corta a conexão, e a interface não tem o que mostrar.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler { // pedido abortado de propósito: segue o tratamento do net/http
				panic(v)
			}
			log.Printf("zweb: pânico em %s %s: %v\n%s", r.Method, r.URL.Path, v, debug.Stack())
			Error(w, http.StatusInternalServerError, "the server hit an unexpected error")
		}()
		next.ServeHTTP(w, r)
	})
}

// DecodeJSON lê o corpo como JSON, com limite de 1 MB.
func DecodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v)
}
