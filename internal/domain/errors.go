package domain

import "errors"

// Sentinel errors. Wrap them with fmt.Errorf("...: %w", ErrX) to add context;
// adapters use errors.Is to map them to exit codes or MCP errors.
var (
	ErrInvalid  = errors.New("invalid input")
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
	// ErrUnavailable means an external service (the embedding provider) could
	// not be reached. Callers degrade rather than fail.
	ErrUnavailable = errors.New("unavailable")
)
