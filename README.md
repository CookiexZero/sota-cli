# Sota CLI

A Go CLI that obtains a Sota VPN configuration with an access key and controls the official `kittend` core. Docker images support **linux/amd64** and **linux/arm64**.

The default is a local HTTP/SOCKS proxy. Connections require either an explicit domain allowlist or `--all` to route all internet destinations through the VPN. LAN bypass remains enabled in both scopes.

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

The build cross-compiles the CLI and prepares target packages using native build stages, so building both architectures does not require QEMU. Running a foreign-architecture image still requires emulation or a matching host. Loading both architectures with `--load` requires Docker's containerd image store; with a classic image store, build only the host architecture or publish the multi-platform image with `--push`.

To build only for the current machine:

```sh
docker build -t sota-cli:dev .
```

The fetch script checks package and executable SHA-256 hashes against [core.lock.json](core.lock.json). If the CDN's `latest` package changes, it fails instead of silently changing the core. Both pinned packages are Sota `1.8.11+1892`.

The `.env` file is mounted as a Compose secret. It is excluded from Git and the Docker build context. The state volume preserves the device ID across restarts. `run` uses the supplied key in memory; `login` explicitly saves a verified key with mode `0600`.

On a rootful Linux Docker engine the secret retains the host file's ownership and permissions. Because the container drops `DAC_OVERRIDE`, a user-owned `.env` with mode `0600` cannot be read by the container's root user. Keep your user as owner, set the file's group to GID 0, and use mode `0640` (`sudo chgrp 0 .env && chmod 640 .env`). Do not make the key world-readable.

## Proxy access

Read the current port from `sota status`; the core chooses it, usually `2080`. The proxy listens on **127.0.0.1 inside the VPN container's network namespace**. A client container can share that namespace with `network_mode: "service:vpn"` in Compose or `--network container:<vpn-container-id>` with Docker.

Use SOCKS with remote name resolution, such as `socks5h://127.0.0.1:2080`. Only the configured exact domains use the selected VPN outbound. Other data destinations use direct routing. Publishing a Docker port does not expose this loopback listener.

## TUN mode

```sh
docker compose -f compose.yaml -f compose.tun.yaml up -d --no-build
```

This grants only `NET_ADMIN` in addition to dropping the default capabilities and provides `/dev/net/tun`. Routes remain inside the VPN container. An application must share its network namespace to use TUN. Containers have separate DNS configuration even when they share a network namespace; configure and verify application DNS separately.

On macOS, this operates inside OrbStack's Linux VM. It does not install a VPN or change routes on macOS.

### Full internet routing through TUN

Build a separate image and select the full-tunnel override:

```sh
docker build -t sota-cli:full-tun .
docker compose -f compose.yaml -f compose.full-tun.yaml up -d --no-build --wait
```

This runs `sota run --mode tun --all`. It sends internet destinations, including connections with no domain name, through the selected outbound. `--all` and `--domain` are mutually exclusive; omitting both is still an error. The existing domain-scoped Compose files keep their behavior.

The TUN and routes remain inside the container. This override alone does not make the Pi a LAN gateway or route a HomePod through it. LAN forwarding, router policy, DNS handling, and IPv6 must be configured and verified separately. The profile currently disables IPv6 in the core; it does not block other LAN devices from reaching the internet over IPv6. Native fail-closed protection remains unavailable.

To return to the original domain-scoped TUN image and settings:

```sh
docker compose -f compose.yaml -f compose.tun.yaml up -d --no-build --wait
```

## CLI

Run `sota help` or `sota <command> --help` for flags.

| Command | Purpose |
|---|---|
| `login --env-file FILE` | Verify `SOTA_SUB_KEY` and save the key |
| `account --env-file FILE` | Show subscription expiry and traffic usage |
| `servers --env-file FILE --json` | List available location IDs |
| `run --env-file FILE --domain example.com` | Start the core and connect in proxy mode |
| `run --mode tun --domain example.com` | Connect in TUN mode using a previously saved key |
| `run --mode tun --all` | Route all internet destinations through TUN, with LAN bypass |
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

The offline suite exercises both routing scopes in both modes, including a second public IP with no DNS mapping and local direct access. Use `--scope domain` or `--scope all` to select a scope, and `--image NAME` to test a separately tagged image.

To test a real subscription, with HTTPS requests limited to one explicit destination domain:

```sh
python3 scripts/live-test.py --domain example.com
```

The live test reads the local `.env`, reuses one device ID, tests both architectures and modes with VLESS, verifies the destination's TLS certificate, and removes its containers. API calls, VPN transport connections, and DNS needed for that domain are expected. Host networking and global proxy settings are not changed.

Verified on 2026-11-08 through OrbStack: real Sota access-key authentication, server/configuration retrieval, and HTTPS 200 from `example.com` in all four architecture/mode combinations.

## Current limits

- The Linux core reports no native kill switch. TUN mode does not provide fail-closed protection if the core crashes. Domain-scoped routing intentionally leaves other traffic direct; `--all` routes internet destinations through VPN while retaining LAN bypass.
- The core's configured DNS resolver may use the VPN even for a name outside the data-routing allowlist. The domain list is not a DNS privacy boundary.
- The health check reads core state; it does not prove that a remote server can currently carry traffic. The live test provides that stronger check.
- Fatal core errors cause `run` to exit, allowing Compose's restart policy to fetch a fresh configuration. A normal `disconnect` leaves the process running. Full network-change recovery and automatic refresh before configuration expiry are not implemented.
- API hosts are seeded from the official client's current actualization payload. Automatic signed actualization refresh is not implemented; endpoint changes may require an update. Standard HTTPS certificate and hostname verification remain enabled.
- The server configuration adapter supports concrete outbounds and linear detours. Grouped detours and standalone WireGuard endpoint configurations are not supported. Real-network acceptance currently covers VLESS; Hysteria2 is selectable but has not been acceptance-tested.
- The official Sota core is downloaded and verified during build preparation, then included in the Docker image. Core binaries are excluded from Git.

