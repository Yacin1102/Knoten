package wg

// state.go holds the typed, in-memory description of the tunnel: this machine's
// interface settings and its peers. Values stay typed
// and are only turned into strings at the edges, when stored or sent.

import (
	"net/netip"
	"time"

	"knoten/internal/key"
)

type Interface struct {
	PrivateKey key.Key
	Address    netip.Prefix
	ListenPort int
	MTU        int
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