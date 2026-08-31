<h1 align="center">Knoten</h1>

<p align="center">
  <strong>A self-hostable mesh VPN</strong>
</p>

<p align="center">
  <a href="#license"><img alt="License: MIT" src="https://img.shields.io/badge/license-MIT-blue.svg"></a>
  <img alt="Go 1.26+" src="https://img.shields.io/badge/go-1.26%2B-00ADD8.svg?logo=go&logoColor=white">
  <img alt="Platform: Linux" src="https://img.shields.io/badge/platform-linux-lightgrey.svg?logo=linux&logoColor=white">
  <img alt="Version: 0.2.0-alpha" src="https://img.shields.io/badge/version-0.2.0--alpha-orange.svg">
  <img alt="Status: alpha" src="https://img.shields.io/badge/status-alpha-orange.svg">
  <img alt="Built on WireGuard" src="https://img.shields.io/badge/built%20on-WireGuard-88171A.svg">
</p>

---

Knoten connects your machines (servers, cloud instances, VMs, laptops) over **direct, end-to-end encrypted WireGuard tunnels**, instead of funnelling every packet through one central VPN server.

A coordination server may exist, but it only handles **discovery, identity, and address assignment**. It never carries your data.

{A demo gif will be inserted here}

> 🟢 Status: **Alpha**: latest version `v0.2.0-alpha`
>
> **Knoten is still in its alpha phase.** The first working version runs: a coordination server, a node daemon, automatic key generation, IP address management, and live tunnel updates. **This is a student-driven project under active development**, command-line flags, the HTTP API, and on-disk formats may still change between versions, without a compatibility guarantee. Not yet recommended for production.

## Contents

- [Why Knoten](#why-knoten)
- [How it works](#how-it-works)
- [Deployment modes](#deployment-modes)
- [Requirements](#requirements)
- [Quick start](#quick-start)
  - [Coordinated mode](#coordinated-mode)
  - [Standalone mode](#standalone-mode)
- [Command-line reference](#command-line-reference)
- [Files and paths](#files-and-paths)
- [Security model](#security-model)
- [Roadmap](#roadmap)
- [Contributing](#contributing)
- [License](#license)

## Why Knoten

**Direct peer-to-peer by default.** Traffic goes straight from one machine to another. The coordination layer is consulted only when something needs to change: who exists, who is allowed in, and which address each machine holds.

**Resilient by design.** Tunnels never depend on the coordination layer to stay alive. Existing connections are sustained by WireGuard itself, so losing a coordinator restricts network *administration*, not connectivity. This assumes relatively stable peers; radical, simultaneous changes across the fleet cannot be reconciled until coordination is restored.

**Access is a lease, never a possession.** Every grant is designed to carry an expiry and persist only through renewal. Revocation is simply expiry-now, so removing access is never a special operation, and abandoned access decays to nothing on its own. *(Design principle; the lease mechanism itself is on the [roadmap](#roadmap).)*

**Small and genuinely self-hostable.** Two static Go binaries and one SQLite file. The SQLite driver is pure Go, so builds need no cgo and no C toolchain.

**No lock-in.** If you stop the daemon, the tunnel it produced keeps working.

## How it works

{A diagram will be inserted here}

On each machine, `meshd`:

1. **Generates an identity** on first run: an X25519 keypair created locally with Go's standard library. The private key is written to a `0600` state file and never leaves the machine.
2. **Registers** with the coordination server, which assigns it a stable VPN address out of `10.10.0.0/16` and records the public endpoint it was seen from.
3. **Polls `/v1/sync`** on an interval the server dictates to the whole fleet, receiving the list of peers that have checked in recently.
4. **Renders `/etc/wireguard/knoten-wg.conf`** atomically, then applies it: `wg-quick up` if the tunnel is down, or `wg syncconf` if it is already running. Syncing does **not** drop the interface, so connections through the tunnel survive a peer-list change.
5. **Steps back.** All actual traffic flows peer to peer, encrypted end to end by WireGuard.

If the coordination server becomes unreachable, `meshd` retries with exponential backoff (1s to 60s, with jitter) while the existing tunnel keeps running untouched.

## Deployment modes

| Mode | When to use it | What you run |
| --- | --- | --- |
| **Standalone** | Every node has a static, reachable IP address, and you are happy to list peers yourself. Pure peer-to-peer, no control plane at all. | `meshd` on each node, with a `static_peers` list in its config. |
| **Coordinated** | Peers sit behind NAT or have changing IPs, or you want identity and permissions managed centrally. | `coordserver` on one reachable host, `meshd` on every node. A static node can do both jobs. |

Machines are given addresses from a fixed `10.10.0.0/16` range in coordinated mode. *(A future version will let you choose your own address pool.)*

## Requirements

- **Linux** on every node. `meshd` writes to `/etc/wireguard` and manages a kernel interface, so it must run as root.
- **[`wireguard-tools`](https://www.wireguard.com/install/)** (`wg` and `wg-quick`) installed on every node.
- **One reachable host** for `coordserver` if you are using coordinated mode.

```bash
# Debian / Ubuntu
sudo apt install wireguard-tools

# Fedora / RHEL
sudo dnf install wireguard-tools

# Arch
sudo pacman -S wireguard-tools
```

## Quick start

Prebuilt Linux binaries are attached to each [release](https://github.com/Yacin1102/Knoten/releases). 

### Coordinated mode

This walks through **coordinated mode**: one coordination server plus a fleet of nodes. Skip to [Standalone mode](#standalone-mode) if every node already has a static IP.

#### 1. Start the coordination server

Generate a join token first. Every machine must present it to enrol.

```bash
sudo mkdir -p /etc/knoten
openssl rand -base64 32 | sudo tee /etc/knoten/token > /dev/null
sudo chmod 600 /etc/knoten/token
```

Then start the server:

```bash
sudo coordserver \
  -listen :8080 \
  -db /var/lib/knoten/coord.db \
  -token-file /etc/knoten/token
```

It should show something like this:

```
coordserver: listening on :8080 (plain HTTP)
coordserver: VPN range: 10.10.0.0/16
coordserver: database:  /var/lib/knoten/coord.db
coordserver: machines poll every 30s; considered gone after 1m30s of silence
coordserver: join token is set (44 characters)
```

The database file and its directory are created if missing. Open TCP **8080** on the coordination server, and UDP **51820** (the WireGuard port) on every node.

> ⚠️ `coordserver` speaks **plain HTTP** and has no TLS of its own. Put it behind a reverse proxy that terminates TLS before exposing it to the internet. (*Native HTTPS support is planned for a future version.*) See [Security model](#security-model).

#### 3. Join your first machine

Run interactive setup on the node:

```bash
sudo meshd -setup
```

Setup asks a short list of questions and writes `/etc/knoten/meshd.json`:

```
ListenPort (default 51820):
Use a coordination server? (y/N): y
Coordination server URL (e.g. https://coord.example.com:8443): http://<server IP>:8080
Join token (leave empty if the server does not require one): <paste the token from step 2>
Name for this machine (leave empty for "Hostname-01"):
```

Setup stores the token you paste inline in `meshd.json`, as `join_token`. To keep the token in its own file instead, edit `/etc/knoten/meshd.json` after setup and swap the field:

```json
{
"use_coordination_server": true,
"server_url": "http://198.51.100.10:8080",
"token_file": "/etc/knoten/token"
}
```

`token_file` wins when both are present: if it is set, `join_token` is ignored entirely. Setup never writes `token_file` for you, so this is always a manual edit.

`-setup` then performs a single register-and-sync cycle and exits, so you can confirm everything worked before running it for real:

```
meshd: Config written to /etc/knoten/meshd.json
meshd: no key found at /var/lib/knoten/meshd-state.json - generating a new identity for this machine
meshd: generated key pair; this machine's PUBLIC key is: 9Kx...=
meshd: registering with http://198.51.100.10:8080
meshd: registered: our VPN address is 10.10.0.2 in 10.10.0.0/16
meshd: wrote /etc/wireguard/knoten-wg.conf (0 peer(s))
meshd: brought knoten-wg up
```

Now run it continuously:

```bash
sudo meshd
```

> You can run it in the background, or install it as a systemd service.

#### 4. Join the rest of the fleet

Repeat step 3 on every other machine. Each one gets its own key and its own address, and within one sync interval (30s by default) every node sees every other node:

```
meshd: wrote /etc/wireguard/knoten-wg.conf (2 peer(s))
meshd: synced knoten-wg
```

#### 5. Verify

```bash
# What does the control plane think?
curl -s http://<server IP>:8080/v1/health
# {"status":"ok","machines_total":3,"machines_alive":3,"uptime_seconds":412,
#  "vpn_cidr":"10.10.0.0/16","sync_interval_seconds":30}

# What does WireGuard think?
sudo wg show knoten-wg

# Can you reach a peer over the mesh?
ping 10.10.0.3
```

Send `SIGHUP` to any daemon to force an immediate sync instead of waiting for the next poll:

```bash
sudo pkill -HUP meshd
```

### Standalone mode

If every node has a static, reachable address, skip the server entirely. Run `meshd -setup` and answer **no** to *"Use a coordination server?"*, then enter this node's address and each peer by hand:

```
This node's Address (e.g. 10.0.0.1/24): 10.0.0.1/24

Peer PublicKey (leave empty to finish): xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=
  AllowedIPs (e.g. 10.0.0.2/32): 10.0.0.2/32
  Endpoint (e.g. 192.168.56.102:51820): 203.0.113.7:51820
  Name for this peer (optional): db-01
Peer PublicKey (leave empty to finish):
```

Use `meshd -show-key` on each node to read the public key you need to paste into the others.

In standalone mode `meshd` renders the config, applies it, and **exits**: there is nothing to poll. Re-run it after editing the peer list.

## Command-line reference

For a quick look without leaving the terminal:

```bash
coordserver -h
meshd -h
```

**Signals:** `SIGHUP` makes `meshd` sync immediately. `SIGINT` and `SIGTERM` stop it cleanly, leaving the tunnel up.

## Files and paths

| Path                               | Written by     | Mode   | Contents                                                                                                          |
| ---------------------------------- | -------------- | ------ | ----------------------------------------------------------------------------------------------------------------- |
| `/etc/knoten/meshd.json`           | `meshd -setup` | `0600` | Node configuration.                                                                                               |
| `/var/lib/knoten/meshd-state.json` | `meshd`        | `0600` | Private key, public key, assigned VPN address. **Back this up.**                                                  |
| `/etc/wireguard/knoten-wg.conf`    | `meshd`        | `0600` | Generated `wg-quick` configuration.                                                                               |
| `/etc/knoten/token`                | you            | `0600` | Shared join token. Read by `coordserver -token-file`, and by `meshd` only when `token_file` is set in its config. |
| `/var/lib/knoten/coord.db`         | `coordserver`  | -      | SQLite database of enrolled machines.                                                                             |

> **Deleting `meshd-state.json` gives the machine a brand-new identity and a brand-new VPN address on the next register.** The old registration lingers on the server until it times out.

The interface is always named `knoten-wg`.

## Security model

**What Knoten protects today**

- **Traffic is end-to-end encrypted by WireGuard**, peer to peer. The coordination server sees metadata: public keys, names, addresses, endpoints, and last-seen times.
- **Private keys never leave the machine.** They are generated locally via Go's `crypto/ecdh` X25519 and written `0600`.
- **The join token is compared in constant time**, so it cannot be recovered by timing the response.
- **The coordination server is defensive at the edges**: bounded request bodies, strict JSON decoding, request timeouts, panic recovery, and a single-writer SQLite store with `WAL` and `synchronous = FULL`.

**What it does not protect yet**

- **No TLS.** `coordserver` serves plain HTTP. Terminate TLS in front of it (nginx, Caddy, Traefik) before exposing it publicly, and set `-trust-proxy` only when your proxy overwrites `X-Forwarded-For`.
- **One shared token for the whole fleet.** Any machine holding it can enrol and read the peer list. There is no per-machine credential and no revocation yet. See the [roadmap](#roadmap).
- **No NAT traversal.** Endpoints come from whatever address the server observed. Peers behind symmetric NAT with no port forwarding may not reach each other directly, and there is no relay fallback yet.
- **No admin surface yet.** Removing a machine means deleting its row from the SQLite database by hand.

## Roadmap

As a student, I am constantly learning while building Knoten. This is the project's planned roadmap.

| Stage  | What we’ll be building                     | Progress |
| ------ | ------------------------------------------ | -------- |
| **0**  | Initial setup                              | ✅        |
| **1**  | Manual WireGuard configuration             | ✅        |
| **2**  | WireGuard config generator \| v0.1         | ✅        |
| **3**  | Coordination server \| v0.2.0-alpha        | ✅        |
| **4**  | wgctrl \| v0.2.1                           | ⚒️       |
| **5**  | Security, features and stability \| v0.2.2 | ⚒️       |
| **6**  | NAT support                                | -        |
| **7**  | Redundant control-plane coordination       | -        |
| **8**  | Identity & access management               | -        |
| **9**  | Management console (UI)                    | -        |
| **10** | Hardening                                  | -        |

Some work carried by those stages:

- **Native HTTPS support.**
- **Native WireGuard control** via [`wgctrl`](https://github.com/WireGuard/wgctrl-go), replacing the current shell-out to `wg` and `wg-quick`.
- **Per-machine credentials, leases and revocation**, replacing the single shared join token and realising the "access is a lease" principle.
- **NAT traversal**, and a last-resort relay for peers that genuinely cannot reach each other. Even then, relayed packets stay sealed and end-to-end encrypted, unreadable by the relay.
- **Multiple coordination servers**, so the control plane has no single point of failure.
- **IPv6 support**.

## Contributing

This is a student-driven project, and contributions are genuinely welcome: bug reports, design critique, and documentation just as much as code.

Please open an issue to discuss anything substantial before sending a large pull request. Because the project documents its own development journey, PRs that change design decisions are more useful when they explain the reasoning, not only the diff.

Thank you.

## License

Released under the [MIT License](LICENSE).
