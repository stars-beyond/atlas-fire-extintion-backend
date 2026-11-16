package entities

import "errors"

// ErrNotFound marca recurso inexistente (investigação, teste, conjunto de dados). O handler devolve 404.
var ErrNotFound = errors.New("not found")
