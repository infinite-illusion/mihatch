// Package proxy implements macOS system-proxy ownership: capturing a complete
// snapshot of every proxy setting per network service, taking ownership safely,
// and restoring via compare-before-restore so a third-party application (e.g.
// Clash Verge Rev dev) that changed settings after takeover is never clobbered.
//
// The package is split into:
//   - types.go: data model + equality + fingerprint (pure, unit-tested);
//   - parser.go: parsing of networksetup text output (fixture-tested);
//   - networksetup.go: the command surface (argv-only, via internal/runner);
//   - discovery.go: mapping the default route to a network service;
//   - ownership.go: acquire / restore / compare-before-restore orchestration.
package proxy

// Enabled is a tri-state for "is this proxy enabled". networksetup prints
// "Enabled: Yes"/"Enabled: No".
type Enabled bool

// ProxyEntry is a single host:port proxy target plus its auth-enabled flag.
type ProxyEntry struct {
	Enabled     bool   `json:"enabled"`
	Server      string `json:"server,omitempty"`
	Port        string `json:"port,omitempty"`
	AuthEnabled bool   `json:"auth_enabled,omitempty"`
}

// Equal reports whether two ProxyEntry values are identical.
func (a ProxyEntry) Equal(b ProxyEntry) bool {
	return a.Enabled == b.Enabled &&
		a.Server == b.Server &&
		a.Port == b.Port &&
		a.AuthEnabled == b.AuthEnabled
}

// AutoProxy is the PAC (Automatic Proxy Configuration) URL + its state.
type AutoProxy struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url,omitempty"`
}

// Equal reports whether two AutoProxy values are identical.
func (a AutoProxy) Equal(b AutoProxy) bool {
	return a.Enabled == b.Enabled && a.URL == b.URL
}

// ServiceProxy is the complete proxy configuration of one network service.
type ServiceProxy struct {
	Service      string     `json:"service"`
	WebProxy     ProxyEntry `json:"web_proxy"`
	SecureProxy  ProxyEntry `json:"secure_proxy"`
	SOCKSProxy   ProxyEntry `json:"socks_proxy"`
	AutoProxy    AutoProxy  `json:"auto_proxy"`
	AutoDiscover bool       `json:"auto_discover"`
	Bypass       []string   `json:"bypass,omitempty"`
}

// Equal reports whether two ServiceProxy values are identical, including the
// bypass domain list (order-sensitive, matching networksetup semantics).
func (a ServiceProxy) Equal(b ServiceProxy) bool {
	if a.Service != b.Service {
		return false
	}
	if !a.WebProxy.Equal(b.WebProxy) {
		return false
	}
	if !a.SecureProxy.Equal(b.SecureProxy) {
		return false
	}
	if !a.SOCKSProxy.Equal(b.SOCKSProxy) {
		return false
	}
	if !a.AutoProxy.Equal(b.AutoProxy) {
		return false
	}
	if a.AutoDiscover != b.AutoDiscover {
		return false
	}
	if len(a.Bypass) != len(b.Bypass) {
		return false
	}
	for i := range a.Bypass {
		if a.Bypass[i] != b.Bypass[i] {
			return false
		}
	}
	return true
}

// HasAuthenticatedProxy reports whether any of the three proxies has
// authentication enabled. MiHatch cannot reliably restore authenticated proxy
// credentials (it never reads Keychain), so such configurations must be
// refused unless the user passes --force.
func (s ServiceProxy) HasAuthenticatedProxy() bool {
	return s.WebProxy.AuthEnabled || s.SecureProxy.AuthEnabled || s.SOCKSProxy.AuthEnabled
}

// Find returns the ServiceProxy for name or false.
func Find(list []ServiceProxy, name string) (ServiceProxy, bool) {
	for i := range list {
		if list[i].Service == name {
			return list[i], true
		}
	}
	return ServiceProxy{}, false
}
