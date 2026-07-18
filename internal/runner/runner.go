// Package runner abstracts execution of external commands (launchctl,
// networksetup, plutil, mihomo) behind an interface so all callers can be
// unit-tested with a scripted fake and never touch the real system.
//
// Hard rules enforced here:
//   - No shell is ever invoked. Programs and their arguments are passed as an
//     argv slice directly to exec.
//   - Each Run honors a context deadline.
//   - stdout and stderr are captured separately.
//   - Non-sensitive command identity (name + exit code) is preserved in errors;
//     callers are responsible for redacting sensitive argv before logging.
package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Result holds the captured output of a command. ExitCode is -1 when the
// process was never started (e.g. binary not found).
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// StdoutString returns stdout trimmed of surrounding whitespace.
func (r Result) StdoutString() string { return strings.TrimSpace(string(r.Stdout)) }

// StderrString returns stderr trimmed of surrounding whitespace.
func (r Result) StderrString() string { return strings.TrimSpace(string(r.Stderr)) }

// Runner executes a command by name with arguments.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (Result, error)
}

// Default returns a Runner that executes real commands via os/exec.
func Default() Runner { return &execRunner{} }

type execRunner struct{}

func (e *execRunner) Run(ctx context.Context, name string, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // argv is caller-controlled; never sh -c
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	res := Result{
		Stdout:   stdout.Bytes(),
		Stderr:   stderr.Bytes(),
		ExitCode: exitCodeOf(cmd, err),
	}
	if err != nil {
		if ctx.Err() != nil {
			return res, fmt.Errorf("%s: %w", name, ctx.Err())
		}
		return res, &ExecError{Name: name, Args: args, Result: res, Err: err}
	}
	return res, nil
}

func exitCodeOf(cmd *exec.Cmd, err error) int {
	if cmd.ProcessState == nil {
		return -1
	}
	if code := cmd.ProcessState.ExitCode(); code >= 0 {
		return code
	}
	// Signal-killed processes surface as -1; that's acceptable.
	return -1
}

// ExecError carries command identity alongside the failure.
type ExecError struct {
	Name   string
	Args   []string
	Result Result
	Err    error
}

func (e *ExecError) Error() string {
	out := fmt.Sprintf("%s exited %d", e.Name, e.Result.ExitCode)
	if stderr := e.Result.StderrString(); stderr != "" {
		out += ": " + stderr
	}
	if e.Err != nil {
		out += fmt.Sprintf(" (%v)", e.Err)
	}
	return out
}

func (e *ExecError) Unwrap() error { return e.Err }

// ExitCode extracts the process exit code from an error returned by Run.
func ExitCode(err error) int {
	var ee *ExecError
	if errors.As(err, &ee) {
		return ee.Result.ExitCode
	}
	return -1
}

// WithTimeout returns a context derived from parent that is cancelled after d,
// along with a cancel function the caller must defer.
func WithTimeout(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, d)
}
