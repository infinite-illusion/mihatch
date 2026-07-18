package mihomoconfig

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func parseMap(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := yaml.Unmarshal(b, &m); err != nil {
		t.Fatalf("parse purified: %v\n%s", err, b)
	}
	return m
}

func TestPurifyForcesOverridesAndRemovesInbounds(t *testing.T) {
	t.Parallel()
	src := []byte(`
mixed-port: 7890
port: 7891
socks-port: 7892
redir-port: 7893
tproxy-port: 7894
allow-lan: true
bind-address: "*"
external-controller: 127.0.0.1:9090
external-controller-unix: /tmp/cvr.sock
secret: topsecret
external-ui: ui
authentication:
  - user:pass
tun:
  enable: true
  dns-hijack:
    - any:53
listeners:
  - name: inbound
tunnels:
  - network: [tcp, udp]
proxies:
  - name: node1
    type: ss
proxy-groups:
  - name: PROXY
rules:
  - MATCH,PROXY
`)
	out, err := Purify(Options{MixedPort: 17890, Source: src})
	if err != nil {
		t.Fatalf("Purify: %v", err)
	}
	m := parseMap(t, out)

	if m["mixed-port"] != 17890 {
		t.Errorf("mixed-port = %v want 17890", m["mixed-port"])
	}
	if m["port"] != nil || m["socks-port"] != nil || m["redir-port"] != nil || m["tproxy-port"] != nil {
		t.Error("extra inbound ports must be removed")
	}
	if m["allow-lan"] != false {
		t.Errorf("allow-lan = %v want false", m["allow-lan"])
	}
	if m["bind-address"] != "127.0.0.1" {
		t.Errorf("bind-address = %v want 127.0.0.1", m["bind-address"])
	}
	for _, k := range []string{"external-controller", "external-controller-unix", "secret", "external-ui", "authentication", "tun", "listeners", "tunnels"} {
		if m[k] != nil {
			t.Errorf("%s must be removed", k)
		}
	}
	// Outbound semantics preserved.
	if m["proxies"] == nil || m["proxy-groups"] == nil || m["rules"] == nil {
		t.Error("proxies/groups/rules must be preserved")
	}
}

func TestPurifyKeepsDNSResolverButDropsListener(t *testing.T) {
	t.Parallel()
	src := []byte(`
mixed-port: 7890
dns:
  enable: true
  listen: 0.0.0.0:1053
  nameserver:
    - 223.5.5.5
  fallback:
    - 1.1.1.1
`)
	out, err := Purify(Options{MixedPort: 17890, Source: src})
	if err != nil {
		t.Fatalf("Purify: %v", err)
	}
	m := parseMap(t, out)
	dns, ok := m["dns"].(map[string]any)
	if !ok {
		t.Fatal("dns block must be preserved")
	}
	if dns["listen"] != nil {
		t.Errorf("dns.listen must be removed, got %v", dns["listen"])
	}
	if dns["nameserver"] == nil || dns["fallback"] == nil {
		t.Error("dns resolver config must be preserved")
	}
}

func TestPurifyMigratesFileProviderAndRewritesHTTP(t *testing.T) {
	t.Parallel()
	cvrHome := t.TempDir()
	// A file provider referenced by the CVR runtime, under CVR's data dir.
	provDir := filepath.Join(cvrHome, "providers")
	_ = os.MkdirAll(provDir, 0o700)
	provFile := filepath.Join(provDir, "sub.yaml")
	if err := os.WriteFile(provFile, []byte("proxies: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	src := []byte(`
mixed-port: 7890
proxy-providers:
  sub:
    type: file
    path: providers/sub.yaml
  remote:
    type: http
    url: https://sub.example.com/token?secret=zzz
    path: /some/abs/cache.yaml
rule-providers:
  rc:
    type: http
    url: https://rules.example.com/r.yaml
`)
	provDest := filepath.Join(t.TempDir(), "providers")
	ruleDest := filepath.Join(t.TempDir(), "rules")
	out, err := Purify(Options{
		MixedPort:     17890,
		Source:        src,
		SourceHomeDir: cvrHome,
		ProvidersDir:  provDest,
		RulesDir:      ruleDest,
	})
	if err != nil {
		t.Fatalf("Purify: %v", err)
	}
	m := parseMap(t, out)

	pp := m["proxy-providers"].(map[string]any)
	sub := pp["sub"].(map[string]any)
	if sub["path"] != "providers/sub" {
		t.Errorf("file provider path = %v want providers/sub", sub["path"])
	}
	// File must have been copied into the MiHatch providers dir.
	if _, err := os.Stat(filepath.Join(provDest, "sub")); err != nil {
		t.Errorf("file provider not copied: %v", err)
	}
	remote := pp["remote"].(map[string]any)
	if remote["path"] != "providers/remote.yaml" {
		t.Errorf("http provider path = %v want providers/remote.yaml", remote["path"])
	}
	if remote["url"] == nil {
		t.Error("http provider url must be preserved")
	}

	rp := m["rule-providers"].(map[string]any)
	rc := rp["rc"].(map[string]any)
	if rc["path"] != "rules/rc.yaml" {
		t.Errorf("rule provider path = %v want rules/rc.yaml", rc["path"])
	}
}

func TestPurifyFileProviderMissingFails(t *testing.T) {
	t.Parallel()
	src := []byte(`
mixed-port: 7890
proxy-providers:
  ghost:
    type: file
    path: does/not/exist.yaml
`)
	_, err := Purify(Options{
		MixedPort:     17890,
		Source:        src,
		SourceHomeDir: t.TempDir(),
		ProvidersDir:  filepath.Join(t.TempDir(), "p"),
		RulesDir:      filepath.Join(t.TempDir(), "r"),
	})
	if err == nil {
		t.Fatal("expected error for missing file provider")
	}
}

func TestPurifyHTTPProviderReusesExistingCache(t *testing.T) {
	t.Parallel()
	cvrHome := t.TempDir()
	cache := filepath.Join(cvrHome, "providers", "remote.yaml")
	_ = os.MkdirAll(filepath.Dir(cache), 0o700)
	_ = os.WriteFile(cache, []byte("proxies: [cached]\n"), 0o600)

	src := []byte(`
mixed-port: 7890
proxy-providers:
  remote:
    type: http
    url: https://sub.example.com/token?secret=zzz
    path: providers/remote.yaml
`)
	provDest := filepath.Join(t.TempDir(), "providers")
	out, err := Purify(Options{
		MixedPort:     17890,
		Source:        src,
		SourceHomeDir: cvrHome,
		ProvidersDir:  provDest,
		RulesDir:      filepath.Join(t.TempDir(), "r"),
	})
	if err != nil {
		t.Fatalf("Purify: %v", err)
	}
	// Cache copied into .mihatch so mihomo -t need not re-download.
	if _, err := os.Stat(filepath.Join(provDest, "remote.yaml")); err != nil {
		t.Errorf("http provider cache not reused/copied: %v", err)
	}
	// Path rewritten to the local location.
	m := parseMap(t, out)
	pp := m["proxy-providers"].(map[string]any)
	if pp["remote"].(map[string]any)["path"] != "providers/remote.yaml" {
		t.Errorf("path = %v", pp["remote"])
	}
}
