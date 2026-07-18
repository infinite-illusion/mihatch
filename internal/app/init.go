package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"mihatch/internal/atomicfile"
	"mihatch/internal/download"
	"mihatch/internal/exit"
	"mihatch/internal/mihomoconfig"
	"mihatch/internal/paths"
	"mihatch/internal/redact"
	"mihatch/internal/state"
)

// InitOpts configures "mihatch init".
type InitOpts struct {
	FromPath   string // offline mihomo binary path (--from)
	ConfigPath string // alternate source config file (--config)
}

// Init sets up .mihatch: installs the engine, imports and purifies the source
// config, validates it, and writes state.json. It does NOT touch the system
// proxy or start Mihomo.
func (a *App) Init(ctx context.Context, opts InitOpts) error {
	if err := ensurePlatform(); err != nil {
		return exit.New(exit.CodeConfig, errors.New("MiHatch only supports darwin/arm64 (Apple Silicon macOS)"))
	}
	release, err := a.acquireLock(ctx)
	if err != nil {
		return exit.New(exit.CodeLocked, err)
	}
	defer release()

	if err := a.Paths.EnsureDirs(); err != nil {
		return exit.New(exit.CodeGeneral, err)
	}

	src := state.SourceRef{Type: SourceClashVergeRev}
	if opts.ConfigPath != "" {
		src = state.SourceRef{Type: SourceFile, Path: opts.ConfigPath}
	}

	fmt.Fprintln(a.Err, "Installing Mihomo engine...")
	coreVersion, sha, err := a.installBinary(ctx, opts.FromPath)
	if err != nil {
		return exit.New(exit.CodeDownload, err)
	}

	fmt.Fprintln(a.Err, "Importing and purifying source config...")
	sourceBytes, sourceHome, display, err := a.readSource(src)
	if err != nil {
		hint := a.sourceMissingHint(src, err)
		return exit.New(exit.CodeConfig, fmt.Errorf("%w\n%s", err, hint))
	}

	purified, err := a.purifyAndValidate(ctx, sourceBytes, sourceHome)
	if err != nil {
		return exit.New(exit.CodeConfig, err)
	}
	if err := installConfig(a.Paths.ConfigFile(), purified); err != nil {
		return exit.New(exit.CodeGeneral, err)
	}

	st := &state.Persisted{}
	st.MarkInitialized(a.now(), a.Paths.Binary(), sha, coreVersion, paths.DefaultMixedPort)
	finalSrc := src
	if src.Type == SourceClashVergeRev {
		finalSrc.Path = display
	}
	st.Source = finalSrc
	if err := a.saveState(st); err != nil {
		return exit.New(exit.CodeGeneral, err)
	}

	fmt.Fprintln(a.Out, "MiHatch initialized.")
	fmt.Fprintf(a.Out, "  engine: %s (%s)\n", coreVersion, shortSHA(sha))
	fmt.Fprintf(a.Out, "  config: %s\n", a.Paths.ConfigFile())
	fmt.Fprintln(a.Out, "Next: mihatch up")
	return nil
}

// Sync re-imports the recorded source, purifies, validates, and atomically
// replaces the runtime config. On validation failure it keeps the existing
// config (fail-closed) and does not touch a running Mihomo or the system proxy.
func (a *App) Sync(ctx context.Context) error {
	release, err := a.acquireLock(ctx)
	if err != nil {
		return exit.New(exit.CodeLocked, err)
	}
	defer release()

	st, err := a.loadState()
	if err != nil {
		return exit.New(exit.CodeGeneral, err)
	}
	if !st.Initialized {
		return exit.New(exit.CodeUninitialized, errors.New("not initialized; run 'mihatch init' first"))
	}
	src := st.Source
	if src.Type == "" {
		src.Type = SourceClashVergeRev
	}

	sourceBytes, sourceHome, _, err := a.readSource(src)
	if err != nil {
		return exit.New(exit.CodeConfig, fmt.Errorf("%s", a.sourceMissingHint(src, err)))
	}

	purified, err := a.purifyAndValidate(ctx, sourceBytes, sourceHome)
	if err != nil {
		fmt.Fprintln(a.Err, "sync: candidate config failed validation; keeping existing config.")
		return exit.New(exit.CodeConfig, err)
	}
	if err := installConfig(a.Paths.ConfigFile(), purified); err != nil {
		return exit.New(exit.CodeGeneral, err)
	}
	fmt.Fprintln(a.Out, "Synced runtime config from source.")
	return nil
}

// installBinary installs the Mihomo engine into .mihatch/mihomo, either from a
// local --from path (offline) or by downloading the latest stable release.
func (a *App) installBinary(ctx context.Context, fromPath string) (version, sha string, err error) {
	binPath := a.Paths.Binary()
	if err := os.MkdirAll(a.Paths.TempDir(), 0o700); err != nil {
		return "", "", err
	}

	if fromPath != "" {
		fmt.Fprintf(a.Err, "  installing from local binary: %s\n", fromPath)
		ok, ferr := download.IsExecutableRegularFile(fromPath)
		if ferr != nil || !ok {
			return "", "", fmt.Errorf("offline binary %s is not a regular executable file", fromPath)
		}
		if err := copyFile(fromPath, binPath, 0o755); err != nil {
			return "", "", fmt.Errorf("copy offline binary: %w", err)
		}
	} else {
		fmt.Fprintln(a.Err, "  resolving latest stable release...")
		rel, ferr := a.FetchLatest(ctx)
		if ferr != nil {
			return "", "", fmt.Errorf("fetch latest mihomo release: %w (if offline, use: mihatch init --from /path/to/mihomo)", ferr)
		}
		asset, serr := download.SelectAsset(rel, runtime.GOOS, runtime.GOARCH)
		if serr != nil {
			return "", "", serr
		}
		fmt.Fprintf(a.Err, "  downloading Mihomo %s (%s/%s)...\n", rel.Tag, runtime.GOOS, runtime.GOARCH)
		gzPath := filepath.Join(a.Paths.TempDir(), "asset.gz")
		if _, derr := a.DownloadAsset(ctx, asset.URL, gzPath, a.Err); derr != nil {
			return "", "", fmt.Errorf("download asset: %w (if your network cannot reach GitHub reliably, install offline: mihatch init --from /path/to/mihomo)", derr)
		}
		fmt.Fprintln(a.Err, "  decompressing...")
		if gerr := download.GunzipFile(gzPath, binPath, download.DefaultMaxBinaryBytes); gerr != nil {
			return "", "", fmt.Errorf("decompress asset: %w", gerr)
		}
		_ = a.Paths.RemoveTempDir()
	}

	fmt.Fprintln(a.Err, "  verifying engine...")
	version, verr := a.Mihomo.Version(ctx, binPath)
	if verr != nil {
		return "", "", fmt.Errorf("verify engine: %w", verr)
	}
	sha, _ = download.SHA256File(binPath)
	return version, sha, nil
}

// purifyAndValidate purifies the source and validates the candidate config
// against the installed engine without touching the live config file.
func (a *App) purifyAndValidate(ctx context.Context, sourceBytes []byte, sourceHome string) ([]byte, error) {
	// Reuse upstream's already-downloaded geo databases (read-only copy) so the
	// validation step below does not re-fetch them over the network.
	copyGeoCaches(sourceHome, a.Paths.DotDir())

	purified, err := mihomoconfig.Purify(mihomoconfig.Options{
		MixedPort:     paths.DefaultMixedPort,
		Source:        sourceBytes,
		SourceHomeDir: sourceHome,
		ProvidersDir:  a.Paths.ProvidersDir(),
		RulesDir:      a.Paths.RulesDir(),
	})
	if err != nil {
		return nil, fmt.Errorf("purify config: %w", err)
	}
	if err := os.MkdirAll(a.Paths.TempDir(), 0o700); err != nil {
		return nil, err
	}
	candidate := filepath.Join(a.Paths.TempDir(), "config.yaml")
	if err := atomicfile.WriteFile(candidate, 0o600, purified); err != nil {
		return nil, err
	}
	// mihomo -t boots Mihomo far enough to load providers and geo data; on a
	// first run with no caches that means real network fetches.
	fmt.Fprintln(a.Err, "Validating config with mihomo -t (first run fetches providers/geo; can be slow on a slow link)…")
	if err := a.Mihomo.Validate(ctx, a.Paths.Binary(), a.Paths.DotDir(), candidate); err != nil {
		return nil, err
	}
	return purified, nil
}

// geoFileNames are the Mihomo geo databases MiHatch reuses from the source's
// data dir when present, so mihomo -t / first up need not download them.
var geoFileNames = []string{
	"Country.mmdb", "GeoIP.dat", "GeoSite.dat", "geoip.metadb",
	"geosite.dat", "geoip.dat", "GeoIP-asn.dat", "ASN.mmdb",
}

// copyGeoCaches copies any present geo databases from srcHome into dstDir
// (read-only reuse), skipping files that already exist at the destination.
func copyGeoCaches(srcHome, dstDir string) {
	for _, name := range geoFileNames {
		src := filepath.Join(srcHome, name)
		fi, err := os.Stat(src)
		if err != nil || fi.IsDir() {
			continue
		}
		dst := filepath.Join(dstDir, name)
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		_ = copyFile(src, dst, 0o600)
	}
}

// installConfig atomically writes the purified config to destPath (0600).
func installConfig(destPath string, purified []byte) error {
	return atomicfile.WriteFile(destPath, 0o600, purified)
}

// sourceMissingHint returns actionable guidance when the source config can't be read.
func (a *App) sourceMissingHint(src state.SourceRef, err error) string {
	if errors.Is(err, os.ErrNotExist) && src.Type != SourceFile {
		p := paths.CVRProdRuntimeFile(a.UserConfigDir)
		return fmt.Sprintf("Clash Verge Rev prod runtime not found at the expected path.\n"+
			"  expected: %s\n"+
			"  Make sure Clash Verge Rev (prod, not dev) has refreshed its current profile, then retry.\n"+
			"  Or import a local file: mihatch init --config /path/to/config.yaml", redactedHome(p))
	}
	return err.Error()
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func redactedHome(p string) string {
	h, _ := os.UserHomeDir()
	return redact.Path(p, h)
}
