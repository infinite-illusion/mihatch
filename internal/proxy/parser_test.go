package proxy

import (
	"reflect"
	"testing"
)

func TestParseProxyEntryEnabled(t *testing.T) {
	t.Parallel()
	out := "Enabled: Yes\nServer: 127.0.0.1\nPort: 17890\nAuthenticated Proxy Enabled: 0\n"
	e := ParseProxyEntry(out)
	if !e.Enabled || e.Server != "127.0.0.1" || e.Port != "17890" || e.AuthEnabled {
		t.Fatalf("got %+v", e)
	}
}

func TestParseProxyEntryDisabledNull(t *testing.T) {
	t.Parallel()
	out := "Enabled: No\nServer: (null)\nPort: 0\nAuthenticated Proxy Enabled: 1\n"
	e := ParseProxyEntry(out)
	if e.Enabled {
		t.Fatal("should be disabled")
	}
	if e.Server != "" {
		t.Fatalf("(null) server must coerce to empty, got %q", e.Server)
	}
	if !e.AuthEnabled {
		t.Fatal("auth flag 1 should be true")
	}
}

func TestParseAutoProxy(t *testing.T) {
	t.Parallel()
	a := ParseAutoProxy("URL: http://127.0.0.1:33331/pac\nEnabled: Yes\n")
	if !a.Enabled || a.URL != "http://127.0.0.1:33331/pac" {
		t.Fatalf("got %+v", a)
	}
}

func TestParseAutoDiscovery(t *testing.T) {
	t.Parallel()
	if !ParseAutoDiscovery("Auto Proxy Discovery: On\n") {
		t.Fatal("On -> true")
	}
	if ParseAutoDiscovery("Auto Proxy Discovery: Off\n") {
		t.Fatal("Off -> false")
	}
}

func TestParseBypass(t *testing.T) {
	t.Parallel()
	got := ParseBypass("127.0.0.1\n*.local\n192.168.0.0/16\n")
	want := []string{"127.0.0.1", "*.local", "192.168.0.0/16"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if ParseBypass("There aren't any bypass domains set on Wi-Fi.\n") != nil {
		t.Fatal("empty-bypass sentence must yield nil")
	}
	if ParseBypass("") != nil {
		t.Fatal("blank must yield nil")
	}
}

func TestParseDefaultInterface(t *testing.T) {
	t.Parallel()
	out := "   route to: default\n   interface: en0\n      flags: <UP>\n"
	if got := ParseDefaultInterface(out); got != "en0" {
		t.Fatalf("got %q want en0", got)
	}
	if ParseDefaultInterface("no iface here") != "" {
		t.Fatal("missing interface should be empty")
	}
}

func TestParseServiceOrder(t *testing.T) {
	t.Parallel()
	out := `An asterisk (*) denotes that a network service is disabled.
(1) USB 10/100/1000 LAN
(Hardware Port: USB 10/100/1000 LAN, Device: en7)

(2) Wi-Fi
(Hardware Port: Wi-Fi, Device: en0)

(3) * Tailscale
(Hardware Port: io.tailscale.ipn.macsys, Device: )
`
	svcs := ParseServiceOrder(out)
	if len(svcs) != 3 {
		t.Fatalf("got %d services want 3: %+v", len(svcs), svcs)
	}
	wifi := svcs[1]
	if wifi.Name != "Wi-Fi" || wifi.Device != "en0" || wifi.Disabled {
		t.Fatalf("Wi-Fi parsed wrong: %+v", wifi)
	}
	tail := svcs[2]
	if !tail.Disabled || tail.Device != "" {
		t.Fatalf("disabled Tailscale parsed wrong: %+v", tail)
	}
}
