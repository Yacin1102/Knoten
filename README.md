# Knoten - A self-hostable mesh VPN
> ⚠️ **Status: Early development.** The project is actively being built. Nothing is ready yet.
> Note that this version would be the first to include a working coordination server.

Knoten connects your machines (servers, cloud instances, VMs, laptops, and more) through **direct, end-to-end encrypted WireGuard tunnels**, without funneling traffic through one central point (e.g., a VPN server).

Coordination servers may still exist, but they only handle **discovery, identity, and permissions** when needed. They never carry data. We may however use a **last-resort relay** when no direct path is possible, and even then only as sealed, end-to-end encrypted packets that cannot be read.

---
## Design principles

1. **Direct p2p connection by default.** The coordination layer operates on an as-needed basis, and traffic relaying is strictly a fallback mechanism used only when a direct peer-to-peer connection is impossible.

| Architecture Type   | Network Scenario                                                                                                  | Infrastructure Outcome                                                                                                                      |
| ------------------- | ----------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| **Serverless**      | All nodes are reachable via static IPs.                                                                           | **Pure P2P:** pure peer-to-peer communication. **No coordination infrastructure needed.**                                                   |
| **Server-Required** | Some nodes are behind strict NATs / dynamic IPs.<br><br>Explicit identity and permission management is mandatory. | **Dedicated Server(s):** the network relies on dedicated coordination server(s). Static nodes can handle both peer and coordination duties. |

2. **Resilient by design.** Tunnels never depend on the coordination layer to stay alive. Existing connections are sustained natively, meaning losing a coordinator restricts only network administration. However, this resiliency assumes relatively stable peers; radical, simultaneous, and complex changes cannot be reconciled until coordination is restored.
    
3. **Control reachability, not actions.** This project never decides what happens inside a connection; that belongs to the tools that already govern it (database permissions, OAuth scopes).
    
4. **Everything is a principal.** One identity model for everything that can hold a key, a server that lives for years and a process that lives for twenty minutes are treated the same way.
    
5. **Access is a lease, never a possession.** Every grant carries an expiry and persists only through renewal. Revocation is simply expiry-now, so removing access is never a special operation, and abandoned access decays to nothing on its own.
    

## Our future Direction: Native AI Agent Identity

> **Planned, will be in development when a fully standard stable version is built.**

Since this project already treats a peer the same whether it lives for years or minutes, it is a natural fit for **short-lived, per-task identities**: access to exactly what a task needs, for exactly as long as it needs it, with a record of what happened, and the identity disappears the moment the task ends.
