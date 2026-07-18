package proxy

import (
	"regexp"
	"strings"
)

// DefaultBypassDomains is the MiHatch default proxy bypass list. Local and
// private ranges so intra-machine and LAN traffic never traverses the proxy.
var DefaultBypassDomains = []string{
	"localhost",
	"127.0.0.1",
	"::1",
	"*.local",
	"169.254/16",
	"10/8",
	"172.16/12",
	"192.168/16",
}

var (
	// serviceLineRe matches "(1) Service Name" and "(12) * Disabled Svc".
	serviceLineRe = regexp.MustCompile(`^\(\d+\)\s*(.*)$`)
	// deviceRe extracts the BSD device from a Hardware Port line.
	deviceRe = regexp.MustCompile(`Device:\s*([^)]*)`)
)

// ParseServiceOrder parses -listnetworkserviceorder output into services. A
// service is disabled if its line carries a leading "*" marker (the man page
// documents an asterisk denotes an inactive service).
func ParseServiceOrder(out string) []Service {
	var services []Service
	var cur *Service
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		// Normalize a leading "* " marker that may precede the "(N)" token on
		// some macOS versions.
		disabled := false
		if strings.HasPrefix(line, "* ") {
			disabled = true
			line = strings.TrimSpace(strings.TrimPrefix(line, "* "))
		}
		if m := serviceLineRe.FindStringSubmatch(line); m != nil {
			name := strings.TrimSpace(m[1])
			if strings.HasPrefix(name, "* ") {
				disabled = true
				name = strings.TrimSpace(strings.TrimPrefix(name, "* "))
			}
			services = append(services, Service{Name: name, Disabled: disabled})
			cur = &services[len(services)-1]
			continue
		}
		if cur != nil && strings.HasPrefix(line, "(Hardware Port:") {
			if d := deviceRe.FindStringSubmatch(line); d != nil {
				cur.Device = strings.TrimSpace(d[1])
			}
		}
	}
	return services
}
