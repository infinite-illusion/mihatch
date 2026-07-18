package redact

import "testing"

func TestURLStripsQueryAndUserinfo(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"https://sub.example.com/api/config?token=verysecret":        "https://sub.example.com/api/config",
		"https://user:pass@host/path":                                "https://host/path",
		"http://127.0.0.1:8080/sub?sub=1&token=abc":                  "http://127.0.0.1:8080/sub",
		"https://example.com/link/0987654321abcdef0987654321abcdef":  "https://example.com/link/tok",
		"https://example.com/d/12345678-1234-1234-1234-1234567890ab": "https://example.com/d/tok",
		"":   "",
		"  ": "",
	}
	for in, want := range cases {
		if got := URL(in); got != want {
			t.Errorf("URL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestURLUnparseable(t *testing.T) {
	t.Parallel()
	if got := URL("://nope"); got != "[unparseable-url]" {
		t.Errorf("URL(://nope) = %q, want unparseable marker", got)
	}
}

func TestIsSensitiveKey(t *testing.T) {
	t.Parallel()
	sensitive := []string{"secret", "proxy-providers.foo.url", "password", "API-KEY", "Authorization", "x-auth-token", "my_uuid", "subscriptionToken"}
	for _, k := range sensitive {
		if !IsSensitiveKey(k) {
			t.Errorf("IsSensitiveKey(%q) = false, want true", k)
		}
	}
	clean := []string{"mixed-port", "allow-lan", "mode", "name", "server", "port", "rules"}
	for _, k := range clean {
		if IsSensitiveKey(k) {
			t.Errorf("IsSensitiveKey(%q) = true, want false", k)
		}
	}
}

func TestField(t *testing.T) {
	t.Parallel()
	if got := Field("secret", "hunter2"); got != Secret {
		t.Errorf("Field(secret,...) = %q, want %q", got, Secret)
	}
	if got := Field("port", "7890"); got != "7890" {
		t.Errorf("Field(port,...) = %q, want 7890", got)
	}
	// URL-shaped keys: scheme+host only, no query token.
	if got := Field("proxy-providers.sub.url", "https://sub.example.com/link?token=topsecret"); got != "https://sub.example.com/link" {
		t.Errorf("Field(url-key,...) = %q, want scheme+host", got)
	}
	if got := Field("geox-url", "https://example.com/geoip.dat?x=1"); got != "https://example.com/geoip.dat" {
		t.Errorf("Field(geox-url,...) = %q", got)
	}
}

func TestPathRedactsHome(t *testing.T) {
	t.Parallel()
	home := "/Users/test"
	cases := map[string]string{
		"/Users/test/Library/Application Support/mihatch": "~/Library/Application Support/mihatch",
		"/Users/test":         "~",
		"/Users/other/secret": "/Users/other/secret",
		"/tmp/x":              "/tmp/x",
		"/Users/testfoo/file": "/Users/testfoo/file", // prefix-but-not-home must not redact
	}
	for in, want := range cases {
		if got := Path(in, home); got != want {
			t.Errorf("Path(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSecretValue(t *testing.T) {
	t.Parallel()
	if got := SecretValue(""); got != "" {
		t.Errorf("SecretValue('') = %q, want empty", got)
	}
	if got := SecretValue("anything"); got != Secret {
		t.Errorf("SecretValue(nonempty) = %q, want %q", got, Secret)
	}
}

func TestHostOf(t *testing.T) {
	t.Parallel()
	if got := HostOf("https://sub.example.com:8443/path?token=x"); got != "https://sub.example.com:8443" {
		t.Errorf("HostOf = %q", got)
	}
}
