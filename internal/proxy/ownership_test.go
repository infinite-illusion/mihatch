package proxy

import (
	"context"
	"strings"
	"sync"
	"testing"

	"mihatch/internal/runner"
)

var setVerbs = map[string]bool{
	setWebProxy: true, setSecureWebProxy: true, setSOCKSProxy: true,
	setAutoProxyURL: true, setAutoDiscovery: true,
	setWebProxyState: true, setSecureProxyState: true, setSOCKSProxyState: true,
	setBypassDomains: true,
}

// statefulFake returns before settings until a set-verb is issued for a
// service, after which get-verbs return the applied (desired) settings.
func statefulFake(before, desired map[string]ServiceProxy) *runner.Fake {
	var mu sync.Mutex
	applied := map[string]bool{}
	f := &runner.Fake{}
	f.Func = func(_ context.Context, name string, args []string) (runner.Result, error) {
		if name != cmdNetworkSetup || len(args) < 2 {
			return runner.Result{}, nil
		}
		verb, svc := args[0], args[1]
		mu.Lock()
		defer mu.Unlock()
		if setVerbs[verb] {
			applied[svc] = true
			return runner.Result{}, nil
		}
		s := before[svc]
		if applied[svc] {
			s = desired[svc]
		}
		switch verb {
		case getWebProxy, getSecureWebProxy, getSOCKSProxy:
			return runner.Result{Stdout: []byte(entryOut(proxyEntryFor(verb, s)))}, nil
		case getAutoProxyURL:
			return runner.Result{Stdout: []byte(autoOut(s.AutoProxy))}, nil
		case getAutoDiscovery:
			return runner.Result{Stdout: []byte(discOut(s.AutoDiscover))}, nil
		case getBypassDomains:
			return runner.Result{Stdout: []byte(bypassOut(s.Bypass))}, nil
		}
		return runner.Result{}, nil
	}
	return f
}

func proxyEntryFor(verb string, s ServiceProxy) ProxyEntry {
	switch verb {
	case getWebProxy:
		return s.WebProxy
	case getSecureWebProxy:
		return s.SecureProxy
	case getSOCKSProxy:
		return s.SOCKSProxy
	}
	return ProxyEntry{}
}

func disabledService(name string) ServiceProxy { return ServiceProxy{Service: name} }

func mihatchDesired(name string) ServiceProxy {
	return ServiceProxy{
		Service:      name,
		WebProxy:     ProxyEntry{Enabled: true, Server: "127.0.0.1", Port: "17890"},
		SecureProxy:  ProxyEntry{Enabled: true, Server: "127.0.0.1", Port: "17890"},
		SOCKSProxy:   ProxyEntry{Enabled: true, Server: "127.0.0.1", Port: "17890"},
		AutoDiscover: false,
		Bypass:       DefaultBypassDomains,
	}
}

func newAcquirer(f *runner.Fake) *Acquirer {
	return &Acquirer{
		Client:     NewClient(f),
		Host:       "127.0.0.1",
		Port:       17890,
		Bypass:     DefaultBypassDomains,
		EnableHTTP: true, EnableHTTPS: true, EnableSOCKS: true,
	}
}

func TestAcquireSuccessRecordsTransaction(t *testing.T) {
	t.Parallel()
	f := statefulFake(
		map[string]ServiceProxy{"Wi-Fi": disabledService("Wi-Fi")},
		map[string]ServiceProxy{"Wi-Fi": mihatchDesired("Wi-Fi")},
	)
	a := newAcquirer(f)
	tx, err := a.Acquire(context.Background(), []string{"Wi-Fi"}, false)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if tx.ID == "" || len(tx.Before) != 1 || len(tx.Applied) != 1 {
		t.Fatalf("bad transaction: %+v", tx)
	}
	if tx.AppliedFingerprint == "" {
		t.Fatal("missing applied fingerprint")
	}
	// Before must reflect the disabled snapshot, Applied the desired settings.
	if tx.Before[0].WebProxy.Enabled {
		t.Fatal("before should be disabled")
	}
	if !tx.Applied[0].WebProxy.Enabled || tx.Applied[0].WebProxy.Port != "17890" {
		t.Fatalf("applied mismatched: %+v", tx.Applied[0].WebProxy)
	}
}

func TestAcquireRefusesAuthenticatedProxy(t *testing.T) {
	t.Parallel()
	before := ServiceProxy{
		Service:  "Wi-Fi",
		WebProxy: ProxyEntry{Enabled: true, Server: "10.0.0.1", Port: "8080", AuthEnabled: true},
	}
	f := statefulFake(
		map[string]ServiceProxy{"Wi-Fi": before},
		map[string]ServiceProxy{"Wi-Fi": mihatchDesired("Wi-Fi")},
	)
	a := newAcquirer(f)
	_, err := a.Acquire(context.Background(), []string{"Wi-Fi"}, false)
	if err == nil || !strings.Contains(err.Error(), "authenticated proxy") {
		t.Fatalf("expected authenticated-proxy refusal, got %v", err)
	}
	// No set commands should have run.
	for _, c := range f.Calls {
		if setVerbs[c.Args[0]] {
			t.Fatalf("must not apply when refusing auth: %v", c.Args)
		}
	}
	// --force path proceeds.
	if _, err := a.Acquire(context.Background(), []string{"Wi-Fi"}, true); err != nil {
		t.Fatalf("force should proceed: %v", err)
	}
}

func TestRestoreNoDriftRollsBack(t *testing.T) {
	t.Parallel()
	// After Acquire the live settings == desired. Restore must roll back to the
	// disabled "before" (setwebproxystate off).
	f := statefulFake(
		map[string]ServiceProxy{"Wi-Fi": disabledService("Wi-Fi")},
		map[string]ServiceProxy{"Wi-Fi": mihatchDesired("Wi-Fi")},
	)
	a := newAcquirer(f)
	tx, _ := a.Acquire(context.Background(), []string{"Wi-Fi"}, false)
	outcome, err := a.Restore(context.Background(), tx)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if outcome.Drifted() || len(outcome.Restored) != 1 {
		t.Fatalf("expected clean restore, got %+v", outcome)
	}
	if !f.Saw(cmdNetworkSetup, setWebProxyState, "Wi-Fi", stateOff) {
		t.Errorf("expected rollback to disable web proxy; calls=%v", f.Calls)
	}
}

func TestRestoreDriftAbandonsWithoutOverwrite(t *testing.T) {
	t.Parallel()
	// Live (current) settings: a third party changed the web-proxy port after
	// MiHatch took over.
	current := ServiceProxy{
		Service:  "Wi-Fi",
		WebProxy: ProxyEntry{Enabled: true, Server: "127.0.0.1", Port: "7890"},
	}
	f := fakeGetScripted("Wi-Fi", current)
	a := newAcquirer(f)

	// Transaction records what MiHatch actually wrote (port 17890) and the
	// disabled before-snapshot.
	tx := Transaction{
		ID:       NewTransactionID(),
		Services: []string{"Wi-Fi"},
		Before:   []ServiceProxy{disabledService("Wi-Fi")},
		Applied:  []ServiceProxy{mihatchDesired("Wi-Fi")}, // port 17890
	}
	tx.AppliedFingerprint = Fingerprint(tx.Applied)

	beforeSets := f.CallCount()
	outcome, err := a.Restore(context.Background(), tx)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if !outcome.Drifted() || len(outcome.Abandoned) != 1 || len(outcome.Restored) != 0 {
		t.Fatalf("expected drift+abandon, got %+v", outcome)
	}
	// Restore must issue the snapshot gets but NO rollback apply for the drifted
	// service.
	for i := beforeSets; i < f.CallCount(); i++ {
		c, _ := f.Lookup(i)
		if len(c.Args) >= 1 && setVerbs[c.Args[0]] {
			t.Fatalf("Restore must not write to a drifted service, saw %v", c.Args)
		}
	}
}
