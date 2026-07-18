package proxy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"mihatch/internal/runner"
)

// fakeGetScripted returns a Fake whose get-commands for the given service
// return the provided settings, and all set-commands succeed.
func fakeGetScripted(service string, s ServiceProxy) *runner.Fake {
	exact := map[string]runner.FakeEntry{
		join(networkSetupArgs(getWebProxy, service)):       {Result: runner.Result{Stdout: []byte(entryOut(s.WebProxy))}},
		join(networkSetupArgs(getSecureWebProxy, service)): {Result: runner.Result{Stdout: []byte(entryOut(s.SecureProxy))}},
		join(networkSetupArgs(getSOCKSProxy, service)):     {Result: runner.Result{Stdout: []byte(entryOut(s.SOCKSProxy))}},
		join(networkSetupArgs(getAutoProxyURL, service)):   {Result: runner.Result{Stdout: []byte(autoOut(s.AutoProxy))}},
		join(networkSetupArgs(getAutoDiscovery, service)):  {Result: runner.Result{Stdout: []byte(discOut(s.AutoDiscover))}},
		join(networkSetupArgs(getBypassDomains, service)):  {Result: runner.Result{Stdout: []byte(bypassOut(s.Bypass))}},
	}
	return &runner.Fake{Exact: exact, DefaultOK: true}
}

func join(args []string) string { return strings.Join(args, " ") }

func networkSetupArgs(verb, service string) []string { return []string{cmdNetworkSetup, verb, service} }

func entryOut(e ProxyEntry) string {
	en := "No"
	if e.Enabled {
		en = "Yes"
	}
	auth := "0"
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
func autoOut(a AutoProxy) string {
	en := "No"
	if a.Enabled {
		en = "Yes"
	}
	url := a.URL
	if url == "" {
		url = "(null)"
	}
	return "URL: " + url + "\nEnabled: " + en + "\n"
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

func TestClientGetReadsAllSix(t *testing.T) {
	t.Parallel()
	want := ServiceProxy{
		Service:      "Wi-Fi",
		WebProxy:     ProxyEntry{Enabled: true, Server: "127.0.0.1", Port: "17890"},
		SecureProxy:  ProxyEntry{Enabled: true, Server: "127.0.0.1", Port: "17890"},
		SOCKSProxy:   ProxyEntry{Enabled: false, Server: "", Port: ""},
		AutoProxy:    AutoProxy{Enabled: false, URL: ""},
		AutoDiscover: false,
		Bypass:       []string{"127.0.0.1", "*.local"},
	}
	c := NewClient(fakeGetScripted("Wi-Fi", want))
	got, err := c.Get(context.Background(), "Wi-Fi")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Equal(want) {
		t.Fatalf("Get mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestApplyArgvNoShell(t *testing.T) {
	t.Parallel()
	f := &runner.Fake{DefaultOK: true}
	c := NewClient(f)
	settings := ServiceProxy{
		Service:     "Wi-Fi",
		WebProxy:    ProxyEntry{Enabled: true, Server: "127.0.0.1", Port: "17890"},
		SecureProxy: ProxyEntry{Enabled: true, Server: "127.0.0.1", Port: "17890"},
		SOCKSProxy:  ProxyEntry{Enabled: false},
		Bypass:      []string{"127.0.0.1", "*.local"},
	}
	if err := c.Apply(context.Background(), settings); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// The three proxies must be set as argv, not via a shell.
	if !f.Saw(cmdNetworkSetup, setWebProxy, "Wi-Fi", "127.0.0.1", "17890") {
		t.Errorf("missing setwebproxy argv; calls=%v", f.Calls)
	}
	if !f.Saw(cmdNetworkSetup, setSOCKSProxyState, "Wi-Fi", stateOff) {
		t.Errorf("disabled socks must use *state verb; calls=%v", f.Calls)
	}
	// WPAD off (always). PAC is NOT touched when disabled (no reliable disable
	// verb; an empty URL is rejected by networksetup).
	if !f.Saw(cmdNetworkSetup, setAutoDiscovery, "Wi-Fi", stateOff) {
		t.Errorf("missing autodiscovery off")
	}
	if f.Saw(cmdNetworkSetup, setAutoProxyURL, "Wi-Fi", "") {
		t.Errorf("setautoproxyurl with empty URL must not be called (it errors)")
	}
	// Bypass passed as individual argv.
	if !f.Saw(cmdNetworkSetup, setBypassDomains, "Wi-Fi", "127.0.0.1", "*.local") {
		t.Errorf("missing bypass argv; calls=%v", f.Calls)
	}
}

func TestApplyClearsBypassWithEmptyToken(t *testing.T) {
	t.Parallel()
	f := &runner.Fake{DefaultOK: true}
	c := NewClient(f)
	if err := c.Apply(context.Background(), ServiceProxy{Service: "Wi-Fi"}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !f.Saw(cmdNetworkSetup, setBypassDomains, "Wi-Fi", tokenClear) {
		t.Errorf("empty bypass must use %q token; calls=%v", tokenClear, f.Calls)
	}
}

func TestApplySetsPACOnlyWhenEnabled(t *testing.T) {
	t.Parallel()
	// Restoring a PAC-enabled snapshot must set the real URL.
	f := &runner.Fake{DefaultOK: true}
	c := NewClient(f)
	if err := c.Apply(context.Background(), ServiceProxy{
		Service:   "Wi-Fi",
		AutoProxy: AutoProxy{Enabled: true, URL: "http://pac.example.com/cfg.pac"},
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !f.Saw(cmdNetworkSetup, setAutoProxyURL, "Wi-Fi", "http://pac.example.com/cfg.pac") {
		t.Errorf("expected setautoproxyurl with real URL; calls=%v", f.Calls)
	}
}

func TestResolveServicesAuto(t *testing.T) {
	t.Parallel()
	orderOut := `An asterisk (*) denotes that a network service is disabled.
(1) * Tailscale
(Hardware Port: io.tailscale, Device: )

(2) Wi-Fi
(Hardware Port: Wi-Fi, Device: en0)
`
	f := &runner.Fake{}
	f.Func = func(_ context.Context, name string, args []string) (runner.Result, error) {
		if name == cmdRoute {
			return runner.Result{Stdout: []byte("interface: en0\n")}, nil
		}
		if len(args) >= 1 && args[0] == listNetworkServiceOrd {
			return runner.Result{Stdout: []byte(orderOut)}, nil
		}
		return runner.Result{}, errors.New("unscripted")
	}
	c := NewClient(f)
	got, err := c.ResolveServices(context.Background(), nil)
	if err != nil {
		t.Fatalf("ResolveServices: %v", err)
	}
	if len(got) != 1 || got[0] != "Wi-Fi" {
		t.Fatalf("got %v want [Wi-Fi]", got)
	}
}

func TestResolveServicesExplicitRejectsUnknown(t *testing.T) {
	t.Parallel()
	orderOut := "(1) Wi-Fi\n(Hardware Port: Wi-Fi, Device: en0)\n"
	f := &runner.Fake{}
	f.Func = func(_ context.Context, name string, args []string) (runner.Result, error) {
		if len(args) >= 1 && args[0] == listNetworkServiceOrd {
			return runner.Result{Stdout: []byte(orderOut)}, nil
		}
		return runner.Result{}, errors.New("unscripted")
	}
	c := NewClient(f)
	if _, err := c.ResolveServices(context.Background(), []string{"Nope"}); err == nil {
		t.Fatal("expected error for unknown service")
	}
	got, err := c.ResolveServices(context.Background(), []string{"Wi-Fi"})
	if err != nil || len(got) != 1 || got[0] != "Wi-Fi" {
		t.Fatalf("explicit valid: got %v err %v", got, err)
	}
}
