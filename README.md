# Knoten - A self-hostable mesh VPN

> 🟢 **Status: The first working version is available**, features, bugs are still being reviewed and tested.

Connect your machines (servers, cloud instances, VMs, laptops, and more) through **direct, end-to-end encrypted WireGuard tunnels**, without funneling traffic through one central point (e.g., a VPN server).

Coordination server(s) may still exist, but they only handle **discovery, identity, and permissions** when needed. They never carry data. However you may need a **last-resort relay** when no direct path is possible, and even then only as sealed, end-to-end encrypted packets that cannot be read.

---
## Design principles

1. **Direct p2p connection by default.** The coordination layer operates on an as-needed basis, and traffic relaying is strictly a fallback mechanism used only when a direct peer-to-peer connection is impossible.

| Architecture Type   | Network Scenario                                                                                            | Infrastructure Outcome                                                                                                                      |
| ------------------- | ----------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| **Serverless**      | All nodes are reachable via static IPs.                                                                     | **No coordination server. Use `meshd` in standalone mode. (Pure P2P)**                                                                      |
| **Server-Required** | All peers behind NAT, or with changing IPs<br><br>Explicit identity and permission management is mandatory. | **Dedicated Server(s):** the network relies on dedicated coordination server(s). Static nodes can handle both peer and coordination duties. |

2. **Resilient by design.** Tunnels never depend on the coordination layer to stay alive. Existing connections are sustained natively, meaning losing a coordinator restricts only network administration. However, this resiliency assumes relatively stable peers; radical, simultaneous, and complex changes cannot be reconciled until coordination is restored.
    
3. **Access is a lease, never a possession.** Every grant carries an expiry and persists only through renewal. Revocation is simply expiry-now, so removing access is never a special operation, and abandoned access decays to nothing on its own.

*README will include quick start and more details soon.*

