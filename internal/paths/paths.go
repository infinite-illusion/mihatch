// Package paths resolves every on-disk location MiHatch uses.
//
// Everything MiHatch owns lives inside a single project-local directory
// <root>/.mihatch/, where <root> is the project directory (the current working
// directory by default, overridable via --root or MIHATCH_ROOT). MiHatch never
// writes to ~/Library/Application Support, ~/Library/Logs, ~/Library/LaunchAgents,
// or ~/.config. Deleting the project directory after "mihatch down" is a full
// cleanup.
//
// The Clash Verge Rev production App ID and runtime filename are constants with
// test coverage; the dev App ID is enumerated explicitly so it can never be
// selected by accident.
package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Identity and layout constants.
const (
	// DotDirName is the single directory MiHatch owns inside the project root.
	DotDirName = ".mihatch"

	// MihomoBinName is the installed engine executable name.
	MihomoBinName = "mihomo"

	// ConfigFileName is the purified Mihomo runtime config.
	ConfigFileName = "config.yaml"

	// StateFileName holds pid/ownership/runtime facts.
	StateFileName = "state.json"

	// LogFileName captures mihomo stdout+stderr.
	LogFileName = "mihomo.log"

	// LockFileName is the advisory process lock.
	LockFileName = "mihatch.lock"

	// ProvidersDir holds migrated/copied provider caches (proxy + rule).
	ProvidersDir = "providers"

	// RulesDir holds migrated rule-provider caches.
	RulesDir = "rules"

	// TempDirName holds transient download/scratch files, removed after use.
	TempDirName = ".tmp"

	// DefaultMixedPort is the loopback mixed proxy port reserved by MiHatch.
	// It intentionally differs from Clash Verge Rev's defaults.
	DefaultMixedPort = 17890

	// ProxyHost is the loopback address the mixed proxy binds.
	ProxyHost = "127.0.0.1"
)

// Clash Verge Rev identity constants.
//
// Only the *production* App ID is a recognized import source. The dev App ID is
// a strict string-prefix extension of the prod id (prod + ".dev"), so any
// prefix-based selection would wrongly match the dev directory. Selection is
// therefore exact-equality only; tests assert this.
const (
	// CVRProdAppID is the production Clash Verge Rev application identifier.
	CVRProdAppID = "io.github.clash-verge-rev.clash-verge-rev"

	// CVRDevAppID is the development Clash Verge Rev identifier; never imported.
	CVRDevAppID = "io.github.clash-verge-rev.clash-verge-rev.dev"

	// CVRRuntimeFile is the filename of Clash Verge Rev's final runtime YAML.
	CVRRuntimeFile = "clash-verge.yaml"
)

// Paths holds the resolved project root and derives every other path from it.
type Paths struct {
	Root string
}

// New returns Paths rooted at the given (absolute) project root.
func New(root string) *Paths { return &Paths{Root: root} }

// ResolveRoot determines the project root from, in order: an explicit override,
// the MIHATCH_ROOT environment variable, or the current working directory. The
// result is made absolute.
func ResolveRoot(explicit string) (string, error) {
	root := explicit
	if root == "" {
		root = os.Getenv("MIHATCH_ROOT")
	}
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("determine working directory: %w", err)
		}
		root = cwd
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	if fi, err := os.Stat(abs); err == nil && !fi.IsDir() {
		return "", fmt.Errorf("root %s is not a directory", abs)
	}
	return abs, nil
}

// DotDir returns <root>/.mihatch.
func (p *Paths) DotDir() string { return filepath.Join(p.Root, DotDirName) }

// Binary returns <root>/.mihatch/mihomo.
func (p *Paths) Binary() string { return filepath.Join(p.DotDir(), MihomoBinName) }

// ConfigFile returns <root>/.mihatch/config.yaml.
func (p *Paths) ConfigFile() string { return filepath.Join(p.DotDir(), ConfigFileName) }

// StateFile returns <root>/.mihatch/state.json.
func (p *Paths) StateFile() string { return filepath.Join(p.DotDir(), StateFileName) }

// LogFile returns <root>/.mihatch/mihomo.log.
func (p *Paths) LogFile() string { return filepath.Join(p.DotDir(), LogFileName) }

// LockFile returns <root>/.mihatch/mihatch.lock.
func (p *Paths) LockFile() string { return filepath.Join(p.DotDir(), LockFileName) }

// ProvidersDir returns <root>/.mihatch/providers.
func (p *Paths) ProvidersDir() string { return filepath.Join(p.DotDir(), ProvidersDir) }

// RulesDir returns <root>/.mihatch/rules.
func (p *Paths) RulesDir() string { return filepath.Join(p.DotDir(), RulesDir) }

// TempDir returns <root>/.mihatch/.tmp (created on demand, removed after use).
func (p *Paths) TempDir() string { return filepath.Join(p.DotDir(), TempDirName) }

// EnsureDirs creates the owned directories with secure modes. The .mihatch
// tree is 0700; the providers/rules subdirs likewise.
func (p *Paths) EnsureDirs() error {
	dirs := []struct {
		path string
		mode os.FileMode
	}{
		{p.DotDir(), 0o700},
		{p.ProvidersDir(), 0o700},
		{p.RulesDir(), 0o700},
		{p.TempDir(), 0o700},
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d.path, d.mode); err != nil {
			return fmt.Errorf("create %s: %w", d.path, err)
		}
		if err := os.Chmod(d.path, d.mode); err != nil {
			return fmt.Errorf("chmod %s: %w", d.path, err)
		}
	}
	return nil
}

// RemoveTempDir removes the scratch directory if present.
func (p *Paths) RemoveTempDir() error {
	if err := os.RemoveAll(p.TempDir()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// --- Clash Verge Rev (read-only import source) ---

// CVRProdDataDir returns the production Clash Verge Rev data directory, which
// is the HomeDir (-d) CVR passes to Mihomo. Used to resolve relative provider
// paths when copying referenced assets. Pure function.
func CVRProdDataDir(userConfigDir string) string {
	return filepath.Join(userConfigDir, CVRProdAppID)
}

// CVRProdRuntimeFile returns the absolute path to the production runtime YAML.
// Pure function; uses the prod App ID only.
func CVRProdRuntimeFile(userConfigDir string) string {
	return filepath.Join(CVRProdDataDir(userConfigDir), CVRRuntimeFile)
}

// UserConfigDir returns the macOS Application Support directory via the stdlib
// API (never hardcoded). On darwin this is ~/Library/Application Support.
func UserConfigDir() (string, error) {
	d, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve Application Support: %w", err)
	}
	if d == "" {
		return "", errors.New("Application Support directory is empty")
	}
	return d, nil
}

// IsCVRProdAppID reports whether an identifier is the prod App ID (exact).
func IsCVRProdAppID(id string) bool { return id == CVRProdAppID }

// IsCVRDevAppID reports whether an identifier is the dev App ID (exact).
func IsCVRDevAppID(id string) bool { return id == CVRDevAppID }
