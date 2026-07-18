// Package app implements MiHatch's command logic: init, sync, up, status,
// pause, resume, down, logs. The CLI layer is a thin parser that delegates here.
//
// All mutating flows acquire a process lock for their duration. System-proxy
// takeover always uses compare-before-restore so a third party (e.g. Clash Verge
// Rev dev) that changed settings after takeover is never clobbered.
package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"mihatch/internal/download"
	"mihatch/internal/health"
	"mihatch/internal/lock"
	"mihatch/internal/mihomo"
	"mihatch/internal/paths"
	"mihatch/internal/proxy"
	"mihatch/internal/redact"
	"mihatch/internal/runner"
	"mihatch/internal/state"
)

// Source type values recorded in state.json.
const (
	SourceClashVergeRev = "clash-verge-rev"
	SourceFile          = "file"
)

// App holds injectable dependencies and executes command flows.
type App struct {
	Paths         *paths.Paths
	Runner        runner.Runner
	Mihomo        mihomo.Manager
	Health        health.Prober
	UserConfigDir string
	Clock         func() time.Time
	Out           io.Writer
	Err           io.Writer
	Verbose       bool

	// FetchLatest returns the latest stable Mihomo release. Injectable for tests.
	FetchLatest func(ctx context.Context) (download.Release, error)
	// DownloadAsset downloads a release asset URL to dest, reporting progress to
	// the given writer (nil = silent). Injectable for tests.
	DownloadAsset func(ctx context.Context, url, dest string, progress io.Writer) (int64, error)
}

// Default builds an App wired to real implementations for the given resolved
// project root. The CLI injects its own writers/flags afterwards.
func Default(root string, w io.Writer) (*App, error) {
	p := paths.New(root)
	ucd, err := paths.UserConfigDir()
	if err != nil {
		return nil, err
	}
	r := runner.Default()
	dl := download.NewClient("mihatch")
	return &App{
		Paths:         p,
		Runner:        r,
		Mihomo:        mihomo.NewReal(r),
		Health:        health.NewReal(),
		UserConfigDir: ucd,
		Clock:         time.Now,
		Out:           w,
		Err:           w,
		FetchLatest:   dl.LatestStable,
		DownloadAsset: func(ctx context.Context, url, dest string, progress io.Writer) (int64, error) {
			return dl.DownloadToFile(ctx, url, dest, progress)
		},
	}, nil
}

// ensurePlatform asserts darwin/arm64.
func ensurePlatform() error {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return os.ErrInvalid
	}
	return nil
}

// now returns an ISO timestamp from the injected clock.
func (a *App) now() string {
	if a.Clock == nil {
		return ""
	}
	return a.Clock().UTC().Format(time.RFC3339)
}

// acquireLock takes the process lock and returns a release function.
func (a *App) acquireLock(ctx context.Context) (func(), error) {
	lk, err := lock.Acquire(ctx, a.Paths.LockFile())
	if err != nil {
		return nil, err
	}
	return func() { _ = lk.Release() }, nil
}

func (a *App) loadState() (*state.Persisted, error) {
	return state.Load(a.Paths.StateFile())
}

func (a *App) saveState(p *state.Persisted) error {
	return p.Save(a.Paths.StateFile())
}

// acquirer builds a proxy.Acquirer with MiHatch defaults.
func (a *App) acquirer() *proxy.Acquirer {
	return &proxy.Acquirer{
		Client:      proxy.NewClient(a.Runner),
		Host:        paths.ProxyHost,
		Port:        paths.DefaultMixedPort,
		Bypass:      proxy.DefaultBypassDomains,
		EnableHTTP:  true,
		EnableHTTPS: true,
		EnableSOCKS: true,
	}
}

// resolveServices resolves target network services (auto via default route, or
// an explicit list).
func (a *App) resolveServices(ctx context.Context, explicit []string) ([]string, error) {
	return proxy.NewClient(a.Runner).ResolveServices(ctx, explicit)
}

// readSource returns the raw source YAML, the source HomeDir to resolve relative
// provider paths against, and the redacted display path.
func (a *App) readSource(src state.SourceRef) ([]byte, string, string, error) {
	switch src.Type {
	case SourceFile, "":
		if src.Path == "" {
			return nil, "", "", os.ErrNotExist
		}
		b, err := os.ReadFile(src.Path)
		if err != nil {
			return nil, "", "", err
		}
		return b, filepath.Dir(src.Path), src.Path, nil
	case SourceClashVergeRev:
		runtimePath := paths.CVRProdRuntimeFile(a.UserConfigDir)
		b, err := os.ReadFile(runtimePath)
		if err != nil {
			return nil, "", "", err
		}
		// Sanity: the file must live inside the prod App ID dir, never the dev one.
		if !paths.IsCVRProdAppID(extractAppID(runtimePath, a.UserConfigDir)) {
			return nil, "", "", os.ErrInvalid
		}
		display := redact.Path(runtimePath, homeDir())
		return b, paths.CVRProdDataDir(a.UserConfigDir), display, nil
	}
	return nil, "", "", os.ErrInvalid
}

// extractAppID returns the App ID segment of a CVR path, or "" if not under a
// CVR data dir.
func extractAppID(p, userConfigDir string) string {
	base := filepath.Dir(p)
	rel, err := filepath.Rel(userConfigDir, base)
	if err != nil {
		return ""
	}
	if seg := filepath.Base(rel); filepath.Dir(rel) == "." {
		return seg
	}
	return ""
}

// homeDir returns the current home directory (for redaction), best-effort.
func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

// waitHealthy polls the mixed port and a proxied connectivity check until both
// pass or the deadline expires.
func (a *App) waitHealthy(ctx context.Context, port int, deadline time.Duration) bool {
	deadlineAt := time.Now().Add(deadline)
	for time.Now().Before(deadlineAt) {
		select {
		case <-ctx.Done():
			return false
		default:
		}
		if a.Health.PortListening(ctx, port) && a.Health.ProxyOK(ctx, port, nil, 6*time.Second).OK {
			return true
		}
		time.Sleep(400 * time.Millisecond)
	}
	return false
}
