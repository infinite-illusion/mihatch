# Architecture

MiHatch is a thin command-layer over a small set of focused packages. There is
**no long-running daemon, no supervisor, and no launchd**. Each `mihatch`
invocation does its work and exits.

```
cmd/mihatch/main.go        entrypoint; os.Exit with stable code
internal/cli               cobra commands -> app methods
internal/app               command orchestration (init/sync/up/status/pause/resume/down/logs)
internal/mihomo            process lifecycle: -t/-v, detached start, pid identity, SIGTERM/SIGKILL
internal/mihomoconfig      YAML surgery: read source, apply isolation overrides, migrate providers
internal/download          GitHub release query, asset select, gzip+sha256
internal/proxy             networksetup parser/commands, service discovery, snapshot, ownership
internal/health            mixed-port TCP dial + proxied generate_204 check
internal/state             state.json schema + pure state determination
internal/{paths,runner,lock,atomicfile,redact,exit}   supporting infra
```

## Process model

`mihatch up` starts Mihomo directly via Go's `os/exec` — **never through a
shell** — with stdio redirected to `.mihatch/mihomo.log` and `Setpgid` so it runs
in its own process group detached from the terminal. When the `mihatch` process
exits, Mihomo is reparented to launchd (pid 1) and keeps running.

MiHatch records the pid, the `ps`-reported start time, and the binary path in
`state.json`. Later commands (`down`, `status`) confirm process **identity** by
re-checking all three before signalling — a recycled pid with a different start
time or command is treated as "not ours" and never killed.

There is **no crash auto-restart** by design. If Mihomo dies, `status` reports
`degraded` and the user re-runs `up`.

## State machine

`status` never trusts the last-written `state.json`; it recomputes from live
signals (process alive, mixed port listening, proxied connectivity, current
proxy settings vs. applied fingerprint).

| State | Meaning |
|---|---|
| `uninitialized` | not `mihatch init`-ed, or engine missing |
| `stopped` | no running process, port closed |
| `standby` | Mihomo healthy, but MiHatch does not own the system proxy |
| `active` | Mihomo healthy **and** system proxy still matches what MiHatch applied |
| `degraded` | process/port/health/ownership is inconsistent |

The mapping is pure logic in `internal/state.Determine` and is fully table-tested.

## System-proxy ownership

The most safety-critical path. See `internal/proxy`:

1. **Snapshot** the full per-service config (HTTP/HTTPS/SOCKS server+port+auth,
   PAC URL+enabled, WPAD, bypass domains).
2. **Acquire**: disable WPAD/PAC, set the three proxies to `127.0.0.1:<port>`,
   set bypass domains, then re-read to form an **applied** snapshot + fingerprint.
3. **Restore** (`pause`/`down`): **compare-before-restore** — only roll back a
   service whose current settings still equal the applied snapshot. A service
   changed by a third party is **abandoned**, never overwritten.

All `networksetup` calls use argv directly (no `sh -c`); every verb spelling and
output field is verified against the macOS man page and fixture-tested. The test
suite never calls real `networksetup`.

## Concurrency

Mutating commands (`init`, `sync`, `up`, `pause`, `resume`, `down`) take an
exclusive `flock` on `.mihatch/mihatch.lock` for their duration, so two
concurrent `mihatch` processes cannot race on the proxy or the engine.
