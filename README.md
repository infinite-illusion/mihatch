# MiHatch

A Mihomo escape hatch for macOS network-client development.

MiHatch runs a single, isolated, user-level [Mihomo](https://github.com/MetaCubeX/mihomo) proxy on Apple Silicon macOS so that **Clash Verge Rev (prod) can be fully exited while you develop the dev build**, without losing outbound network access.

It is deliberately small: **no TUN, no DNS hijacking, no root service, no launchd, no supervisor, no version manager**. Everything MiHatch owns lives in one project-local directory (`.mihatch/`); deleting that directory after `mihatch down` is a complete cleanup.

---

## What it is for

When you develop Clash Verge Rev itself, the prod app and the dev build fight over the same machine-level resources (Mihomo ports, system proxy, TUN, DNS). MiHatch gives you a standalone proxy you fully control, so you can:

1. Quit Clash Verge Rev **prod**.
2. Use MiHatch for your own outbound traffic.
3. Launch the **dev** build and test its system-proxy / TUN behavior without conflict.
4. Pause MiHatch's system-proxy ownership while you test dev, then resume.

MiHatch **never** controls, stops, or reads private state from Clash Verge Rev. It only makes a one-time, read-only copy of prod's final runtime config (`clash-verge.yaml`).

---

## Requirements

- macOS on **Apple Silicon** (`darwin/arm64`). Intel Macs are not supported.
- Go 1.26+ to build.
- Clash Verge Rev (prod) installed, with the profile you want already selected and refreshed — *or* a local Mihomo YAML.

No `sudo`, no admin rights, no system DNS/route/TUN changes.

## Install

```bash
git clone <this repo> && cd mihatch
just build                 # or: go build -o mihatch ./cmd/mihatch
# optionally: mv mihatch /usr/local/bin/
```

MiHatch runs against the **current directory**: it creates `./.mihatch/` next to where you invoke it. Run it from your project directory (or set `MIHATCH_ROOT`, or pass `--root`).

---

## Quick start

```bash
# 1. Import prod's runtime config + install the Mihomo engine into .mihatch/
mihatch init

# 2. Start the proxy and take over the system proxy
mihatch up

# 3. Check it's healthy and owning the proxy
mihatch status
#   state:        active
#   proxy-owned:  true
```

Now Safari, Chrome, `curl`, `git`, `pnpm`, … all go out through `127.0.0.1:17890`. Quit Clash Verge Rev prod — you still have network.

### Testing Clash Verge Rev dev

```bash
mihatch pause     # release the system proxy; MiHatch's Mihomo keeps running
# ... launch the dev build, test its system proxy / TUN ...
mihatch resume    # re-acquire the system proxy with a fresh snapshot
```

If the dev build (or any other tool) changed the system proxy while MiHatch was paused, MiHatch **will not overwrite it** — see [Ownership & safety](#ownership--safety).

### Stop

```bash
mihatch down      # restore the original proxy (if still ours) and stop Mihomo
```

---

## Commands

| Command | What it does |
|---|---|
| `mihatch init` | Install the Mihomo engine, import & purify the source config, write `.mihatch/config.yaml` + `state.json`. Does **not** start Mihomo or touch the proxy. |
| `mihatch sync` | Re-import the source config and atomically refresh `.mihatch/config.yaml`. On validation failure it keeps the old config. |
| `mihatch up` | Validate config → start Mihomo (background, detached) → wait for the mixed proxy to serve → take over the system proxy. Idempotent. |
| `mihatch status` | Show live lifecycle state and liveness. Use `--json` for machine-readable output. |
| `mihatch pause` | Release the system proxy but keep Mihomo running. |
| `mihatch resume` | Confirm Mihomo is healthy, then re-acquire the system proxy with a fresh snapshot. |
| `mihatch down` | Restore the original proxy (if still ours) and stop Mihomo. |
| `mihatch logs` | Print Mihomo logs. `-f` to follow, `--tail N` to control the window. |

Common flags: `--root <dir>` (project root, default cwd / `MIHATCH_ROOT`), `--json`, `-v/--verbose`.

### `init` options

```bash
mihatch init                              # default: import Clash Verge Rev prod runtime
mihatch init --config /path/to/cfg.yaml   # import a local Mihomo YAML instead
mihatch init --from /path/to/mihomo       # install an offline Mihomo binary (no download)
```

If you are offline, `init`'s download step fails with a clear hint — use `--from` pointing at an existing `mihomo` binary (e.g. Clash Verge Rev's sidecar).

---

## How it isolates from Clash Verge Rev

MiHatch's purified runtime config **forces** these settings regardless of the source:

- `mixed-port: 17890` (MiHatch's reserved port — different from Clash Verge Rev's defaults)
- `port`, `socks-port`, `redir-port`, `tproxy-port` **removed**
- `allow-lan: false`, `bind-address: 127.0.0.1`
- `tun`, `listeners`, `tunnels` **removed**
- `external-controller`, `external-controller-unix`, `external-controller-tls`, `secret`, `external-ui*` **removed**
- DNS resolver config is kept, but `dns.listen` is removed (Mihomo never opens a DNS server port)

Provider/rule-provider paths are rewritten to live under `.mihatch/`; `type: file` providers are copied in, `type: http` providers cache into `.mihatch/`. MiHatch never references Clash Verge Rev's data directory at runtime.

## Ownership & safety

Taking over the macOS system proxy is the riskiest thing MiHatch does. It follows a strict protocol:

1. **Snapshot** every proxy setting on the target network service(s) *before* changing anything (HTTP, HTTPS, SOCKS, PAC, WPAD, bypass domains — not just on/off flags).
2. Apply MiHatch's settings, then **re-read** to form an "applied" fingerprint.
3. On `pause`/`down`/failure rollback: **compare-before-restore**. MiHatch only restores the original snapshot for a service whose *current* settings still match what it applied. If another app (e.g. Clash Verge Rev dev) changed them in the meantime, MiHatch **abandons** that service and leaves it untouched.

MiHatch **refuses** to take over a service that currently has an *authenticated* proxy (it cannot restore Keychain credentials) unless you pass `--force`.

The target network service is auto-detected from the default route; override with `mihatch up --service "Wi-Fi"`.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | success |
| 1 | general error |
| 2 | config / argument error |
| 3 | not initialized |
| 4 | Mihomo is not healthy |
| 6 | system proxy ownership drifted; not restored for safety |
| 7 | download / digest verification failed |
| 8 | another MiHatch operation holds the lock |

---

## File layout

```
<project>/
└── .mihatch/
    ├── mihomo          # the engine binary (downloaded or --from)
    ├── config.yaml     # purified runtime config (-f)
    ├── state.json      # pid, ownership transaction, engine facts
    └── mihomo.log      # Mihomo stdout+stderr
```

`.mihatch/` is in `.gitignore`. Mihomo uses `.mihatch/` as its `-d` home dir, so any caches/providers it generates stay there too. After `mihatch down`, removing the project directory (or just `.mihatch/`) is a full cleanup.

---

## Limitations (intentional, v1)

- **No auto-restart.** If Mihomo crashes, it stays down; `mihatch status` reports `degraded` and you re-run `mihatch up`.
- **No log rotation.** `mihomo.log` grows unbounded for now; trim it manually if needed.
- **No TUN / DNS / route changes, ever.** Only the loopback mixed proxy.
- **No integrity signature.** Mihomo publishes no checksum asset; MiHatch pins by release tag + asset name over HTTPS and records a local SHA-256 for tamper-evidence. This does **not** prove authenticity against a compromised release account.
- **darwin/arm64 only.**

See [`docs/`](docs/) for architecture, troubleshooting, and security notes.

## Relationship to upstream projects

MiHatch is an independent tool. It is **not affiliated with, endorsed by, or part of** MetaCubeX/mihomo or Clash Verge Rev. It downloads and runs the official Mihomo binary; redistribution and use of Mihomo are governed by Mihomo's own license.

## License

MIT. See [LICENSE](LICENSE).
