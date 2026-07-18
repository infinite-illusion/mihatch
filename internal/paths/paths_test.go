package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCVRAppIDDistinction(t *testing.T) {
	t.Parallel()
	if CVRProdAppID == CVRDevAppID {
		t.Fatalf("prod and dev App IDs must differ")
	}
	// CRITICAL: dev is a string-prefix extension of prod. Prefix matching is
	// unsafe; selection must be exact-equality.
	if !strings.HasPrefix(CVRDevAppID, CVRProdAppID) {
		t.Fatalf("expected dev to prefix-extend prod (the hazard)")
	}
	if !IsCVRProdAppID(CVRProdAppID) || IsCVRProdAppID(CVRDevAppID) {
		t.Fatal("prod classification must be exact")
	}
	if IsCVRDevAppID(CVRProdAppID) || !IsCVRDevAppID(CVRDevAppID) {
		t.Fatal("dev classification must be exact")
	}
}

func TestCVRProdRuntimeUsesProdAppID(t *testing.T) {
	t.Parallel()
	got := CVRProdRuntimeFile("/support")
	if !strings.Contains(got, CVRProdAppID) || strings.Contains(got, CVRDevAppID) {
		t.Fatalf("prod path must reference prod App ID only: %q", got)
	}
	if !strings.HasSuffix(got, CVRRuntimeFile) {
		t.Fatalf("runtime path must end with %s: %q", CVRRuntimeFile, got)
	}
}

func TestProjectLocalLayout(t *testing.T) {
	t.Parallel()
	root := "/proj/mihatch"
	p := New(root)
	cases := map[string]string{
		"dot":   p.DotDir(),
		"bin":   p.Binary(),
		"cfg":   p.ConfigFile(),
		"state": p.StateFile(),
		"log":   p.LogFile(),
		"lock":  p.LockFile(),
		"prov":  p.ProvidersDir(),
		"rules": p.RulesDir(),
		"tmp":   p.TempDir(),
	}
	for name, got := range cases {
		if !strings.HasPrefix(got, filepath.Join(root, DotDirName)) {
			t.Errorf("%s %q must be under %s/.mihatch", name, got, root)
		}
	}
	if got, want := p.Binary(), filepath.Join(root, DotDirName, MihomoBinName); got != want {
		t.Errorf("Binary = %q want %q", got, want)
	}
}

func TestEnsureDirs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	p := New(root)
	if err := p.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	for _, d := range []string{p.DotDir(), p.ProvidersDir(), p.RulesDir(), p.TempDir()} {
		fi, err := os.Stat(d)
		if err != nil {
			t.Fatalf("stat %s: %v", d, err)
		}
		if fi.Mode().Perm() != 0o700 {
			t.Fatalf("dir %s mode %o want 0700", d, fi.Mode().Perm())
		}
	}
}

func TestResolveRootFallbacks(t *testing.T) {
	// t.Setenv is incompatible with t.Parallel, so this test is serial.
	t.Setenv("MIHATCH_ROOT", "/from/env")
	got, err := ResolveRoot("")
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	if got != "/from/env" {
		t.Fatalf("env override: got %q want /from/env", got)
	}
	// Explicit wins over env.
	got, _ = ResolveRoot("/explicit")
	if got != "/explicit" {
		t.Fatalf("explicit: got %q", got)
	}
}
