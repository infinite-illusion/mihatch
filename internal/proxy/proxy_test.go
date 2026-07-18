package proxy

import "testing"

func TestProxyEntryEqual(t *testing.T) {
	t.Parallel()
	a := ProxyEntry{Enabled: true, Server: "127.0.0.1", Port: "17890"}
	b := ProxyEntry{Enabled: true, Server: "127.0.0.1", Port: "17890"}
	if !a.Equal(b) {
		t.Fatal("identical entries not equal")
	}
	b.Port = "7890"
	if a.Equal(b) {
		t.Fatal("differing port should not be equal")
	}
}

func TestServiceProxyAuthenticatedDetection(t *testing.T) {
	t.Parallel()
	s := ServiceProxy{WebProxy: ProxyEntry{AuthEnabled: true}}
	if !s.HasAuthenticatedProxy() {
		t.Fatal("should detect authenticated web proxy")
	}
	s = ServiceProxy{SOCKSProxy: ProxyEntry{AuthEnabled: false}}
	if s.HasAuthenticatedProxy() {
		t.Fatal("no auth should not be flagged")
	}
}

func TestPlanRestoreNoDrift(t *testing.T) {
	t.Parallel()
	before := []ServiceProxy{{Service: "Wi-Fi", WebProxy: ProxyEntry{Enabled: false}}}
	applied := []ServiceProxy{{Service: "Wi-Fi", WebProxy: ProxyEntry{Enabled: true, Server: "127.0.0.1", Port: "17890"}}}
	current := []ServiceProxy{{Service: "Wi-Fi", WebProxy: ProxyEntry{Enabled: true, Server: "127.0.0.1", Port: "17890"}}}
	out, err := PlanRestore(before, applied, current)
	if err != nil {
		t.Fatalf("PlanRestore: %v", err)
	}
	if out.Drifted() || len(out.Restored) != 1 || len(out.Abandoned) != 0 {
		t.Fatalf("expected clean restore, got %+v", out)
	}
}

func TestPlanRestoreDriftAbandons(t *testing.T) {
	t.Parallel()
	before := []ServiceProxy{{Service: "Wi-Fi", WebProxy: ProxyEntry{Enabled: false}}}
	applied := []ServiceProxy{{Service: "Wi-Fi", WebProxy: ProxyEntry{Enabled: true, Server: "127.0.0.1", Port: "17890"}}}
	// A third party (e.g. Clash Verge Rev dev) changed the port after takeover.
	current := []ServiceProxy{{Service: "Wi-Fi", WebProxy: ProxyEntry{Enabled: true, Server: "127.0.0.1", Port: "7890"}}}
	out, err := PlanRestore(before, applied, current)
	if err != nil {
		t.Fatalf("PlanRestore: %v", err)
	}
	if !out.Drifted() || len(out.Abandoned) != 1 || len(out.Restored) != 0 {
		t.Fatalf("expected drift+abandon, got %+v", out)
	}
}

func TestFingerprintStable(t *testing.T) {
	t.Parallel()
	a := []ServiceProxy{{Service: "Wi-Fi"}, {Service: "Ethernet"}}
	b := []ServiceProxy{{Service: "Ethernet"}, {Service: "Wi-Fi"}} // reordered
	if Fingerprint(a) != Fingerprint(b) {
		t.Fatal("fingerprint must be order-independent")
	}
	if Fingerprint(a) != Fingerprint(a) {
		t.Fatal("fingerprint must be deterministic")
	}
}

func TestNewTransactionID(t *testing.T) {
	t.Parallel()
	id := NewTransactionID()
	if len(id) < 8 || id[:3] != "tx-" {
		t.Fatalf("bad transaction id: %q", id)
	}
}
