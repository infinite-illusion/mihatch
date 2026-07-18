# Troubleshooting

## `init` fails: "Clash Verge Rev prod runtime not found"

MiHatch reads prod's final runtime from
`~/Library/Application Support/io.github.clash-verge-rev.clash-verge-rev/clash-verge.yaml`.

- Open **Clash Verge Rev (prod, not dev)**, select the profile you want, and let
  it refresh the runtime once.
- Re-run `mihatch init`.
- If you don't use Clash Verge Rev, import a file directly:
  `mihatch init --config /path/to/your/mihomo.yaml`.
- Only the **prod** App ID is read; the `.dev` App ID is never selected.

## `init` fails: cannot download Mihomo (offline / unstable network)

The download is fault-tolerant: it **resumes** from disk via HTTP Range and
**retries up to 10 times** when a read stalls (no bytes for 20s) or the
connection drops. The progress bar stays continuous across retries and prints a
`retry N/10 after … — resuming from XmB` notice. So a slow link is fine — let it
run.

If it still fails after all retries, install an offline binary instead:

```
mihatch init --from /path/to/an/existing/mihomo
```

A good source is Clash Verge Rev's sidecar binary inside its app bundle. The
binary is copied into `.mihatch/mihomo`; its local SHA-256 is recorded but does
**not** prove upstream authenticity (Mihomo publishes no checksum).

The downloader honors the standard `HTTP_PROXY` / `HTTPS_PROXY` environment
variables, so if your circumvention tool exposes a local proxy you can point at
it (e.g. `HTTPS_PROXY=http://127.0.0.1:7890 mihatch init`).

## `up` reports `degraded` / health check failed

Mihomo started but did not serve the mixed proxy. Inspect the log:

```bash
mihatch logs --tail 100
```

Common causes:
- The config references `type: file` providers that no longer exist.
- A rule references GeoIP/GeoSite data that Mihomo could not download (the
  machine has no direct route to the geo host). Place the geo files under
  `.mihatch/` or simplify the rules.
- The mixed port is already taken by another process (rare; check
  `lsof -i :17890`).

Recover with `mihatch down` then `mihatch up`. (`up` also self-heals: if a
previous Mihomo is alive but unhealthy, it stops it and starts fresh.)

## `pause`/`down` exits with code 6 (proxy drifted)

This is MiHatch refusing to overwrite proxy settings another application changed
after MiHatch took over. **This is correct behavior.** MiHatch abandoned that
network service and left the other app's settings in place.

To take back control: run `mihatch resume` (it takes a fresh snapshot and
re-acquires), or set the proxy as you want it manually first.

## `up` refuses with "authenticated proxy"

The target network service currently has a proxy that requires a username /
password. MiHatch cannot restore Keychain credentials, so it refuses by default.
Only override if you accept that the original credentials will not be restored:

```bash
mihatch up --force
```

## `up` can't auto-detect the network service

Auto-detection maps the default-route interface to a network service. If that
fails (e.g. an unusual interface), specify the service explicitly:

```bash
mihatch up --service "Wi-Fi"
# multiple:
mihatch up --service "Wi-Fi,USB 10/100/1000 LAN"
```

Find service names with `networksetup -listallnetworkservices`.

## Two `mihatch` commands at once → code 8

Another MiHatch process holds the lock. Wait for it to finish, or remove a stale
`.mihatch/mihatch.lock` only if you are sure no MiHatch process is running.

## Full reset

```bash
mihatch down
rm -rf .mihatch
```
