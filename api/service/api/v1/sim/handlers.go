// Package sim tem os handlers do simulador de chama 3D: configuração e validação.
package sim

import (
	"errors"
	"io/fs"
	"log"
	"net/http"

	"atlas-api/extensions/zweb"
)

type ConfigUseCase interface {
	Get() (map[string]any, error)
}

type ValidationUseCase interface {
	Get() map[string]any
}

// ConfigHandler: GET /api/sim/config — 404 sem arquivo (o simulador usa o padrão embutido).
func ConfigHandler(uc ConfigUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, err := uc.Get()
		if errors.Is(err, fs.ErrNotExist) {
			zweb.Error(w, 404, "simulator configuration not found")
			return
		}
		if err != nil {
			log.Printf("sim/config: %v", err) // o erro traz o caminho do arquivo no servidor
			zweb.Error(w, 500, "the simulator configuration could not be read")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		zweb.JSON(w, 200, cfg)
	}
}

// ValidationHandler: GET /api/sim/validation — o simulador comparado com a curva da NASA.
func ValidationHandler(uc ValidationUseCase) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { zweb.JSON(w, 200, uc.Get()) }
}
