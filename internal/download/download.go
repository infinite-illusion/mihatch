// Package download fetches the official Mihomo engine binary from the
// MetaCubeX/mihomo GitHub Releases for darwin/arm64.
//
// Mihomo publishes NO first-party checksum or signature asset. MiHatch therefore
// pins by release tag + canonical asset name over HTTPS, computes a local
// SHA-256 for tamper-evidence and reproducibility, and is explicit in its docs
// that this does NOT prove authenticity against a compromised release account.
package download

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// MetaCubeXRepo is the official upstream repository (owner/name).
const MetaCubeXRepo = "MetaCubeX/mihomo"

// DefaultMaxAssetBytes caps a single downloaded asset (~64 MiB; real builds are
// ~16 MiB, leaving headroom for toolchain growth).
const DefaultMaxAssetBytes int64 = 64 << 20

// Asset is a release asset of interest.
type Asset struct {
	Name string
	URL  string
	Size int64
}

// Release is the minimal release metadata MiHatch consumes.
type Release struct {
	Tag        string
	Prerelease bool
	Assets     []Asset
}

// Client downloads from GitHub Releases. It is built for flaky links: downloads
// run until complete (no wall-clock cap); if a connection goes silent
// (DeadConnTimeout — no bytes at all) it is abandoned and the transfer resumes
// from disk via HTTP Range, up to MaxAttempts times. A download that is making
// any progress is never interrupted.
type Client struct {
	HTTP                  *http.Client // for small JSON gets (whole-request timeout)
	UserAgent             string
	MaxAssetBytes         int64
	MaxAttempts           int           // download attempts before giving up
	DeadConnTimeout       time.Duration // abandon a connection only when zero bytes flow for this long
	ResponseHeaderTimeout time.Duration
}

// NewClient builds a Client with fault-tolerant defaults.
func NewClient(userAgent string) *Client {
	return &Client{
		HTTP:                  &http.Client{Timeout: 60 * time.Second},
		UserAgent:             userAgent,
		MaxAssetBytes:         DefaultMaxAssetBytes,
		MaxAttempts:           10,
		DeadConnTimeout:       60 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
}

// downloadTransport builds a transport with dial/header timeouts and no
// whole-request timeout (the stall reader governs liveness). It honors the
// standard HTTP_PROXY/HTTPS_PROXY env vars so a local circumvention proxy is
// used when present.
func (c *Client) downloadTransport() *http.Transport {
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	headerTimeout := c.ResponseHeaderTimeout
	if headerTimeout <= 0 {
		headerTimeout = 30 * time.Second
	}
	return &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: headerTimeout,
		ExpectContinueTimeout: 5 * time.Second,
		MaxIdleConnsPerHost:   4,
		Proxy:                 http.ProxyFromEnvironment,
	}
}

// LatestStable queries the latest non-prerelease Mihomo release.
func (c *Client) LatestStable(ctx context.Context) (Release, error) {
	url := "https://api.github.com/repos/" + MetaCubeXRepo + "/releases/latest"
	body, err := c.get(ctx, url, 4<<20)
	if err != nil {
		return Release{}, fmt.Errorf("query latest release: %w", err)
	}
	var raw struct {
		TagName    string `json:"tag_name"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return Release{}, fmt.Errorf("parse release JSON: %w", err)
	}
	if raw.TagName == "" {
		return Release{}, fmt.Errorf("release has no tag_name")
	}
	rel := Release{Tag: raw.TagName, Prerelease: raw.Prerelease}
	for _, a := range raw.Assets {
		rel.Assets = append(rel.Assets, Asset{Name: a.Name, URL: a.BrowserDownloadURL, Size: a.Size})
	}
	return rel, nil
}

// ExpectedAssetName returns the canonical asset name for a platform and tag.
// Mihomo names darwin/arm64 builds "mihomo-darwin-arm64-<tag>.gz" (standard
// build; Go-toolchain-pinned variants carry a -goNNN segment and are avoided).
func ExpectedAssetName(goos, goarch, tag string) (string, error) {
	if goos != "darwin" || goarch != "arm64" {
		return "", fmt.Errorf("unsupported platform %s/%s: MiHatch only supports darwin/arm64", goos, goarch)
	}
	return "mihomo-" + goos + "-" + goarch + "-" + tag + ".gz", nil
}

// SelectAsset picks the standard darwin/arm64 asset from a release, rejecting
// Go-toolchain-pinned (-goNNN) and compatible variants.
func SelectAsset(rel Release, goos, goarch string) (Asset, error) {
	if _, err := ExpectedAssetName(goos, goarch, rel.Tag); err != nil {
		return Asset{}, err
	}
	exact := "mihomo-" + goos + "-" + goarch + "-" + rel.Tag + ".gz"
	suffix := "-" + rel.Tag + ".gz"
	prefix := "mihomo-" + goos + "-" + goarch + "-"
	var fallback []Asset
	for _, a := range rel.Assets {
		if a.Name == exact {
			return a, nil
		}
		if strings.HasPrefix(a.Name, prefix) && strings.HasSuffix(a.Name, suffix) && !strings.Contains(a.Name, "-go") {
			fallback = append(fallback, a)
		}
	}
	if len(fallback) > 0 {
		return fallback[0], nil
	}
	names := make([]string, 0, len(rel.Assets))
	for _, a := range rel.Assets {
		names = append(names, a.Name)
	}
	return Asset{}, fmt.Errorf("no %s/%s asset in release %s; available: %s", goos, goarch, rel.Tag, strings.Join(names, ", "))
}

// get performs an HTTP GET with a User-Agent and a response-size cap, retrying
// transient failures a few times.
func (c *Client) get(ctx context.Context, url string, maxBody int64) ([]byte, error) {
	const maxAttempts = 4
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		if c.UserAgent != "" {
			req.Header.Set("User-Agent", c.UserAgent)
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		resp, err := c.HTTP.Do(req)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil || !c.sleep(ctx, backoff(attempt)) {
				break
			}
			continue
		}
		body, rerr := readCapped(resp, maxBody, url)
		_ = resp.Body.Close()
		if rerr == nil {
			return body, nil
		}
		lastErr = rerr
		if ctx.Err() != nil || !c.sleep(ctx, backoff(attempt)) {
			break
		}
	}
	return nil, fmt.Errorf("query %s after %d attempts: %w", redactURL(url), maxAttempts, lastErr)
}

func readCapped(resp *http.Response, maxBody int64, url string) ([]byte, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, redactURL(url))
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBody))
}

// DownloadToFile streams an asset to destPath with resume + retry, tolerating
// unstable connections. It resumes from whatever bytes are already on disk via
// HTTP Range (206), falls back to a full restart if the server ignores Range
// (200), and retries up to MaxAttempts when a read stalls (no bytes for
// DeadConnTimeout) or the connection drops. progress (if non-nil) shows a
// continuous bar across attempts.
func (c *Client) DownloadToFile(ctx context.Context, url, destPath string, progress io.Writer) (int64, error) {
	maxAttempts := c.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	client := &http.Client{Transport: c.downloadTransport()} // no Timeout: stall reader governs

	var pw *progressWriter
	if progress != nil {
		pw = newProgressWriter(progress, 0)
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		offset := fileSize(destPath)
		received, fullSize, err := c.downloadAttempt(ctx, client, url, destPath, offset, pw)
		if err == nil {
			if pw != nil {
				pw.setRange(0, fullSize)
				pw.finish()
			}
			return offset + received, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break // caller cancelled (e.g. Ctrl-C)
		}
		if attempt < maxAttempts {
			if pw != nil {
				pw.note("retry %d/%d after %v — resuming from %s", attempt+1, maxAttempts, shortErr(err), humanBytes(fileSize(destPath)))
			}
			if !c.sleep(ctx, backoff(attempt)) {
				break
			}
		}
	}
	if pw != nil {
		pw.finish()
	}
	return 0, fmt.Errorf("download failed after %d attempts: %w", maxAttempts, lastErr)
}

// downloadAttempt performs one (possibly resumed) download. offset is the bytes
// already on disk; the request asks for bytes offset..end via Range.
func (c *Client) downloadAttempt(ctx context.Context, client *http.Client, url, destPath string, offset int64, pw *progressWriter) (received, fullSize int64, err error) {
	stallCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	req, err := http.NewRequestWithContext(stallCtx, http.MethodGet, url, nil)
	if err != nil {
		return 0, 0, err
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// Server ignored Range (or offset==0): start over from the beginning.
		offset = 0
	case http.StatusPartialContent:
		// Resume path: append.
	default:
		return 0, 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	fullSize = fullContentSize(resp)
	if fullSize > 0 && fullSize > c.MaxAssetBytes {
		return 0, fullSize, fmt.Errorf("asset size %d exceeds limit %d", fullSize, c.MaxAssetBytes)
	}
	if pw != nil {
		pw.setRange(offset, fullSize)
	}

	appendMode := offset > 0 && resp.StatusCode == http.StatusPartialContent
	flag := os.O_WRONLY | os.O_CREATE
	if appendMode {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}
	f, err := os.OpenFile(destPath, flag, 0o600)
	if err != nil {
		return 0, fullSize, fmt.Errorf("create %s: %w", destPath, err)
	}
	defer f.Close()

	sr := newStallReader(resp.Body, c.DeadConnTimeout, cancel)
	defer sr.Close()

	limit := c.MaxAssetBytes - offset + 1
	sink := io.Writer(f)
	if pw != nil {
		sink = io.MultiWriter(f, pw)
	}
	n, err := io.Copy(sink, io.LimitReader(sr, limit))
	if err != nil {
		return n, fullSize, err
	}
	if offset+n > c.MaxAssetBytes {
		return n, fullSize, fmt.Errorf("download exceeded size limit %d", c.MaxAssetBytes)
	}
	if err := f.Sync(); err != nil {
		return n, fullSize, fmt.Errorf("fsync download: %w", err)
	}
	if err := f.Close(); err != nil {
		return n, fullSize, fmt.Errorf("close download: %w", err)
	}
	return n, fullSize, nil
}

// fullContentSize derives the full asset size from a response: for 206 it parses
// the Content-Range total; otherwise it uses Content-Length.
func fullContentSize(resp *http.Response) int64 {
	if resp.StatusCode == http.StatusPartialContent {
		if cr := resp.Header.Get("Content-Range"); cr != "" {
			if i := strings.LastIndex(cr, "/"); i >= 0 {
				if n, err := strconv.ParseInt(strings.TrimSpace(cr[i+1:]), 10, 64); err == nil {
					return n
				}
			}
		}
	}
	return resp.ContentLength
}

// fileSize returns the current size of a file, or 0 if missing.
func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// backoff returns a short, capped delay before retry attempt n.
func backoff(n int) time.Duration {
	d := time.Duration(n) * time.Second
	if d > 10*time.Second {
		d = 10 * time.Second
	}
	return d
}

// sleep waits for d (or until ctx is cancelled). It reports false if ctx was
// cancelled before the sleep completed.
func (c *Client) sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// shortErr trims an error to one line for retry notices.
func shortErr(err error) string {
	s := err.Error()
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return s
}

// redactURL keeps URLs out of error messages at the scheme+host granularity.
func redactURL(url string) string {
	if i := strings.Index(url, "/releases/"); i > 0 {
		return url[:i] + "/releases/..."
	}
	return url
}
