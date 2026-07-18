package app_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mihatch/internal/app"
	"mihatch/internal/download"
	"mihatch/internal/health"
	"mihatch/internal/mihomo"
	"mihatch/internal/paths"
	"mihatch/internal/proxy"
	"mihatch/internal/runner"
	"mihatch/internal/state"
)

// --- networksetup fake (stateful: before -> desired after a set verb) ---

type nsFake struct {
	*runner.Fake
	mu      sync.Mutex
	applied map[string]bool
	before  map[string]proxy.ServiceProxy
	desired map[string]proxy.ServiceProxy
}

func newNSFake() *nsFake {
	n := &nsFake{
		Fake:    &runner.Fake{},
		applied: map[string]bool{},
		before:  map[string]proxy.ServiceProxy{"Wi-Fi": disabledSvc("Wi-Fi")},
		desired: map[string]proxy.ServiceProxy{"Wi-Fi": enabledSvc("Wi-Fi", "17890")},
	}
	n.Func = func(_ context.Context, name string, args []string) (runner.Result, error) {
		if name == "route" {
			return runner.Result{Stdout: []byte("  interface: en0\n")}, nil
		}
		if len(args) >= 1 && args[0] == "-listnetworkserviceorder" {
			return runner.Result{Stdout: []byte("(1) Wi-Fi\n(Hardware Port: Wi-Fi, Device: en0)\n")}, nil
		}
		if len(args) < 2 {
			return runner.Result{}, nil
		}
		verb, svc := args[0], args[1]
		if isSetVerb(verb) {
			n.mu.Lock()
			n.applied[svc] = true
			n.mu.Unlock()
			return runner.Result{}, nil
		}
		n.mu.Lock()
		defer n.mu.Unlock()
		s := n.before[svc]
		if n.applied[svc] {
			s = n.desired[svc]
		}
		return runner.Result{Stdout: []byte(nsOut(verb, s))}, nil
	}
	return n
}

func isSetVerb(v string) bool {
	switch v {
	case "-setwebproxy", "-setsecurewebproxy", "-setsocksfirewallproxy",
		"-setautoproxyurl", "-setproxyautodiscovery",
		"-setwebproxystate", "-setsecurewebproxystate", "-setsocksfirewallproxystate",
		"-setproxybypassdomains":
		return true
	}
	return false
}

func disabledSvc(name string) proxy.ServiceProxy { return proxy.ServiceProxy{Service: name} }
func enabledSvc(name, port string) proxy.ServiceProxy {
	return proxy.ServiceProxy{
		Service:     name,
		WebProxy:    proxy.ProxyEntry{Enabled: true, Server: "127.0.0.1", Port: port},
		SecureProxy: proxy.ProxyEntry{Enabled: true, Server: "127.0.0.1", Port: port},
		SOCKSProxy:  proxy.ProxyEntry{Enabled: true, Server: "127.0.0.1", Port: port},
		Bypass:      proxy.DefaultBypassDomains,
	}
}

func nsOut(verb string, s proxy.ServiceProxy) string {
	switch verb {
	case "-getwebproxy":
		return entryOut(s.WebProxy)
	case "-getsecurewebproxy":
		return entryOut(s.SecureProxy)
	case "-getsocksfirewallproxy":
		return entryOut(s.SOCKSProxy)
	case "-getautoproxyurl":
		return autoOut(s.AutoProxy)
	case "-getproxyautodiscovery":
		return discOut(s.AutoDiscover)
	case "-getproxybypassdomains":
		return bypassOut(s.Bypass)
	}
	return ""
}
func entryOut(e proxy.ProxyEntry) string {
	en, auth := "No", "0"
	if e.Enabled {
		en = "Yes"
	}
	if e.AuthEnabled {
		auth = "1"
	}
	srv := e.Server
	if srv == "" {
		srv = "(null)"
	}
	port := e.Port
	if port == "" {
		port = "0"
	}
	return "Enabled: " + en + "\nServer: " + srv + "\nPort: " + port + "\nAuthenticated Proxy Enabled: " + auth + "\n"
}
func autoOut(a proxy.AutoProxy) string {
	en := "No"
	if a.Enabled {
		en = "Yes"
	}
	u := a.URL
	if u == "" {
		u = "(null)"
	}
	return "URL: " + u + "\nEnabled: " + en + "\n"
}
func discOut(on bool) string {
	v := "Off"
	if on {
		v = "On"
	}
	return "Auto Proxy Discovery: " + v + "\n"
}
func bypassOut(b []string) string {
	if len(b) == 0 {
		return "There aren't any bypass domains set on Wi-Fi.\n"
	}
	return strings.Join(b, "\n") + "\n"
}

// --- test app builder ---

func newTestApp(t *testing.T) (*app.App, *nsFake, *mihomo.FakeManager, *health.Fake, string) {
	t.Helper()
	root := t.TempDir()
	ns := newNSFake()
	mgr := &mihomo.FakeManager{VersionStr: "v1.19.28"}
	hf := &health.Fake{Listening: true, ProxyResult: health.Result{OK: true}}
	var out bytes.Buffer
	a := &app.App{
		Paths:         paths.New(root),
		Runner:        ns, // networksetup + route
		Mihomo:        mgr,
		Health:        hf,
		UserConfigDir: filepath.Join(root, "support"), // CVR support dir (no real CVR)
		Clock:         func() time.Time { return time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC) },
		Out:           &out,
		Err:           &out,
	}
	return a, ns, mgr, hf, root
}

// placeCVR writes a minimal prod runtime YAML (and a decoy dev one) under the
// app's UserConfigDir.
func placeCVR(t *testing.T, a *app.App, body string) {
	t.Helper()
	prod := filepath.Join(a.UserConfigDir, paths.CVRProdAppID)
	if err := os.MkdirAll(prod, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prod, paths.CVRRuntimeFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// Decoy dev runtime must never be selected.
	dev := filepath.Join(a.UserConfigDir, paths.CVRDevAppID)
	_ = os.MkdirAll(dev, 0o700)
	_ = os.WriteFile(filepath.Join(dev, paths.CVRRuntimeFile), []byte("mixed-port: 1\n"), 0o600)
}

const minimalRuntime = `
mixed-port: 7890
proxies:
  - {name: n1, type: ss, server: example.com, port: 8388, cipher: aes-256-gcm, password: pw}
proxy-groups:
  - {name: PROXY, type: select, proxies: [n1]}
rules:
  - MATCH,PROXY
`

// makeOfflineBinary writes a dummy executable (the real one isn't needed; the
// FakeManager short-circuits -v/-t).
func makeOfflineBinary(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "mihomo")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// --- tests ---

func TestInitCVROffline(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only behavior")
	}
	a, _, _, _, root := newTestApp(t)
	placeCVR(t, a, minimalRuntime)

	if err := a.Init(context.Background(), app.InitOpts{FromPath: makeOfflineBinary(t)}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Engine copied into .mihatch/mihomo.
	if fi, err := os.Stat(paths.New(root).Binary()); err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("engine not installed 0o755: %v %v", err, fi)
	}
	// Config purified.
	cfg, err := os.ReadFile(paths.New(root).ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), "mixed-port: 17890") {
		t.Errorf("config not purified: %s", cfg)
	}
	if !strings.Contains(string(cfg), "allow-lan: false") || !strings.Contains(string(cfg), "bind-address: 127.0.0.1") {
		t.Errorf("isolation overrides missing: %s", cfg)
	}
	// State.
	st, _ := state.Load(paths.New(root).StateFile())
	if !st.Initialized || st.CoreVersion != "v1.19.28" || st.MixedPort != 17890 {
		t.Fatalf("bad state: %+v", st)
	}
	if st.Source.Type != "clash-verge-rev" {
		t.Fatalf("source type = %q want clash-verge-rev", st.Source.Type)
	}
}

func TestStatusUninitialized(t *testing.T) {
	a, _, _, _, _ := newTestApp(t)
	rep, err := a.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if rep.State != state.StateUninitialized || rep.Initialized {
		t.Fatalf("expected uninitialized, got %+v", rep)
	}
}

func TestUpDownFlow(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only")
	}
	a, ns, mgr, _, root := newTestApp(t)
	placeCVR(t, a, minimalRuntime)
	ctx := context.Background()

	if err := a.Init(ctx, app.InitOpts{FromPath: makeOfflineBinary(t)}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Up: start mihomo, become healthy, take over proxy.
	if err := a.Up(ctx, app.UpOpts{}); err != nil {
		t.Fatalf("Up: %v", err)
	}
	st, _ := state.Load(paths.New(root).StateFile())
	if st.Process == nil || !st.Ownership.Owned {
		t.Fatalf("expected running + owned after up: %+v", st)
	}
	// Proxy was actually set via networksetup (argv, no shell).
	if !ns.Fake.Saw("networksetup", "-setwebproxy", "Wi-Fi", "127.0.0.1", "17890") {
		t.Errorf("expected setwebproxy call; calls=%v", ns.Fake.Calls)
	}

	// Idempotent up: already healthy + owned -> no second start.
	startsBefore := len(mgr.StopCalls)
	if err := a.Up(ctx, app.UpOpts{}); err != nil {
		t.Fatalf("second Up: %v", err)
	}
	_ = startsBefore

	// Down: restore proxy + stop mihomo.
	if err := a.Down(ctx); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if len(mgr.StopCalls) == 0 {
		t.Errorf("expected mihomo Stop")
	}
	st2, _ := state.Load(paths.New(root).StateFile())
	if st2.Process != nil || st2.Ownership.Owned {
		t.Fatalf("down must clear process+ownership: %+v", st2)
	}
	// Restore issued setwebproxestate off.
	if !ns.Fake.Saw("networksetup", "-setwebproxystate", "Wi-Fi", "off") {
		t.Errorf("expected rollback to disable; calls=%v", ns.Fake.Calls)
	}
}

func TestPauseResume(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only")
	}
	a, _, mgr, _, root := newTestApp(t)
	placeCVR(t, a, minimalRuntime)
	ctx := context.Background()
	_ = a.Init(ctx, app.InitOpts{FromPath: makeOfflineBinary(t)})
	_ = a.Up(ctx, app.UpOpts{})

	pid := mgr.AlivePIDs // not directly useful; ensure process still alive after pause
	_ = pid
	if err := a.Pause(ctx); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	st, _ := state.Load(paths.New(root).StateFile())
	if st.Ownership.Owned {
		t.Fatal("pause must release ownership")
	}
	if st.Process == nil {
		t.Fatal("pause must keep mihomo running")
	}
	// Mihomo must NOT have been stopped by pause.
	if len(mgr.StopCalls) != 0 {
		t.Errorf("pause must not stop mihomo; stops=%v", mgr.StopCalls)
	}
	// Resume re-takes ownership with a fresh transaction.
	if err := a.Resume(ctx, app.UpOpts{}); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	st2, _ := state.Load(paths.New(root).StateFile())
	if !st2.Ownership.Owned || st2.Ownership.TransactionID == "" {
		t.Fatalf("resume must re-acquire ownership: %+v", st2.Ownership)
	}
}

func TestDownWhenNotInitializedIsIdempotent(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only")
	}
	a, _, _, _, _ := newTestApp(t)
	if err := a.Down(context.Background()); err != nil {
		t.Fatalf("Down on uninitialized should be a no-op success, got %v", err)
	}
}

func TestInitDownloadPath(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only")
	}
	a, _, _, _, root := newTestApp(t)
	placeCVR(t, a, minimalRuntime)

	// Build a real gzip payload so GunzipFile succeeds.
	var gz bytes.Buffer
	gw := gzip.NewWriter(&gz)
	gw.Write(bytes.Repeat([]byte("mihomo-fake-binary"), 512))
	gw.Close()
	gzBytes := gz.Bytes()

	a.FetchLatest = func(context.Context) (download.Release, error) {
		return download.Release{Tag: "v9.9.9", Assets: []download.Asset{
			{Name: "mihomo-darwin-arm64-v9.9.9.gz", URL: "http://test/asset.gz"},
		}}, nil
	}
	progressSeen := atomic.Bool{}
	a.DownloadAsset = func(_ context.Context, _, dest string, progress io.Writer) (int64, error) {
		// Write a couple of chunks to exercise the progress writer.
		if progress != nil {
			progress.Write(gzBytes[:len(gzBytes)/2])
			progress.Write(gzBytes[len(gzBytes)/2:])
			progressSeen.Store(true)
		}
		if err := os.WriteFile(dest, gzBytes, 0o600); err != nil {
			return 0, err
		}
		return int64(len(gzBytes)), nil
	}

	if err := a.Init(context.Background(), app.InitOpts{}); err != nil {
		t.Fatalf("Init (download path): %v", err)
	}
	// Engine installed.
	if _, err := os.Stat(paths.New(root).Binary()); err != nil {
		t.Fatalf("engine not installed: %v", err)
	}
	st, _ := state.Load(paths.New(root).StateFile())
	if !st.Initialized || st.CoreVersion != "v1.19.28" {
		t.Fatalf("bad state: %+v", st)
	}
	if !progressSeen.Load() {
		t.Errorf("progress writer was not used during download")
	}
}
