// Package redact scrubs sensitive data before it reaches logs, status output,
// error messages, or user-facing summaries.
//
// MiHatch never prints subscription URLs (with query tokens), secrets,
// passwords, UUIDs, or private keys in normal output. Redaction is applied
// defensively: when in doubt, output the placeholder rather than the value.
package redact

import (
	"net/url"
	"regexp"
	"strings"
)

// urlRe matches absolute http(s) URLs as they appear in free text.
var urlRe = regexp.MustCompile(`https?://[^\s'"<>]+`)

// Multiline scrubs a free-form block of text (e.g. mihomo stderr) by reducing
// any embedded http(s) URL to scheme+host. Subscription URLs that leak into
// parse errors are the main concern; this keeps the diagnostic readable without
// exposing tokens.
func Multiline(s string) string {
	return urlRe.ReplaceAllStringFunc(s, func(u string) string { return URL(u) })
}

// Secret is the placeholder substituted for sensitive values.
const Secret = "***REDACTED***"

// secretKeyFragments are lowercased substrings that mark a key as holding an
// opaque secret (full redaction). URL-shaped keys are handled separately by
// isURLKey so their value can be reduced to scheme+host rather than hidden.
var secretKeyFragments = []string{
	"secret", "password", "passwd", "token", "apikey", "api-key", "api_key",
	"authorization", "auth", "credential", "privatekey", "private-key",
	"private_key", "passphrase", "uuid", "subscription",
}

// URL redacts a raw URL string for logging. It keeps scheme, host, port and a
// coarse path, and strips the query string and user-info entirely. If parsing
// fails, it returns the input with a trailing marker so the caller knows it was
// not safely representable.
func URL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		// Not a usable absolute URL. Fall back to redacting aggressively.
		return "[unparseable-url]"
	}
	u.User = nil    // drop userinfo
	u.RawQuery = "" // drop query (tokens live here)
	u.Fragment = "" // drop fragment
	// Collapse long, token-shaped path segments.
	if u.Path != "" {
		u.Path = collapseTokenPath(u.Path)
		u.RawPath = ""
	}
	return u.String()
}

// collapseTokenPath turns long opaque path segments into a placeholder while
// keeping short, meaningful ones (e.g. "/api/config" survives).
func collapseTokenPath(p string) string {
	parts := strings.Split(p, "/")
	for i, seg := range parts {
		if seg == "" {
			continue
		}
		if looksLikeToken(seg) {
			parts[i] = "tok"
		}
	}
	return strings.Join(parts, "/")
}

// looksLikeToken reports whether a path segment resembles a bearer token,
// hex/base64 blob, UUID, or otherwise opaque credential fragment.
func looksLikeToken(s string) bool {
	if len(s) >= 24 {
		return true
	}
	if isUUID(s) {
		return true
	}
	// long run of hex/base64 with no separators
	letters := 0
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
			letters++
		default:
			return false
		}
	}
	return letters >= 16
}

// isUUID matches the canonical 8-4-4-4-12 hex form.
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				return false
			}
		}
	}
	return true
}

// SecretValue redacts an opaque secret string. A short, clearly non-secret
// value (empty) passes through; anything else is replaced.
func SecretValue(s string) string {
	if s == "" {
		return ""
	}
	return Secret
}

// Field redacts a single key/value pair for output:
//   - secret-shaped keys (secret/password/token/uuid/...) become Secret;
//   - url-shaped keys (proxy-providers.*.url, geox-url, ...) are reduced to
//     scheme+host via URL, dropping query tokens and userinfo;
//   - otherwise the value is returned unchanged.
func Field(key, value string) string {
	if isSecretKey(key) {
		return Secret
	}
	if isURLKey(key) {
		return URL(value)
	}
	return value
}

// IsSensitiveKey reports whether a key name holds sensitive data (a secret or a
// URL that may carry tokens). Used to decide whether a value should be redacted
// before display or logging.
func IsSensitiveKey(key string) bool {
	return isSecretKey(key) || isURLKey(key)
}

// isSecretKey reports whether the key denotes an opaque secret.
func isSecretKey(key string) bool {
	k := strings.ToLower(key)
	for _, frag := range secretKeyFragments {
		if strings.Contains(k, frag) {
			return true
		}
	}
	return false
}

// isURLKey reports whether the key's leaf field name denotes a URL value
// (subscription / provider / geo URLs). The leaf is the segment after the last
// dot so that e.g. "proxy-providers.foo.url" and "geox-url" both qualify while
// "url-test" (a group type used as a value, not a key) does not.
func isURLKey(key string) bool {
	leaf := strings.ToLower(key)
	if i := strings.LastIndex(leaf, "."); i >= 0 {
		leaf = leaf[i+1:]
	}
	return strings.Contains(leaf, "url")
}

// Path redacts a filesystem path by replacing the user home prefix with "~".
// This hides the account name while preserving enough structure to be useful
// in diagnostics.
func Path(p, home string) string {
	if home == "" || p == "" {
		return p
	}
	if p == home {
		return "~"
	}
	rest := strings.TrimPrefix(p, home)
	if len(rest) == len(p) {
		// not under home
		return p
	}
	if !strings.HasPrefix(rest, string('/')) && rest != "" {
		return p
	}
	return "~" + rest
}

// HostOf returns scheme://host[:port] for a URL, dropping everything else. Used
// for terse provenance display.
func HostOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "[unparseable-url]"
	}
	u.User = nil
	u.Path = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
