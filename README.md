# Sota CLI

A Go CLI that obtains a Sota VPN configuration with an access key and controls the official `kittend` core. Docker images support **linux/amd64** and **linux/arm64**.

The default is a local HTTP/SOCKS proxy. Every connection requires an explicit domain allowlist. There is no unrestricted routing option.

## Start with Docker

Requirements: Python 3, Docker with Buildx, and a Linux Docker engine. OrbStack works on macOS. Go is needed only when building outside Docker.

Create a local `.env` file:

```dotenv
SOTA_SUB_KEY=your-access-key
SOTA_TEST_DOMAIN=example.com
```

Then prepare the official cores, build both architectures, and start the proxy:

```sh
python3 scripts/fetch-core.py
docker buildx build --platform linux/amd64,linux/arm64 --load -t sota-cli:dev .
docker compose up -d --no-build
docker compose exec vpn sota status
docker compose exec vpn sota servers --env-file /run/secrets/sota-env
```

The fetch script checks package and executable SHA-256 hashes against [core.lock.json](core.lock.json). If the CDN's `latest` package changes, it fails instead of silently changing the core. Both pinned packages are Sota `1.8.11+1892`.

The `.env` file is mounted as a Compose secret. It is excluded from Git and the Docker build context. The state volume preserves the device ID across restarts. `run` uses the supplied key in memory; `login` explicitly saves a verified key with mode `0600`.

## Proxy access

Read the current port from `sota status`; the core chooses it, usually `2080`. The proxy listens on **127.0.0.1 inside the VPN container's network namespace**. A client container can share that namespace with `network_mode: "service:vpn"` in Compose or `--network container:<vpn-container-id>` with Docker.

Use SOCKS with remote name resolution, such as `socks5h://127.0.0.1:2080`. Only the configured exact domains use the selected VPN outbound. Other data destinations use direct routing. Publishing a Docker port does not expose this loopback listener.

## TUN mode

```sh
docker compose -f compose.yaml -f compose.tun.yaml up -d --no-build
```

This grants only `NET_ADMIN` in addition to dropping the default capabilities and provides `/dev/net/tun`. Routes remain inside the VPN container. An application must share its network namespace to use TUN. Containers have separate DNS configuration even when they share a network namespace; configure and verify application DNS separately.

On macOS, this operates inside OrbStack's Linux VM. It does not install a VPN or change routes on macOS.

## CLI

Run `sota help` or `sota <command> --help` for flags.

| Command | Purpose |
|---|---|
| `login --env-file FILE` | Verify `SOTA_SUB_KEY` and save the key |
| `account --env-file FILE` | Show subscription expiry and traffic usage |
| `servers --env-file FILE --json` | List available location IDs |
| `run --env-file FILE --domain example.com` | Start the core and connect in proxy mode |
| `run --mode tun --domain example.com` | Connect in TUN mode using a previously saved key |
| `connect --domain example.com --server ID` | Connect an existing core to a selected location |
| `disconnect` | Disconnect while keeping the core running |
| `status --json` | Read state and the actual proxy port |
| `health` | Exit with success only when core state is connected |
| `check-config --config FILE --domain example.com` | Validate an imported configuration |
| `logs` | Follow sanitized core events |
| `doctor` | Show core version, protocol, TUN availability, and native kill-switch support |

Connection commands also accept `--key-file FILE`, `--state-dir DIR`, `--server ID`, `--transport vless`, and repeated `--domain NAME`. `SOTA_SUB_KEY` can be read from the process environment. Credentials are never accepted as command-line values.

To import a configuration without using the Sota API:

```sh
sota run --config /path/to/outbounds.json --domain example.com
```

The imported object must contain only a non-empty `outbounds` array. Routing, DNS, and inbound settings are owned by the CLI. Configurations returned by the API are reduced to one concrete transport and its required detours. Background `urltest` probes and unrelated provider routing rules are removed.

## Verification

Offline integration tests use the real cores and a local mock tunnel. Proxy tests have no network; TUN tests use a separate container network namespace. They verify selected-domain traffic, direct bypass, RPC commands, and graceful shutdown for both architectures.

```sh
python3 scripts/test.py
```

To test a real subscription, with HTTPS requests limited to one explicit destination domain:

```sh
python3 scripts/live-test.py --domain example.com
```

The live test reads the local `.env`, reuses one device ID, tests both architectures and modes with VLESS, verifies the destination's TLS certificate, and removes its containers. API calls, VPN transport connections, and DNS needed for that domain are expected. Host networking and global proxy settings are not changed.

Verified on 2026-11-08 through OrbStack: real Sota access-key authentication, server/configuration retrieval, and HTTPS 200 from `example.com` in all four architecture/mode combinations.

## Current limits

- The Linux core reports no native kill switch. TUN mode does not provide fail-closed protection if the core crashes. Domain-scoped routing intentionally leaves other traffic direct.
- The core's configured DNS resolver may use the VPN even for a name outside the data-routing allowlist. The domain list is not a DNS privacy boundary.
- The health check reads core state; it does not prove that a remote server can currently carry traffic. The live test provides that stronger check.
- Fatal core errors cause `run` to exit, allowing Compose's restart policy to fetch a fresh configuration. A normal `disconnect` leaves the process running. Full network-change recovery and automatic refresh before configuration expiry are not implemented.
- API hosts are seeded from the official client's current actualization payload. Automatic signed actualization refresh is not implemented; endpoint changes may require an update. Standard HTTPS certificate and hostname verification remain enabled.
- The server configuration adapter supports concrete outbounds and linear detours. Grouped detours and standalone WireGuard endpoint configurations are not supported. Real-network acceptance currently covers VLESS; Hysteria2 is selectable but has not been acceptance-tested.
- The official Sota core is downloaded and verified during build preparation, then included in the Docker image. Core binaries are excluded from Git.

