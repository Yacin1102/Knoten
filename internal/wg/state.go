package wg

// state.go holds the typed, in-memory description of the tunnel: this machine's
// interface settings and its peers. Values stay typed (wgtypes.Key, netip.Prefix)
// and are only turned into strings at the edges, when stored or sent.

import (
	"net/netip"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

type Interface struct {
	PrivateKey wgtypes.Key
	Address netip.Prefix
	ListenPort int
	MTU int
}

type Peer1 struct {
	Name string
	PublicKey string
	AllowedIPs string
	Endpoint string
	PersistentKeepalive int
}