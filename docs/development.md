# Development

## Build & verify

or via the [justfile](../justfile): `just build`, `just test`, `just check`
(runs fmt + vet + test + race + build). `just` with no args lists all recipes.
The raw commands are:

```bash
gofmt -w .
go vet ./...
go test ./...
go test -race ./...
GOOS=darwin GOARCH=arm64 go build -o mihatch ./cmd/mihatch
```

## Layout

See [architecture.md](architecture.md). One responsibility per package; the CLI
layer is a thin parser over `internal/app`. The app layer depends only on
domain packages and injectable interfaces — never on `os/exec` directly for the
parts that matter for tests.

## Testing without touching the real system

The single most important property of the test suite: **it never modifies the
real system proxy and never starts a real Mihomo.** This is achieved with:

- `internal/runner.Fake` — a scripted command runner. `internal/proxy` and the
  app integration tests script `networksetup`/`route` outputs from it, so the
  real `networksetup` is never invoked.
- `internal/mihomo.FakeManager` — replaces process start/stop/identity.
- `internal/health.Fake` — replaces port/proxy probing.
- App `FetchLatest`/`DownloadAsset` function fields — replace release fetch.

The app integration test (`internal/app/app_test.go`) drives a full
`init → up → pause → resume → down` lifecycle with fakes and asserts on state,
recorded `networksetup` argv, and manager calls.

## Real end-to-end (opt-in, manual)

There is **no** automated E2E that touches the real system proxy. If you want to
exercise the real path on your own machine, do it manually and carefully:

```bash
# from a throwaway project dir:
MIHATCH_ROOT=/tmp/mh-e2e ./mihatch init
MIHATCH_ROOT=/tmp/mh-e2e ./mihatch up
networksetup -getwebproxy "Wi-Fi"   # confirm 127.0.0.1:17890
MIHATCH_ROOT=/tmp/mh-e2e ./mihatch down
networksetup -getwebproxy "Wi-Fi"   # confirm restored
```

This modifies your real system proxy — only run it when you can afford to, and
always verify the proxy is restored afterward.
