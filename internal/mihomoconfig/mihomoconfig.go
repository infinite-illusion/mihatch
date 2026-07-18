// Package mihomoconfig reads a source Mihomo configuration (Clash Verge Rev
// prod runtime, a local file), applies MiHatch's isolation overrides, migrates
// referenced provider files into .mihatch/, and emits a self-contained runtime
// config that Mihomo can run with -d .mihatch -f .mihatch/config.yaml.
//
// Overrides are applied structurally on a parsed YAML node tree — never by
// string substitution — and remove every inbound listener, controller, TUN, and
// DNS listener that could conflict with Clash Verge Rev or touch the system.
package mihomoconfig

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Options configures a Purify run.
type Options struct {
	// MixedPort is the MiHatch reserved loopback mixed proxy port.
	MixedPort int
	// Source is the raw source YAML bytes.
	Source []byte
	// SourceHomeDir is the HomeDir (-d) the source config was authored against
	// (CVR's data dir). Relative provider paths resolve against it.
	SourceHomeDir string
	// ProvidersDir is the destination for migrated provider caches (.mihatch/providers).
	ProvidersDir string
	// RulesDir is the destination for migrated rule-provider caches (.mihatch/rules).
	RulesDir string
}

// keys removed wholesale from the source config.
var removedKeys = []string{
	// inbound ports besides mixed-port
	"port", "socks-port", "redir-port", "tproxy-port",
	// extra inbound listeners
	"listeners", "tunnels",
	// TUN / system-level features
	"tun",
	// controllers and external surfaces
	"external-controller", "external-controller-unix", "external-controller-tls",
	"external-controller-cors", "external-controller-pipe", "external-controller-routing-mark",
	"external-controller-unix-perm",
	"secret",
	"external-ui", "external-ui-name", "external-ui-url",
	// inbound proxy auth (loopback needs none; would break connectivity check)
	"authentication",
	// CVR-specific selection persistence location is irrelevant here
	"clash-for-android",
}

// Purify parses the source, applies overrides, migrates provider paths, and
// returns the purified YAML bytes. It may create files under ProvidersDir /
// RulesDir as a side effect.
func Purify(opts Options) ([]byte, error) {
	if opts.MixedPort <= 0 {
		return nil, errors.New("mixed port required")
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(opts.Source, &doc); err != nil {
		return nil, fmt.Errorf("parse source yaml: %w", err)
	}
	root, err := mappingOf(&doc)
	if err != nil {
		return nil, err
	}

	for _, k := range removedKeys {
		removeKey(root, k)
	}

	setInt(root, "mixed-port", opts.MixedPort)
	setBool(root, "allow-lan", false)
	setStr(root, "bind-address", "127.0.0.1")

	// Keep the internal DNS resolver but remove the local DNS listener so
	// Mihomo never opens a DNS server port.
	if dns, _ := findValue(root, "dns"); dns != nil && dns.Kind == yaml.MappingNode {
		removeKey(dns, "listen")
	}

	if err := migrateProviders(root, "proxy-providers", opts.SourceHomeDir, opts.ProvidersDir, "providers"); err != nil {
		return nil, err
	}
	if err := migrateProviders(root, "rule-providers", opts.SourceHomeDir, opts.RulesDir, "rules"); err != nil {
		return nil, err
	}

	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, fmt.Errorf("encode purified config: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// --- yaml.Node helpers ---

func mappingOf(doc *yaml.Node) (*yaml.Node, error) {
	if doc.Kind == 0 {
		// Empty document.
		return nil, errors.New("empty source config")
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, errors.New("source is not a yaml document")
	}
	m := doc.Content[0]
	if m.Kind != yaml.MappingNode {
		return nil, errors.New("source root is not a mapping")
	}
	return m, nil
}

// findValue returns the value node and the key's index in the mapping Content.
func findValue(m *yaml.Node, key string) (*yaml.Node, int) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1], i
		}
	}
	return nil, -1
}

func removeKey(m *yaml.Node, key string) {
	if _, i := findValue(m, key); i >= 0 {
		m.Content = append(m.Content[:i], m.Content[i+2:]...)
	}
}

func setStr(m *yaml.Node, key, value string) {
	if v, i := findValue(m, key); v != nil {
		m.Content[i+1] = scalar(value, "!!str")
		return
	}
	m.Content = append(m.Content,
		scalar(key, "!!str"),
		scalar(value, "!!str"),
	)
}

func setInt(m *yaml.Node, key string, value int) {
	if v, i := findValue(m, key); v != nil {
		m.Content[i+1] = scalar(fmt.Sprintf("%d", value), "!!int")
		return
	}
	m.Content = append(m.Content,
		scalar(key, "!!str"),
		scalar(fmt.Sprintf("%d", value), "!!int"),
	)
}

func setBool(m *yaml.Node, key string, value bool) {
	s := "false"
	if value {
		s = "true"
	}
	if v, i := findValue(m, key); v != nil {
		m.Content[i+1] = scalar(s, "!!bool")
		return
	}
	m.Content = append(m.Content,
		scalar(key, "!!str"),
		scalar(s, "!!bool"),
	)
}

func scalar(value, tag string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value}
}

// --- provider path migration ---

func migrateProviders(root *yaml.Node, topKey, sourceHome, destDir, relPrefix string) error {
	providers, _ := findValue(root, topKey)
	if providers == nil || providers.Kind != yaml.MappingNode {
		return nil
	}
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", destDir, err)
	}
	for i := 0; i+1 < len(providers.Content); i += 2 {
		name := providers.Content[i].Value
		prov := providers.Content[i+1]
		if prov == nil || prov.Kind != yaml.MappingNode {
			continue
		}
		if err := migrateOneProvider(prov, name, sourceHome, destDir, relPrefix); err != nil {
			return fmt.Errorf("%s.%s: %w", topKey, name, err)
		}
	}
	return nil
}

func migrateOneProvider(prov *yaml.Node, name, sourceHome, destDir, relPrefix string) error {
	typ := "http"
	if t, _ := findValue(prov, "type"); t != nil {
		typ = strings.ToLower(strings.TrimSpace(t.Value))
	}
	sanitized := sanitizeName(name)

	if typ == "file" {
		// Must copy the referenced file into .mihatch; CVR's data dir must not
		// be referenced at runtime.
		pathNode, _ := findValue(prov, "path")
		if pathNode == nil || strings.TrimSpace(pathNode.Value) == "" {
			return errors.New("type=file provider requires a path")
		}
		src := resolveUnder(pathNode.Value, sourceHome)
		dest := filepath.Join(destDir, sanitized)
		if err := copyFileCapped(src, dest, 64<<20); err != nil {
			return fmt.Errorf("copy %s: %w", src, err)
		}
		setStr(prov, "path", relPrefix+"/"+sanitized)
		return nil
	}

	// http (or unspecified): cache must land under .mihatch. Rewrite path to a
	// relative location so mihomo resolves it under its HomeDir (.mihatch). If
	// the source already cached the provider at its original path, copy that
	// cache in so mihomo -t (and first up) need not re-download the subscription.
	rel := relPrefix + "/" + sanitized + ".yaml"
	if pathNode, _ := findValue(prov, "path"); pathNode != nil && strings.TrimSpace(pathNode.Value) != "" {
		src := resolveUnder(pathNode.Value, sourceHome)
		if fi, err := os.Stat(src); err == nil && !fi.IsDir() {
			dest := filepath.Join(destDir, sanitized+".yaml")
			if cerr := copyFileCapped(src, dest, 64<<20); cerr == nil {
				setStr(prov, "path", rel)
				return nil
			}
		}
	}
	setStr(prov, "path", rel)
	return nil
}

// resolveUnder resolves a possibly-relative path against sourceHome.
func resolveUnder(p, sourceHome string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(sourceHome, p)
}

// sanitizeName turns a provider name into a safe file basename (no separators).
func sanitizeName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "provider"
	}
	return strings.ToLower(b.String())
}

func copyFileCapped(src, dst string, maxBytes int64) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	n, err := io.Copy(out, io.LimitReader(in, maxBytes+1))
	if err != nil {
		return err
	}
	if n > maxBytes {
		return fmt.Errorf("file exceeds %d bytes", maxBytes)
	}
	return out.Sync()
}
