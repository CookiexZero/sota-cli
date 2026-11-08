# Implementation and verification

Date: 2026-11-08.

## Delivered path

`SOTA_SUB_KEY` → subscription profile → location list → location configuration → concrete transport selection → domain-scoped local options → `kittend` Unix RPC → proxy or TUN.

All new CLI output, logs, comments, and documentation are in English. No commits were created.

## Verified native API contract

The active API hosts were recovered by running the official desktop client in a temporary Linux container and inspecting its public actualization metadata. No access key was entered into that GUI. The CLI does not depend on the desktop application.

The following authenticated GET requests succeeded with `X-Access-Key`, a persistent `X-HWID`, `X-App-Version: 1.8.11`, and a device name:

| Path under `/api/v1/public` | Result |
|---|---|
| `/subscription/profile` | Traffic limit, usage, expiry, name, and profile URL |
| `/connection/list` | Location IDs, names, recommended flags, and gateways |
| `/connection/connect?gate_id=ID` | Platform, configuration object, and configuration expiry |

The current endpoints accepted these calls without `X-Signature` or `X-API-KEY`. This is an observation about the tested service, not a guarantee about future enforcement. The client uses HTTPS certificate and hostname validation. The actualization payload's `catnip` values were not certificate SPKI hashes and are not used as TLS pins.

The account website also accepted the key through its separate cookie-based API. The delivered CLI uses the native connection API instead.

The returned configuration contained a `urltest` group, VLESS and Hysteria2 outbounds, legacy direct/block/DNS outbounds, and remote rule sets. The adapter selects a single concrete transport, preserves required detours, and discards provider routing and background probes. This avoids unrelated latency-test requests during domain-scoped tests.

## Core and runtime

Both official packages are version `1.8.11+1892`. Package and executable hashes, source URLs, and ELF machine IDs are in `core.lock.json`. The ARM64 package was downloaded from the official CDN link present on the Sota Linux installation page. The amd64 package matches the file originally provided by the user.

The runtime contains the CLI, `kittend`, CA certificates, and Linux networking tools. It does not contain Flutter, GTK, systemd, the access key, or an account-specific VPN configuration.

Proxy tests succeeded with all Linux capabilities dropped. TUN tests succeeded with only `NET_ADMIN` added and `/dev/net/tun` passed through. `privileged`, host networking, host PID access, and macOS route changes were not used.

## Test results

| Architecture | Local proxy integration | Local TUN integration | Real VLESS HTTPS proxy | Real VLESS HTTPS TUN |
|---|---|---|---|---|
| Linux/amd64 under OrbStack emulation | Passed | Passed | Passed | Passed |
| Linux/arm64 on OrbStack | Passed | Passed | Passed | Passed |

Real-network requests targeted `https://example.com/` and verified its certificate, receiving HTTP 200. Native API requests and the DNS/transport traffic needed to reach that domain were performed directly or through the configured tunnel as appropriate. No broad host proxy was enabled.

Focused Go tests cover RPC handshakes and interleaved events, cancellation, credential redaction, domain validation, routing-field rejection, account response filtering, credential file permissions, stable device IDs, and transport/detour selection. `go test ./...` and `go vet ./...` passed. Both Compose variants pass `docker compose config --quiet`.

The local integration test also exercises config validation, status, doctor, disconnect, reconnect, SIGTERM shutdown, and Unix-socket cleanup. Test fixtures contain only synthetic credentials.

The delivered Compose service was started with the real `.env` secret, reached connected state, and carried a certificate-verified HTTPS request to `example.com`. It was then stopped and removed. The state volume retains the same device ID for the next start. The subscription key was checked against all source/documentation files and the exported filesystems of both final image architectures; it was absent from them.

## Deliberate boundaries

This is a usable first implementation for a specified domain and both CPU architectures. It is not yet a general fail-closed VPN gateway. The README records the DNS behavior, native kill-switch absence, fixed bootstrap host list, state-only health checks, and remaining recovery/transport limits. Those limits are explicit rather than hidden behind a connected-state indicator.
