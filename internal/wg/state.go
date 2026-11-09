// Package wg runs the WireGuard tunnel, knoten-wg, with wireguard-go inside
// the meshd process. The tunnel exists only while that process holds it.
//
// Config describes the tunnel you want. Tunnel.Apply sends the device only
// what changed since the last Apply, so unchanged peers keep their sessions.
// The tunnel also gets a control socket, so wg(8) and other tools can find
// and manage it.
//
// Only Linux is supported for now.
package wg

// state.go holds the typed, in-memory description of the tunnel: this machine's
// interface settings and its peers. Values stay typed
// and are only turned into strings at the edges, when stored or sent.

import (
	"net/netip"
	"time"

	"github.com/Yacin1102/Knoten/internal/key"
)

type Interface struct {
	PrivateKey key.Key
	ListenPort int
}

// Peer is one remote machine the tunnel talks to.
type Peer struct {
	Name                string
	PublicKey           key.Key
	AllowedIPs          []netip.Prefix
	Endpoint            netip.AddrPort
	PersistentKeepalive time.Duration
}

// Config is the whole desired state of the tunnel.
type Config struct {
	Interface Interface
	Peers     []Peer
}
