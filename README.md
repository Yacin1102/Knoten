<p align="center">
  <img src="./assets/Knoten_README_banner.png" alt="Knoten Banner" width="100%">
</p>

<p align="center">
  <a href="#license"><img alt="License: MIT" src="https://img.shields.io/badge/license-MIT-blue.svg"></a>
  <img alt="Go 1.26+" src="https://img.shields.io/badge/go-1.26%2B-00ADD8.svg?logo=go&logoColor=white">
  <img alt="Platform: Linux" src="https://img.shields.io/badge/platform-linux-lightgrey.svg?logo=linux&logoColor=white">
  <img alt="Version: 0.3.2-alpha" src="https://img.shields.io/badge/version-0.3.2--alpha-orange.svg">
  <img alt="Status: alpha" src="https://img.shields.io/badge/status-alpha-orange.svg">
  <img alt="Built on WireGuard" src="https://img.shields.io/badge/built%20on-WireGuard-88171A.svg">
</p>

---

Knoten connects your machines (servers, cloud instances, VMs, laptops) over **direct, end-to-end encrypted WireGuard tunnels**, instead of funnelling every packet through one central VPN server.

A coordination server may exist, but it is deliberately dumb: it handles **discovery only**, keeps no database or files, and never carries your data. Every decision (addresses, which peers to accept) is made by each machine itself.

*This Demo is from an older version:*
<p align="center">
  <img src="./assets/Knoten_demo.gif" alt="Knoten_demo.gif">
</p>

> [!IMPORTANT]
>Suggestions, feature requests, and feedback are highly welcome!


> [!NOTE]
> Status: **Alpha** (latest version `v0.3.2-alpha`)
>
> **Knoten is still in its alpha phase.** The current working version runs: a coordination server, a node daemon, automatic key generation and locally derived IPv6 addresses, peer memory, and live tunnel updates. 
> **This is a student-driven project under active development**. Command-line flags, the HTTP API, and on-disk formats may still change between versions, without a compatibility guarantee.

> [!IMPORTANT]
> **Knoten does not currently support nodes behind Network Address Translation (NAT)**.

## Contents

- [Why Knoten](#why-knoten)
- [How it works](#how-it-works)
  - [Addresses](#addresses)
- [Ways to run it](#ways-to-run-it)
- [Requirements](#requirements)
- [Quick start](#quick-start)
  - [With a coordination server](#with-a-coordination-server)
  - [Without a server](#without-a-server)
- [The config file](#the-config-file)
- [Command-line reference](#command-line-reference)
- [Files and paths](#files-and-paths)
- [Security model](#security-model)
- [Roadmap](#roadmap)
- [Contributing](#contributing)
- [License](#license)

## Why Knoten

**Direct peer-to-peer by default.** Traffic goes straight from one machine to another. The coordination layer is consulted for discovery.

**Resilient by design.** Tunnels never depend on the coordination layer to stay alive. Losing a coordinator restricts live peer updates and synchronization, not connectivity. This assumes relatively stable endpoints; radical, simultaneous changes across the fleet cannot be reconciled until coordination is restored.

**Access is a lease, never a possession.** Every grant is designed to carry an expiry and persist only through renewal. Revocation is simply expiry-now, so removing access is never a special operation, and abandoned access decays to nothing on its own. *(Design principle not yet implemented; the lease mechanism itself is on the [roadmap](#roadmap).)*

**Small and genuinely self-hostable.** Two static Go binaries. **You can even run the coordserver and meshd binaries on the same machine.**

## How it works

<p align="center">
  <img src="./assets/Simplified_architecture.png" alt="Knoten Architecture Diagram">
</p>

On each machine, `meshd`:

1. **Has its own identity:** a keypair created locally by `meshd -setup`. The private key is stored in `/etc/knoten/meshd.json` (`0600`) and never leaves the machine.

2. **Computes its own address** from the mesh name, its subnet and its public key. No server is involved, so any machine can compute any peer's address from the peer's key and subnet.

3. **Syncs with `/v2/sync`** on an interval the server dictates. Each sync tells the server about this machine (its first sync is how it joins) and returns the machines that synced recently, with the endpoint the server saw each one at.

4. **Filters the peers**: machines of another mesh are skipped, and peers from subnets not in `accept_subnets` are left out.

5. **Applies the peer list** (the server's, plus any peers you listed yourself) to its own WireGuard tunnel, `knoten-wg`, which runs inside `meshd` itself.

6. **Remembers the peers** in `meshd.json`, so after a restart the tunnel comes back with them, even while the server is unreachable.

7. **Stays in the background.** All actual traffic flows peer to peer, encrypted end to end by WireGuard.

**The tunnel lives inside `meshd`: stopping `meshd` removes it, and starting it again brings it back. Run it as a service (for example systemd with `Restart=always`) so it comes back on its own.**

**If the coordination server becomes unreachable, `meshd` keeps the tunnel and its peers as they are, and retries with exponential backoff (1s to 60s, with jitter).**

### Addresses

Every address is a unique local IPv6 address, built from three hashes:

| Bits | Part   | Comes from                                           |
| ---- | ------ | ---------------------------------------------------- |
| 8    | `fd`   | **fixed**                                            |
| 40   | mesh   | hash of `mesh`: the same for every machine of a mesh |
| 16   | subnet | hash of `subnet`; the subnet `global` is `0000`      |
| 64   | host   | hash of the machine's public key                     |

The first 48 bits are the mesh's network. A machine ignores server peers from another network, so several meshes can share one coordination server.

**Only the tunnel is IPv6: endpoints (the machines' real addresses) can be IPv4 or IPv6.**

## Ways to run it

| Setup                          | When to use it                                                                                                                       | What you run                                                                         |
| ------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------ |
| **With a coordination server** | Machines have changing IPs, or you don't want to exchange keys by hand.                                                              | `coordserver` on one reachable host, `meshd` on every node. A node can do both jobs. |
| **Without a server**           | Every node has a static, reachable IP address, and you are happy to list peers yourself. Pure peer-to-peer, no control plane at all. | `meshd` on each node, with its peers listed in `meshd.json`.                         |
| **Both**                       | Peers come from the server, plus ones you list yourself.                                                                             | The same as with a server, plus `static` peers in `meshd.json`.                      |

There are no modes to pick: setting `server_url` in `meshd.json` turns the server on, and peers you list yourself work with or without it.

## Requirements

- **Linux** on every node, with `/dev/net/tun` available (it is on standard distributions). `meshd` creates the `knoten-wg` interface itself, so it must run as root. *(IPv6 must not be disabled in the kernel, because the tunnel address is IPv6.)*

- **One reachable host** for `coordserver`, if you use a coordination server.

## Quick start

Prebuilt Linux binaries are attached to each [release](https://github.com/Yacin1102/Knoten/releases). 

### With a coordination server

This walks through one coordination server plus a fleet of nodes. Skip to [Without a server](#without-a-server) if every node already has a static IP.

#### 1. Start the coordination server

Generate a join token first. Every machine must present it to enrol. (Skip if you don't want a join token)

```bash
sudo mkdir -p /etc/knoten
openssl rand -base64 32 | sudo tee /etc/knoten/token > /dev/null
sudo chmod 600 /etc/knoten/token
```

Give the binary the right permissions:
```bash
chmod +x ./coordserver
```
Then start the server:

```bash
sudo ./coordserver \
  -token-file /etc/knoten/token #skip if you didn't generate one
```

Open TCP **8080** on the coordination server, and UDP **51820** (the WireGuard port) on every node.

> ⚠️ `coordserver` speaks **plain HTTP** and has no TLS of its own. Put it behind a reverse proxy that terminates TLS before exposing it to the internet. (*Native HTTPS support is planned for a future version.*) See [Security model](#security-model).

#### 2. Join your first machine
Give the binary the right permissions:
```bash
chmod +x ./meshd
```

Run interactive setup on the node:
```bash
sudo ./meshd -setup
```

Setup asks a short list of questions, writes `/etc/knoten/meshd.json` with a new key for this machine, and prints its public key and address.

Setup stores the token you paste inline in `meshd.json`, as `join_token`. To keep the token in its own file instead, edit `/etc/knoten/meshd.json` after setup and swap the field:

```json
{
  "server_url": "http://198.51.100.10:8080",
  "token_file": "/etc/knoten/token"
}
```

`token_file` wins when both are present: if it is set, `join_token` is ignored entirely. Setup never writes `token_file` for you, so this is always a manual edit.

`-setup` writes the config and exits. Then start `meshd` and keep it running:

```bash
sudo ./meshd
```

> You can run it in the background, or install it as a systemd service.

#### 3. Join the rest of the fleet

Repeat step 2 on every other machine, with the same mesh name. Each one gets its own key and its own address, and within one sync interval (30s by default) every node sees every other node of the same mesh.

#### 4. Verify

```bash
# You will need wireguard tools installed to use this command
sudo wg show knoten-wg 

# Can you reach a peer over the mesh? (its address is under "allowed ips" above)
ping <peer's address>
```

Send `SIGHUP` to any daemon to re-read its config and sync right away, instead of waiting for the next poll:

```bash
sudo pkill -HUP meshd
```

### Without a server

If every node has a static, reachable address, skip the server entirely. Run `meshd -setup`, leave the server URL empty, then enter the mesh name, this node's subnet, and each peer (its public key, subnet and endpoint) by hand.

Setup prints this machine's public key and address at the end, and `meshd -show-key` shows it again later. If you don't have the other machines' keys yet, run setup on every node first with no peers, then add them in `meshd.json` (see [The config file](#the-config-file)).

Start `meshd` on each node and keep it running, since the tunnel lives inside it. Changes to the peer list are picked up within a few seconds, without a restart.

## The config file

Everything a node needs is in `/etc/knoten/meshd.json`. `meshd -setup` writes it; after that you can edit it by hand, and a running `meshd` picks up your changes within a few seconds.

```json
{
  "server_url": "http://198.51.100.10:8080",
  "token_file": "/etc/knoten/token",
  "name": "node-1",
  "listen_port": 51820,
  "persistent_keepalive": 25,
  "private_key": "…",
  "mesh": "home",
  "subnet": "global",
  "accept_subnets": ["global", "lab"],
  "peers": [
    { "source": "static",  "name": "node-2", "public_key": "Q2v…=", "subnet": "global", "endpoint": "198.51.100.20:51820" },
    { "source": "synced",  "name": "node-3", "public_key": "m7T…=", "subnet": "lab",    "endpoint": "198.51.100.30:51820" },
    { "source": "blocked", "name": "old-laptop", "public_key": "Zr8…=" }
  ]
}
```

#### Settings

| Field                       | Meaning                                                                                                                                  |
| --------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| `mesh`                      | **Required.** The mesh's name, the same on every machine of the mesh. It sets the first 48 bits of every address.                        |
| `subnet`                    | This machine's subnet name. Setup writes `global` when you skip the question. A missing subnet is the name `""`, not `global`.           |
| `accept_subnets`            | The subnets whose peers this machine uses, or `["*"]` for all. Empty means only this machine's own subnet. A list accepts exactly those subnets, so include your own. |
| `server_url`                | The coordination server URL. Leave it out to run without one.                                                                            |
| `join_token` / `token_file` | The server's join token, inline or read from a `0600` file. `token_file` wins if both are set.                                           |
| `name`                      | This machine's name, sent to the server with every sync. Defaults to the hostname.                                                       |
| `listen_port`               | WireGuard's UDP port. Default `51820`.                                                                                                   |
| `persistent_keepalive`      | Seconds between keepalives, which keep NAT mappings open. `0` turns them off. Default `25`.                                              |
| `mtu`                       | The tunnel's MTU, `1280` or more (the IPv6 minimum). Leave it out for the default (`1420`).                                              |
| `private_key`               | This machine's identity. **Back it up:** without it, the machine gets a new identity.                                                    |
| `peers`                     | The peer list, see below.                                                                                                                |

#### Peers

Each peer has a `source`, which says who decides about it:

| `source`  | Written by | Meaning                                                                                          |
| --------- | ---------- | ------------------------------------------------------------------------------------------------ |
| `static`  | **you**    | Always in the tunnel. `meshd` never changes it.                                                  |
| `synced`  | `meshd`    | Received from the coordination server and kept up to date. Edits to these lines are overwritten. |
| `blocked` | **you**    | Never in the tunnel, even if the server sends it. Only `public_key` is needed.                   |

- **List each `public_key` once.** To keep a server peer even after the server drops it, change its `source` to `static`. To keep it out, change it to `blocked`.
- **`subnet` is the peer's subnet exactly as it is in that peer's own `meshd.json`**, because the peer's address is computed from it. A peer whose subnet is not accepted by `accept_subnets` is left out of the tunnel.
- `public_key` is the only required field. `endpoint` (`ip:port`) is optional: without it, this machine waits for the peer to connect first.

#### How changes apply

- `meshd` writes only its own parts: the `synced` peers, and `private_key` if it has to create one. Your settings and your `static` and `blocked` peers stay as you wrote them.
- A file with a mistake is reported in the log and left alone. `meshd` keeps the last good settings until you fix it.
- Changes to `server_url`, `join_token`, `token_file`, `mtu` and `private_key` take effect after a restart. Everything else applies within a few seconds.
- Changing `mesh` or `subnet` changes this machine's address, so the tunnel is recreated.

## Command-line reference

For a quick look without leaving the terminal:

```bash
coordserver -h
meshd -h
```

**Signals:** `SIGHUP` makes `meshd` re-read its config and sync immediately. `SIGINT` and `SIGTERM` stop it cleanly and remove the tunnel.

## Files and paths

| Path                                | Written by | Mode                          | Contents                                                                                                          |
| ----------------------------------- | ---------- | ----------------------------- | ----------------------------------------------------------------------------------------------------------------- |
| `/etc/knoten/meshd.json`            | `meshd`    | `0600`                        | Settings, private key and peers. See [The config file](#the-config-file). **Back this up.**                      |
| `/var/run/wireguard/knoten-wg.sock` | `meshd`    | `0600`-equivalent (root only) | WireGuard control socket. Exists while `meshd` runs.                                                              |
| `/etc/knoten/token`                 | you        | `0600`                        | Shared join token. Read by `coordserver -token-file`, and by `meshd` only when `token_file` is set in its config. |

> **Deleting `private_key` from `meshd.json` gives the machine a brand-new identity, and with it a new address**, because the address comes from the key. Every peer has to learn the new key. The server forgets the old one after `-peer-timeout`.

The interface is always named `knoten-wg`.

## Security model

**What Knoten protects today**

- **Traffic is end-to-end encrypted by WireGuard**, peer to peer. The coordination server sees metadata: public keys, names, subnet names, mesh networks and endpoints, held in memory only.

- **Private keys never leave the machine.** They are generated locally and stored in `meshd.json`, which `meshd` always writes `0600`. That file _is_ the machine's identity: don't share it, paste it, or commit it.

- **The coordination server is for discovery only.** It passes on what each machine says about itself, and keeps no database or files. Restarting it loses nothing: machines re-announce themselves on their next sync.

- **Each node has the final say over its own tunnel.** A `blocked` peer stays out whatever the server sends, and `static` peers stay even if the server drops them.

**Known limitations**

- **No TLS.** `coordserver` serves plain HTTP. Terminate TLS in front of it (nginx, Caddy, Traefik) before exposing it publicly, and set `-trust-proxy` only when your proxy overwrites `X-Forwarded-For`.

- **One shared token for the whole fleet.** Any machine holding it can enroll and read the peer list. There is no per-machine credential and no revocation yet. See the [roadmap](#roadmap).

- **No admin surface yet.** The server keeps nothing on disk and forgets a machine after 90 seconds of silence. A machine that keeps syncing can't be removed from the server. On each node, you can keep it out of the tunnel by changing its `source` to `blocked` in `meshd.json`.

## Roadmap

As a student, I am constantly **learning while building** Knoten. This is the project's planned roadmap:

| What we’ll be building                                  | Progress | **Details**                                                                                                                                                                                                                                                                            |
| ------------------------------------------------------- | -------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| WireGuard config generator \| v0.1                      | ✅        | -                                                                                                                                                                                                                                                                                      |
| Coordination server \| v0.2.0-alpha                     | ✅        | -                                                                                                                                                                                                                                                                                      |
| Wg-go + Light optimizations \| v0.3.0-alpha             | ✅        | - **Embed Wireguard into knoten through wg-go package**, replacing the config file writing mechanism.<br>- Replace the current shell-out to `wg` and `wg-quick`.                                                                                                                       |
| Peer memory & single configuration file \| v0.3.1-alpha | ✅        | - **Remember the last peer list** across restarts, so a node comes back with its peers even while the coordination server is unreachable.<br>- **One configuration file** instead of separate standalone and coordinated modes: a coordination server, manually listed peers, or both. |
| IPv6 migration \| v0.3.2-alpha                          | ✅        | - **IPv6 addresses derived from each machine's public key, subnet and meshID**, so its address follows from its identity.<br>- **No address assignment by the coordination server**, one step towards needing it less.<br>- **Rebuild the coord server codebase**                      |
| windows support                                         | -        | -                                                                                                                                                                                                                                                                                      |
| Redundant control-plane coordination                    | -        | - **Multiple coordination servers**, so the control plane has no single point of failure.                                                                                                                                                                                              |
| Relay                                                   | -        | - **Last-resort relay** for peers that genuinely cannot reach each other. (Even then, relayed packets stay sealed and end-to-end encrypted, unreadable by the relay.)                                                                                                                  |
| NAT support                                             | -        | -                                                                                                                                                                                                                                                                                      |
| Identity & access management                            | -        | - **Per-machine credentials, leases and revocation**, replacing the single shared join token and realising the "access is a lease" principle.                                                                                                                                          |
| Management console (UI)                                 | -        | -                                                                                                                                                                                                                                                                                      |
| Hardening                                               | -        | -                                                                                                                                                                                                                                                                                      |


## Contributing

This is a student-driven project, and contributions are genuinely welcome: bug reports, suggestions, design critique, and documentation just as much as code.

Please open an issue to discuss anything substantial before sending a large pull request. Because the project documents its own development journey, PRs that change design decisions are more useful when they explain the reasoning, not only the diff.

Thank you.

## License

Released under the [MIT License](LICENSE).
