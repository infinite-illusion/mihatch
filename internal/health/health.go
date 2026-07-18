// Package health probes whether the managed Mihomo is actually serving: a TCP
// dial to the mixed port, and a real HTTP request through the proxy to a
// generate_204 endpoint. "the process exists" is never treated as "the proxy
// works".
package health

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// DefaultProbeURLs are generate_204 endpoints used for connectivity checks.
var DefaultProbeURLs = []string{
	"https://www.gstatic.com/generate_204",
	"https://cp.cloudflare.com/generate_204",
}

// Result of a proxy connectivity probe.
type Result struct {
	OK      bool
	Latency time.Duration
	UsedURL string
	Status  int
}

// Prober checks engine liveness.
type Prober interface {
	PortListening(ctx context.Context, port int) bool
	ProxyOK(ctx context.Context, port int, urls []string, timeout time.Duration) Result
}

// Real uses the network. The HTTP client honors the standard *_PROXY env vars
// only when WithEnvProxy is set; by default it dials the MiHatch proxy directly
// so a check can never loop through a not-yet-started MiHatch.
type Real struct {
	HTTP            *http.Client
	PortDialTimeout time.Duration
}

// NewReal builds a Real prober.
func NewReal() *Real {
	return &Real{HTTP: &http.Client{}, PortDialTimeout: time.Second}
}

// PortListening reports whether 127.0.0.1:port accepts a TCP connection.
func (r *Real) PortListening(ctx context.Context, port int) bool {
	d := r.PortDialTimeout
	if d <= 0 {
		d = time.Second
	}
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), d)
	if err != nil {
		return false
	}
	_ = c.Close()
	_ = ctx
	return true
}

// ProxyOK issues an HTTP GET through the MiHatch mixed proxy to each URL until
// one returns 2xx (generate_204 returns 204). A fresh transport is built per
// call so the probe always targets the local proxy regardless of env vars.
func (r *Real) ProxyOK(ctx context.Context, port int, urls []string, timeout time.Duration) Result {
	if len(urls) == 0 {
		urls = DefaultProbeURLs
	}
	proxyURL := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", port)}
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 http.ProxyURL(proxyURL),
			DisableKeepAlives:     true,
			ResponseHeaderTimeout: timeout,
		},
	}
	for _, u := range urls {
		start := time.Now()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		_ = resp.Body.Close()
		lat := time.Since(start)
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return Result{OK: true, Latency: lat, UsedURL: u, Status: resp.StatusCode}
		}
	}
	return Result{OK: false}
}

// Fake is a test prober with scripted results.
type Fake struct {
	Listening   bool
	ProxyResult Result
}

func (f *Fake) PortListening(context.Context, int) bool                      { return f.Listening }
func (f *Fake) ProxyOK(context.Context, int, []string, time.Duration) Result { return f.ProxyResult }
