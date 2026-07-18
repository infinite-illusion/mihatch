package state

import (
	"path/filepath"
	"testing"

	"mihatch/internal/proxy"
)

func TestDetermineTable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		f    Facts
		want State
	}{
		{"uninit-no-config", Facts{}, StateUninitialized},
		{"uninit-no-binary", Facts{Initialized: true}, StateUninitialized},
		{"stopped", Facts{Initialized: true, BinaryExists: true}, StateStopped},
		{
			"degraded-process-no-port",
			Facts{Initialized: true, BinaryExists: true, ProcessRunning: true},
			StateDegraded,
		},
		{
			"degraded-port-no-health",
			Facts{Initialized: true, BinaryExists: true, ProcessRunning: true, PortListening: true, ProxyOK: false},
			StateDegraded,
		},
		{
			"degraded-drifted",
			Facts{Initialized: true, BinaryExists: true, ProcessRunning: true, PortListening: true, ProxyOK: true, OwnershipOwned: true, OwnershipDrifted: true},
			StateDegraded,
		},
		{
			"standby-healthy-not-owned",
			Facts{Initialized: true, BinaryExists: true, ProcessRunning: true, PortListening: true, ProxyOK: true},
			StateStandby,
		},
		{
			"active-healthy-owned",
			Facts{Initialized: true, BinaryExists: true, ProcessRunning: true, PortListening: true, ProxyOK: true, OwnershipOwned: true},
			StateActive,
		},
	}
	for _, c := range cases {
		if got := Determine(c.f); got != c.want {
			t.Errorf("%s: got %s want %s", c.name, got, c.want)
		}
	}
}

func TestPersistedRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	p := &Persisted{}
	p.MarkInitialized("t0", "/proj/.mihatch/mihomo", "sha", "v1.19.28", 17890)
	p.MarkUp("t1", &ProcessInfo{PID: 123, StartTime: "Thu Jul 16 10:00:00 2026", BinaryPath: "/proj/.mihatch/mihomo"}, true)
	p.SetOwnership(Ownership{
		Owned: true, TransactionID: "tx-1", AppliedFingerprint: "fp-1", Services: []string{"Wi-Fi"},
		Before:  []proxy.ServiceProxy{{Service: "Wi-Fi"}},
		Applied: []proxy.ServiceProxy{{Service: "Wi-Fi", WebProxy: proxy.ProxyEntry{Enabled: true, Server: "127.0.0.1", Port: "17890"}}},
	})
	if err := p.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.CoreVersion != "v1.19.28" || loaded.MixedPort != 17890 {
		t.Fatalf("engine facts lost: %+v", loaded)
	}
	if loaded.Process == nil || loaded.Process.PID != 123 {
		t.Fatalf("process info lost: %+v", loaded.Process)
	}
	if !loaded.Ownership.Owned || loaded.Ownership.TransactionID != "tx-1" {
		t.Fatalf("ownership lost: %+v", loaded.Ownership)
	}
	if loaded.LastState != StateActive {
		t.Fatalf("last state %s want active", loaded.LastState)
	}
}

func TestLoadMissing(t *testing.T) {
	t.Parallel()
	p, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.Initialized || p.SchemaVersion != SchemaVersion {
		t.Fatalf("missing state should be uninitialized schema v1: %+v", p)
	}
}

func TestClearOwnershipKeepsProcess(t *testing.T) {
	t.Parallel()
	p := &Persisted{}
	p.MarkUp("t", &ProcessInfo{PID: 5}, true)
	p.SetOwnership(Ownership{Owned: true, TransactionID: "tx"})
	p.ClearOwnership("t2")
	if p.Ownership.Owned {
		t.Fatal("ownership should be cleared")
	}
	if p.Process == nil || p.Process.PID != 5 {
		t.Fatal("process must survive a pause (clear ownership)")
	}
	if p.LastState != StateStandby {
		t.Fatalf("last state %s want standby", p.LastState)
	}
}

func TestMarkDownClearsAll(t *testing.T) {
	t.Parallel()
	p := &Persisted{}
	p.MarkUp("t", &ProcessInfo{PID: 5}, true)
	p.SetOwnership(Ownership{Owned: true})
	p.MarkDown("t2")
	if p.Process != nil || p.Ownership.Owned {
		t.Fatal("MarkDown must clear process and ownership")
	}
	if p.LastState != StateStopped {
		t.Fatalf("last state %s want stopped", p.LastState)
	}
}
