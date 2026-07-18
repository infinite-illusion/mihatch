# Security & isolation

MiHatch is built to be safe to run on a developer machine that already depends
on Clash Verge Rev for network. The design boundaries below are enforced in code
and tested.

## What MiHatch never does

- **No `sudo` / admin rights.** It is entirely user-level.
- **No TUN, utun, or default-route changes.** Only the loopback mixed proxy.
- **No system DNS changes.** Mihomo's internal resolver config may be kept (for
  proxy connections), but `dns.listen` is removed so Mihomo never opens a DNS
  server, and MiHatch never points the system DNS at anything.
- **No Clash Verge Service.** No install/uninstall of any system service.
- **No controlling or stopping Clash Verge Rev.** MiHatch makes a one-time,
  read-only copy of prod's runtime YAML and then fully detaches.
- **No background daemon / supervisor / launchd.** `mihatch` runs and exits.

## Isolation from Clash Verge Rev

- MiHatch's mixed port (`17890`) differs from Clash Verge Rev's defaults.
- All controllers (`external-controller`, `-unix`, `-tls`, `secret`), external
  UI, extra listeners, tunnels, and TUN are stripped from the imported config.
- Provider/rule-provider paths are rewritten under `.mihatch/`; MiHatch never
  references the Clash Verge Rev data directory at runtime (no `SAFE_PATHS`
  widening).
- Only the **production** App ID is read; the `.dev` App ID is never selected
  (exact-equality matching — the dev id is a prefix-extension of prod, so prefix
  matching would be unsafe).

## System-proxy ownership safety

- A **complete** snapshot (server, port, auth flag, PAC URL+enabled, WPAD,
  bypass domains) is taken before any change — not just on/off flags.
- MiHatch **refuses** services with an authenticated proxy unless `--force` is
  given, because Keychain credentials cannot be read or restored.
- **Compare-before-restore:** on `pause`/`down`, a service whose settings were
  changed by another application is **abandoned**, never overwritten.
- All `networksetup` invocations use argv directly — **no shell**, no string
  interpolation — so service names with spaces or special characters are passed
  safely.

## Sensitive data handling

- Subscription URLs are reduced to `scheme://host[:port]/path` (query tokens and
  userinfo stripped) wherever they could reach logs or output.
- `proxy-provider.*.url`, `geox-url`, and secret-shaped keys (`secret`,
  `password`, `token`, `uuid`, …) are redacted by `internal/redact` before any
  display. (Node credentials legitimately remain inside `.mihatch/config.yaml`,
  which is mode `0600` — they are **never** written to `mihomo.log` by MiHatch.)
- `mihomo.log` captures Mihomo's own stdout/stderr, which may include node names
  and domains. **Review it before sharing.**
- No telemetry, no log upload, no remote control API.

## File permissions

`.mihatch/` and its subdirectories are `0700`; config/state files are `0600`;
the engine binary is `0755`. Downloads go through temp files that are `0600`.

## Integrity of the downloaded engine

Mihomo publishes **no checksum or signature** asset. MiHatch:

- pins by release **tag + canonical asset name** over HTTPS,
- rejects Go-toolchain-pinned / `-compatible` variants for darwin/arm64,
- records a local **SHA-256** of the installed binary for tamper-evidence and
  reproducibility.

This protects against transport corruption and accidental asset mismatch, but
does **not** prove authenticity against a compromised GitHub release account.

## Tests must not touch the real system

The test suite uses a fake command runner and never invokes real `networksetup`,
never starts a real Mihomo in unit/integration tests, and never modifies the
system proxy. Real end-to-end checks (which do touch the system proxy) are gated
behind an explicit env var and are never run by CI.
