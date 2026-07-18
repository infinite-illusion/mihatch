// Package exit defines MiHatch's stable process exit codes and the ExitError
// type that carries them. It is dependency-free so both the app layer and the
// CLI layer can import it without creating an import cycle.
//
// Exit codes are part of the stable contract documented in the README; their
// numeric values must not change between releases without a major version bump.
package exit

import (
	"errors"
	"fmt"
)

const (
	CodeOK            = 0 // success
	CodeGeneral       = 1 // general/unspecified error
	CodeConfig        = 2 // argument or configuration error
	CodeUninitialized = 3 // MiHatch is not initialized
	CodeUnhealthy     = 4 // core is not healthy
	CodeConflict      = 5 // port conflict
	CodeDrifted       = 6 // system proxy ownership drifted; not restored for safety
	CodeDownload      = 7 // download or digest verification failed
	CodeLocked        = 8 // operation blocked by another concurrent MiHatch process
)

// ExitError wraps an underlying error together with a stable exit code.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return fmt.Sprintf("mihatch: exit code %d", e.Code)
}

// Unwrap allows errors.Is/errors.As to reach the underlying cause.
func (e *ExitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// New returns an ExitError carrying the given code and cause.
func New(code int, err error) *ExitError {
	return &ExitError{Code: code, Err: err}
}

// Code extracts the documented exit code from an error. A nil error is success;
// an unclassified error defaults to CodeGeneral.
func Code(err error) int {
	if err == nil {
		return CodeOK
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return CodeGeneral
}
