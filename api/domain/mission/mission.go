// Package mission é o motor de missão (Mission Fire Engine) visto pela API: as missões de referência e a
// simulação de uma missão hora a hora. O cálculo inteiro roda no serviço Python (brain/atlas/mission); aqui só se
// pede e se repassa, sem mexer nos números.
package mission

import (
	"context"
	"encoding/json"
	"errors"
)

// Engine roda o motor.
type Engine interface {
	Presets(ctx context.Context) (json.RawMessage, error)
	Simulate(ctx context.Context, cfg json.RawMessage) (json.RawMessage, error)
}

// ErrUnavailable: o motor não respondeu (serviço fora do ar ou não configurado).
var ErrUnavailable = errors.New("the mission engine is unavailable")

// InvalidError: o motor recusou a configuração; Message é o motivo dele, Detail o erro de validação completo.
type InvalidError struct {
	Message string
	Detail  json.RawMessage
}

func (e *InvalidError) Error() string { return e.Message }

// GetPresetsUseCase devolve as missões de referência e as opções do motor.
type GetPresetsUseCase struct {
	Engine Engine
}

func (uc *GetPresetsUseCase) Get(ctx context.Context) (json.RawMessage, error) {
	if uc.Engine == nil {
		return nil, ErrUnavailable
	}
	return uc.Engine.Presets(ctx)
}

// SimulateUseCase roda uma missão: cfg é a configuração como o motor a define (MissionConfig).
type SimulateUseCase struct {
	Engine Engine
}

func (uc *SimulateUseCase) Simulate(ctx context.Context, cfg json.RawMessage) (json.RawMessage, error) {
	if uc.Engine == nil {
		return nil, ErrUnavailable
	}
	if len(cfg) == 0 || cfg[0] != '{' {
		return nil, &InvalidError{Message: "the mission configuration must be a JSON object"}
	}
	return uc.Engine.Simulate(ctx, cfg)
}
