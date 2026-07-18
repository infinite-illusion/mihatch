package download

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func fixtureRelease() Release {
	return Release{
		Tag:        "v1.19.28",
		Prerelease: false,
		Assets: []Asset{
			{Name: "mihomo-darwin-arm64-v1.19.28.gz", URL: "https://x/darwin-arm64.gz", Size: 15963072},
			{Name: "mihomo-darwin-arm64-go120-v1.19.28.gz", URL: "https://x/go120.gz", Size: 16000000},
			{Name: "mihomo-darwin-arm64-go124-v1.19.28.gz", URL: "https://x/go124.gz", Size: 16000000},
			{Name: "mihomo-darwin-amd64-compatible-v1.19.28.gz", URL: "https://x/amd64.gz", Size: 17000000},
			{Name: "mihomo-linux-arm64-v1.19.28.gz", URL: "https://x/linux.gz", Size: 15000000},
			{Name: "version.txt", URL: "https://x/version.txt", Size: 9},
		},
	}
}

func TestSelectAssetStandard(t *testing.T) {
	t.Parallel()
	rel := fixtureRelease()
	a, err := SelectAsset(rel, "darwin", "arm64")
	if err != nil {
		t.Fatalf("SelectAsset: %v", err)
	}
	if a.Name != "mihomo-darwin-arm64-v1.19.28.gz" {
		t.Fatalf("selected %q, want standard build (no -goNNN)", a.Name)
	}
}

func TestSelectAssetRejectsUnsupportedArch(t *testing.T) {
	t.Parallel()
	rel := fixtureRelease()
	if _, err := SelectAsset(rel, "linux", "arm64"); err == nil {
		t.Fatal("linux/arm64 must be rejected")
	}
	if _, err := SelectAsset(rel, "darwin", "amd64"); err == nil {
		t.Fatal("darwin/amd64 must be rejected")
	}
}

func TestSelectAssetFallsBackWhenNoStandard(t *testing.T) {
	t.Parallel()
	// No standard asset, but a non-go variant exists.
	rel := Release{Tag: "v1.19.28", Assets: []Asset{
		{Name: "mihomo-darwin-arm64-go120-v1.19.28.gz", URL: "u1"},
		{Name: "mihomo-darwin-arm64-custom-v1.19.28.gz", URL: "u2"}, // no "go" substring
	}}
	a, err := SelectAsset(rel, "darwin", "arm64")
	if err != nil {
		t.Fatalf("expected fallback: %v", err)
	}
	if a.Name != "mihomo-darwin-arm64-custom-v1.19.28.gz" {
		t.Fatalf("fallback picked %q", a.Name)
	}
}

func TestExpectedAssetName(t *testing.T) {
	t.Parallel()
	got, err := ExpectedAssetName("darwin", "arm64", "v1.19.28")
	if err != nil {
		t.Fatal(err)
	}
	if got != "mihomo-darwin-arm64-v1.19.28.gz" {
		t.Fatalf("got %q", got)
	}
}

func TestLatestStableViaHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("User-Agent should be set")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v1.19.28","prerelease":false,"assets":[{"name":"mihomo-darwin-arm64-v1.19.28.gz","browser_download_url":"https://x/a.gz","size":1}]}`))
	}))
	defer srv.Close()

	c := NewClient("mihatch/test")
	// Override the URL by pointing the client at the test server through a
	// custom getter: re-implement the call path via a direct get.
	body, err := c.get(context.Background(), srv.URL+"/releases/latest", 1<<20)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !bytes.Contains(body, []byte("v1.19.28")) {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestDownloadToFileAndGunzip(t *testing.T) {
	t.Parallel()
	// Build a real gzip of a fake binary payload.
	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	payload := bytes.Repeat([]byte("mihomo-binary-"), 1000)
	if _, err := gw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(gzBuf.Bytes())
	}))
	defer srv.Close()

	dir := t.TempDir()
	c := NewClient("mihatch/test")
	gzPath := filepath.Join(dir, "asset.gz")
	n, err := c.DownloadToFile(context.Background(), srv.URL, gzPath, nil)
	if err != nil {
		t.Fatalf("DownloadToFile: %v", err)
	}
	if n != int64(gzBuf.Len()) {
		t.Fatalf("downloaded %d want %d", n, gzBuf.Len())
	}
	if fi, _ := os.Stat(gzPath); fi.Mode().Perm() != 0o600 {
		t.Fatalf("download perm %o want 0600", fi.Mode().Perm())
	}

	binPath := filepath.Join(dir, "mihomo")
	if err := GunzipFile(gzPath, binPath, DefaultMaxBinaryBytes); err != nil {
		t.Fatalf("GunzipFile: %v", err)
	}
	got, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("gunzip content mismatch (len %d vs %d)", len(got), len(payload))
	}
	if fi, _ := os.Stat(binPath); fi.Mode().Perm() != 0o755 {
		t.Fatalf("binary perm %o want 0755", fi.Mode().Perm())
	}
}

func TestGunzipRejectsBadMagic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "notgz")
	if err := os.WriteFile(src, []byte("plain text"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := GunzipFile(src, filepath.Join(dir, "out"), 1<<20); err == nil {
		t.Fatal("expected bad-magic error")
	}
}

func TestGunzipSizeCap(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	_, _ = gw.Write(bytes.Repeat([]byte{0}, 1000))
	_ = gw.Close()
	src := filepath.Join(dir, "big.gz")
	_ = os.WriteFile(src, gzBuf.Bytes(), 0o600)
	if err := GunzipFile(src, filepath.Join(dir, "out"), 100); err == nil {
		t.Fatal("expected size-cap error")
	}
}

func TestSHA256File(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	_ = os.WriteFile(p, []byte("hello"), 0o600)
	d, err := SHA256File(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(d) != 64 {
		t.Fatalf("digest len %d want 64", len(d))
	}
}

func TestDownloadResumesViaRange(t *testing.T) {
	t.Parallel()
	body := bytes.Repeat([]byte("AB"), 500) // 1000 bytes
	half := 400

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rng := r.Header.Get("Range")
		if rng == "" {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			_, _ = w.Write(body)
			return
		}
		// Expect "bytes=400-"
		start := half
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(body)-1, len(body)))
		w.Header().Set("Content-Length", strconv.Itoa(len(body)-start))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(body[start:])
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "asset")
	// Pre-write the first 400 bytes to simulate a partial download on disk.
	if err := os.WriteFile(dest, body[:half], 0o600); err != nil {
		t.Fatal(err)
	}

	c := NewClient("mihatch/test")
	n, err := c.DownloadToFile(context.Background(), srv.URL, dest, nil)
	if err != nil {
		t.Fatalf("DownloadToFile: %v", err)
	}
	if n != int64(len(body)) {
		t.Fatalf("reported %d bytes, want %d", n, len(body))
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("resumed file mismatch (len %d vs %d)", len(got), len(body))
	}
}

func TestDownloadRetriesOnTransientFailure(t *testing.T) {
	t.Parallel()
	body := []byte("the quick brown fox")
	var attempts int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			http.Error(w, "transient", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "asset")
	c := NewClient("mihatch/test")
	n, err := c.DownloadToFile(context.Background(), srv.URL, dest, nil)
	if err != nil {
		t.Fatalf("DownloadToFile: %v", err)
	}
	if n != int64(len(body)) {
		t.Fatalf("reported %d bytes, want %d", n, len(body))
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, body) {
		t.Fatalf("content mismatch")
	}
	if attempts < 2 {
		t.Fatalf("expected at least 2 attempts, got %d", attempts)
	}
}

func TestDownloadGivesUpAfterMaxAttempts(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "always fails", http.StatusInternalServerError)
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "asset")
	c := NewClient("mihatch/test")
	c.MaxAttempts = 3 // keep the test quick
	_, err := c.DownloadToFile(context.Background(), srv.URL, dest, nil)
	if err == nil || !strings.Contains(err.Error(), "after 3 attempts") {
		t.Fatalf("expected give-up error, got %v", err)
	}
}
