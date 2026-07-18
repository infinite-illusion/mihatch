package proxy

import (
	"strconv"
	"strings"
)

// parseKV parses "Key: value" lines into a lowercased-key map. A line without a
// colon is ignored. Used for the networksetup get-proxy outputs.
func parseKV(out string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		i := strings.Index(line, ":")
		if i < 0 {
			continue
		}
		key := strings.TrimSpace(line[:i])
		val := strings.TrimSpace(line[i+1:])
		m[strings.ToLower(key)] = val
	}
	return m
}

// cleanValue coerces networksetup's "(null)" placeholder to the empty string.
func cleanValue(s string) string {
	s = strings.TrimSpace(s)
	if s == "(null)" {
		return ""
	}
	return s
}

// cleanPort coerces networksetup's unset sentinels ("(null)" and "0") to the
// empty string. Port 0 is never a real proxy port — networksetup uses it to
// mean "not configured".
func cleanPort(s string) string {
	s = strings.TrimSpace(s)
	if s == "(null)" || s == "0" {
		return ""
	}
	return s
}

func yesNo(s string) bool { return strings.EqualFold(strings.TrimSpace(s), "yes") }

func onOff(s string) bool { return strings.EqualFold(strings.TrimSpace(s), "on") }

func bool01(s string) bool {
	s = strings.TrimSpace(s)
	return s == "1" || strings.EqualFold(s, "true") || strings.EqualFold(s, "on")
}

// ParseProxyEntry parses the output of -getwebproxy / -getsecurewebproxy /
// -getsocksfirewallproxy into a ProxyEntry.
func ParseProxyEntry(out string) ProxyEntry {
	kv := parseKV(out)
	return ProxyEntry{
		Enabled:     yesNo(kv["enabled"]),
		Server:      cleanValue(kv["server"]),
		Port:        cleanPort(kv["port"]),
		AuthEnabled: bool01(kv["authenticated proxy enabled"]),
	}
}

// ParseAutoProxy parses -getautoproxyurl output.
func ParseAutoProxy(out string) AutoProxy {
	kv := parseKV(out)
	return AutoProxy{
		Enabled: yesNo(kv["enabled"]),
		URL:     cleanValue(kv["url"]),
	}
}

// ParseAutoDiscovery parses -getproxyautodiscovery output
// ("Auto Proxy Discovery: On"/"Off").
func ParseAutoDiscovery(out string) bool {
	kv := parseKV(out)
	if v, ok := kv["auto proxy discovery"]; ok {
		return onOff(v)
	}
	return false
}

// noBypassSentence is the single line networksetup prints when there are no
// bypass domains set, e.g. "There aren't any bypass domains set on Wi-Fi."
func noBypassSentence(out string) bool {
	out = strings.TrimSpace(out)
	if out == "" {
		return true
	}
	first := strings.Split(out, "\n")[0]
	return strings.Contains(strings.ToLower(first), "aren't any bypass domains")
}

// ParseBypass parses -getproxybypassdomains output into a domain list. The
// "no bypass domains set" sentence yields nil.
func ParseBypass(out string) []string {
	if noBypassSentence(out) {
		return nil
	}
	var domains []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			domains = append(domains, line)
		}
	}
	return domains
}

// PortString renders a numeric port for networksetup argv.
func PortString(port int) string { return strconv.Itoa(port) }
