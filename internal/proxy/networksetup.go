package proxy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mihatch/internal/runner"
)

// networksetup verb spelling (verified against the macOS man page).
const (
	cmdNetworkSetup = "networksetup"
	cmdRoute        = "route"

	getWebProxy           = "-getwebproxy"
	getSecureWebProxy     = "-getsecurewebproxy"
	getSOCKSProxy         = "-getsocksfirewallproxy"
	getAutoProxyURL       = "-getautoproxyurl"
	getAutoDiscovery      = "-getproxyautodiscovery"
	getBypassDomains      = "-getproxybypassdomains"
	listNetworkServiceOrd = "-listnetworkserviceorder"

	setWebProxy         = "-setwebproxy"
	setSecureWebProxy   = "-setsecurewebproxy"
	setSOCKSProxy       = "-setsocksfirewallproxy"
	setAutoProxyURL     = "-setautoproxyurl"
	setAutoDiscovery    = "-setproxyautodiscovery"
	setWebProxyState    = "-setwebproxystate"
	setSecureProxyState = "-setsecurewebproxystate"
	setSOCKSProxyState  = "-setsocksfirewallproxystate"
	setBypassDomains    = "-setproxybypassdomains"

	stateOn  = "on"
	stateOff = "off"

	// tokenClear is the documented way to clear all bypass domains.
	tokenClear = "Empty"
)

// Service is a discovered network service.
type Service struct {
	Name     string
	Device   string
	Disabled bool
}

// Client talks to networksetup and route via a runner.Runner. All argv are
// passed directly; no shell is ever invoked.
type Client struct {
	Run     runner.Runner
	Timeout time.Duration
}

// NewClient builds a Client with a default per-command timeout.
func NewClient(r runner.Runner) *Client {
	return &Client{Run: r, Timeout: 10 * time.Second}
}

func (c *Client) ctx(parent context.Context) (context.Context, context.CancelFunc) {
	if c.Timeout <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, c.Timeout)
}

func (c *Client) run(parent context.Context, name string, args ...string) (runner.Result, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	return c.Run.Run(ctx, name, args...)
}

// Get reads the complete proxy configuration of one service.
func (c *Client) Get(parent context.Context, service string) (ServiceProxy, error) {
	svc := ServiceProxy{Service: service}

	r, err := c.run(parent, cmdNetworkSetup, getWebProxy, service)
	if err != nil {
		return svc, fmt.Errorf("get web proxy for %q: %w", service, err)
	}
	svc.WebProxy = ParseProxyEntry(r.StdoutString())

	r, err = c.run(parent, cmdNetworkSetup, getSecureWebProxy, service)
	if err != nil {
		return svc, fmt.Errorf("get secure web proxy for %q: %w", service, err)
	}
	svc.SecureProxy = ParseProxyEntry(r.StdoutString())

	r, err = c.run(parent, cmdNetworkSetup, getSOCKSProxy, service)
	if err != nil {
		return svc, fmt.Errorf("get socks proxy for %q: %w", service, err)
	}
	svc.SOCKSProxy = ParseProxyEntry(r.StdoutString())

	r, err = c.run(parent, cmdNetworkSetup, getAutoProxyURL, service)
	if err != nil {
		return svc, fmt.Errorf("get auto proxy url for %q: %w", service, err)
	}
	svc.AutoProxy = ParseAutoProxy(r.StdoutString())

	r, err = c.run(parent, cmdNetworkSetup, getAutoDiscovery, service)
	if err != nil {
		return svc, fmt.Errorf("get auto discovery for %q: %w", service, err)
	}
	svc.AutoDiscover = ParseAutoDiscovery(r.StdoutString())

	r, err = c.run(parent, cmdNetworkSetup, getBypassDomains, service)
	if err != nil {
		return svc, fmt.Errorf("get bypass domains for %q: %w", service, err)
	}
	svc.Bypass = ParseBypass(r.StdoutString())

	return svc, nil
}

// Apply writes a full ServiceProxy configuration. Used both to take ownership
// (with MiHatch's desired settings) and to restore a prior snapshot.
func (c *Client) Apply(parent context.Context, s ServiceProxy) error {
	// 1. Disable WPAD first so it cannot conflict mid-change.
	if _, err := c.run(parent, cmdNetworkSetup, setAutoDiscovery, s.Service, stateOff); err != nil {
		return fmt.Errorf("set auto discovery: %w", err)
	}
	// PAC (auto-proxy URL): there is no reliable networksetup verb to *disable*
	// it — an empty URL is rejected on current macOS (exit 4). So only write it
	// when we actually want one enabled (restoring a PAC-on snapshot). An
	// already-disabled PAC is left untouched; MiHatch relies on the explicit
	// web/secure/socks proxies taking effect.
	if s.AutoProxy.Enabled && strings.TrimSpace(s.AutoProxy.URL) != "" {
		if _, err := c.run(parent, cmdNetworkSetup, setAutoProxyURL, s.Service, s.AutoProxy.URL); err != nil {
			return fmt.Errorf("set auto proxy url: %w", err)
		}
	}
	// 2. The three proxies.
	if err := c.applyEntry(parent, s.Service, s.WebProxy, setWebProxy, setWebProxyState); err != nil {
		return err
	}
	if err := c.applyEntry(parent, s.Service, s.SecureProxy, setSecureWebProxy, setSecureProxyState); err != nil {
		return err
	}
	if err := c.applyEntry(parent, s.Service, s.SOCKSProxy, setSOCKSProxy, setSOCKSProxyState); err != nil {
		return err
	}
	// 3. Bypass domains (clear with the literal token when empty).
	args := []string{setBypassDomains, s.Service}
	if len(s.Bypass) == 0 {
		args = append(args, tokenClear)
	} else {
		args = append(args, s.Bypass...)
	}
	if _, err := c.run(parent, cmdNetworkSetup, args...); err != nil {
		return fmt.Errorf("set bypass domains: %w", err)
	}
	return nil
}

// applyEntry sets one proxy entry: if enabled with a server, configure it
// (which also enables); otherwise disable via the *state verb, preserving any
// prior host/port.
func (c *Client) applyEntry(parent context.Context, service string, e ProxyEntry, setVerb, stateVerb string) error {
	if e.Enabled && e.Server != "" {
		port := e.Port
		if port == "" {
			port = "0"
		}
		if _, err := c.run(parent, cmdNetworkSetup, setVerb, service, e.Server, port); err != nil {
			return fmt.Errorf("%s: %w", setVerb, err)
		}
		return nil
	}
	if _, err := c.run(parent, cmdNetworkSetup, stateVerb, service, stateOff); err != nil {
		return fmt.Errorf("%s: %w", stateVerb, err)
	}
	return nil
}

// ListServices parses -listnetworkserviceorder into services with their BSD
// device and disabled flag.
func (c *Client) ListServices(parent context.Context) ([]Service, error) {
	r, err := c.run(parent, cmdNetworkSetup, listNetworkServiceOrd)
	if err != nil {
		return nil, fmt.Errorf("list network service order: %w", err)
	}
	return ParseServiceOrder(r.StdoutString()), nil
}

// DefaultInterface returns the primary outbound BSD interface from
// "route -n get default".
func (c *Client) DefaultInterface(parent context.Context) (string, error) {
	r, err := c.run(parent, cmdRoute, "-n", "get", "default")
	if err != nil {
		return "", fmt.Errorf("read default route: %w", err)
	}
	iface := ParseDefaultInterface(r.StdoutString())
	if iface == "" {
		return "", fmt.Errorf("could not determine default route interface")
	}
	return iface, nil
}

// ResolveServices returns the target network services for takeover. If explicit
// is non-empty it is validated against known, enabled services. Otherwise the
// service bound to the default-route interface is selected. Fails closed when no
// reliable mapping exists.
func (c *Client) ResolveServices(parent context.Context, explicit []string) ([]string, error) {
	all, err := c.ListServices(parent)
	if err != nil {
		return nil, err
	}
	enabled := make([]Service, 0, len(all))
	for _, s := range all {
		if !s.Disabled {
			enabled = append(enabled, s)
		}
	}
	if len(explicit) > 0 {
		known := map[string]bool{}
		for _, s := range enabled {
			known[s.Name] = true
		}
		var out []string
		for _, name := range explicit {
			if !known[name] {
				return nil, fmt.Errorf("service %q is not an enabled network service", name)
			}
			out = append(out, name)
		}
		return out, nil
	}
	iface, err := c.DefaultInterface(parent)
	if err != nil {
		return nil, err
	}
	for _, s := range enabled {
		if s.Device != "" && s.Device == iface {
			return []string{s.Name}, nil
		}
	}
	return nil, fmt.Errorf("no enabled network service bound to interface %q; specify --service", iface)
}

// ParseDefaultInterface extracts the "interface:" field from route output.
func ParseDefaultInterface(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "interface:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "interface:"))
		}
	}
	return ""
}
