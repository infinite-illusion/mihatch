// Package mihomo manages the Mihomo engine process lifecycle: config
// validation (-t), version probe (-v), detached background start (no shell, no
// launchd), identity verification by pid+start-time+binary path, and graceful
// stop (SIGTERM with a SIGKILL fallback).
//
// MiHatch does not supervise Mihomo: if the process crashes, it stays down and
// "mihatch status" reports degraded until the user re-runs "mihatch up".
package mihomo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"mihatch/internal/redact"
	"mihatch/internal/runner"
)

// ProcessHandle identifies a started Mihomo process.
type ProcessHandle struct {
	PID       int
	StartTime string // ps lstart string, used for identity
	Binary    string
}

// Manager is the process-lifecycle abstraction used by the app layer (and faked
// in tests, since spawning real processes is not unit-testable).
type Manager interface {
	Validate(ctx context.Context, binary, homedir, config string) error
	Version(ctx context.Context, binary string) (string, error)
	Start(binary, homedir, config, logfile string) (*ProcessHandle, error)
	IsAlive(ctx context.Context, pid int, startTime, binary string) (bool, error)
	Stop(ctx context.Context, pid int, startTime, binary string, timeout time.Duration) error
}

// Real executes real mihomo / ps / signals.
type Real struct {
	Run runner.Runner
}

// NewReal builds a Real manager backed by the given runner (for mihomo -t, -v
// and ps identity checks; Start/Stop use exec/syscall directly).
func NewReal(r runner.Runner) *Real { return &Real{Run: r} }

var versionRe = regexp.MustCompile(`Mihomo Meta (\S+)`)

// Validate runs "mihomo -t -d homedir -f config"; exit 0 means valid.
func (m *Real) Validate(ctx context.Context, binary, homedir, config string) error {
	res, err := m.Run.Run(ctx, binary, "-t", "-d", homedir, "-f", config)
	if err != nil {
		stderr := redact.Multiline(res.StderrString())
		return fmt.Errorf("config validation failed: %s", stderr)
	}
	return nil
}

// Version runs "mihomo -v" and extracts the version token (e.g. "v1.19.28").
func (m *Real) Version(ctx context.Context, binary string) (string, error) {
	res, err := m.Run.Run(ctx, binary, "-v")
	if err != nil {
		return "", fmt.Errorf("version probe failed: %w", err)
	}
	out := strings.Join([]string{res.StdoutString(), res.StderrString()}, "\n")
	if m := versionRe.FindStringSubmatch(out); len(m) == 2 {
		return m[1], nil
	}
	return "", errors.New("could not parse mihomo version output")
}

// Start launches Mihomo detached (its own process group, stdio to logfile) so it
// survives MiHatch exiting. The binary, -d homedir, and -f config are all under
// .mihatch. It returns a handle with the pid and ps-reported start time.
func (m *Real) Start(binary, homedir, config, logfile string) (*ProcessHandle, error) {
	logf, err := os.OpenFile(logfile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}
	devnull, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		_ = logf.Close()
		return nil, fmt.Errorf("open /dev/null: %w", err)
	}
	cmd := exec.Command(binary, "-d", homedir, "-f", config) //nolint:gosec // argv is internal
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.Stdin = devnull
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // detach into its own group
	if err := cmd.Start(); err != nil {
		_ = logf.Close()
		_ = devnull.Close()
		return nil, fmt.Errorf("start mihomo: %w", err)
	}
	pid := cmd.Process.Pid
	// Release so MiHatch does not wait on it; the process is reparented to
	// launchd when MiHatch exits.
	_ = cmd.Process.Release()
	_ = devnull.Close()
	_ = logf.Close()

	startTime := psStartTime(pid)
	return &ProcessHandle{PID: pid, StartTime: startTime, Binary: binary}, nil
}

// IsAlive confirms a pid is still running and matches the recorded start time
// and binary path. A recycled pid (different start time / command) reads as
// not-ours, preventing MiHatch from killing an unrelated process.
func (m *Real) IsAlive(ctx context.Context, pid int, startTime, binary string) (bool, error) {
	lstart, cmdLine, ok, err := psIdent(pid)
	if err != nil {
		return false, nil // ps unavailable -> treat as not alive
	}
	if !ok {
		return false, nil
	}
	if startTime != "" && lstart != startTime {
		return false, nil
	}
	if binary != "" && !strings.Contains(cmdLine, binary) {
		return false, nil
	}
	return true, nil
}

// Stop verifies identity, sends SIGTERM, waits up to timeout, then SIGKILL.
func (m *Real) Stop(ctx context.Context, pid int, startTime, binary string, timeout time.Duration) error {
	alive, _ := m.IsAlive(ctx, pid, startTime, binary)
	if !alive {
		return nil
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !pidExists(pid) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	if pidExists(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	return nil
}

// pidExists reports whether pid is a live process (kill 0 probe).
func pidExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}
	return true
}

// psIdent returns (lstart, command, ok, err) for a pid via ps. ok is false when
// the pid does not exist.
func psIdent(pid int) (string, string, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := runLocal(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "lstart=", "-o", "command=")
	if err != nil {
		return "", "", false, err
	}
	line := strings.TrimSpace(out)
	if line == "" {
		return "", "", false, nil
	}
	// ps prints "<lstart> <command>"; lstart is 24 chars like
	// "Thu Jul 16 10:00:00 2026". Split on first space after the timestamp.
	if len(line) >= 24 {
		lstart := strings.TrimSpace(line[:24])
		cmd := strings.TrimSpace(line[24:])
		return lstart, cmd, true, nil
	}
	return "", line, true, nil
}

// psStartTime returns just the lstart string for a pid (best-effort).
func psStartTime(pid int) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := runLocal(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "lstart=")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// runLocal runs a command via the default runner.
func runLocal(ctx context.Context, name string, args ...string) (string, error) {
	r := runner.Default()
	res, err := r.Run(ctx, name, args...)
	if err != nil {
		return "", err
	}
	return res.StdoutString(), nil
}

// --- Fake for app-layer tests ---

// FakeManager is a test Manager. Start returns a synthetic handle and records
// the call; IsAlive/Stop consult the provided AlivePIDs set.
type FakeManager struct {
	ValidateErr error
	VersionStr  string
	VersionErr  error
	StartErr    error
	AlivePIDs   map[int]bool
	StopCalls   []int

	validateCalls int
}

func (f *FakeManager) Validate(context.Context, string, string, string) error {
	return f.ValidateErr
}
func (f *FakeManager) Version(context.Context, string) (string, error) {
	return f.VersionStr, f.VersionErr
}
func (f *FakeManager) Start(binary, _, _, _ string) (*ProcessHandle, error) {
	if f.StartErr != nil {
		return nil, f.StartErr
	}
	f.validateCalls++
	pid := 40000 + f.validateCalls
	if f.AlivePIDs == nil {
		f.AlivePIDs = map[int]bool{}
	}
	f.AlivePIDs[pid] = true
	return &ProcessHandle{PID: pid, StartTime: "fake", Binary: binary}, nil
}
func (f *FakeManager) IsAlive(_ context.Context, pid int, _, _ string) (bool, error) {
	return f.AlivePIDs[pid], nil
}
func (f *FakeManager) Stop(_ context.Context, pid int, _, _ string, _ time.Duration) error {
	f.StopCalls = append(f.StopCalls, pid)
	delete(f.AlivePIDs, pid)
	return nil
}

// Ensure interface compliance.
var _ Manager = (*Real)(nil)
var _ Manager = (*FakeManager)(nil)
