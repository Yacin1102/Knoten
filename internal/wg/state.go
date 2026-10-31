package wg

// state.go holds the typed, in-memory description of the tunnel: this machine's
// interface settings and its peers. Values stay typed
// and are only turned into strings at the edges, when stored or sent.

import (
	"net/netip"
)

type Interface struct {
	PrivateKey Key
	Address netip.Prefix
	ListenPort int
	MTU int
}

// This struct will be removed and replaced by the typed Structs.
type Peer1 struct {
	Name string
	PublicKey string
	AllowedIPs string
	Endpoint string
	PersistentKeepalive int
}

