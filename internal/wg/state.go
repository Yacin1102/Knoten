package wg

// state.go holds the typed, in-memory description of the tunnel: this machine's
// interface settings and its peers. Values stay typed
// and are only turned into strings at the edges, when stored or sent.

import (
	"fmt"
	"net/netip"
	"strings"
	"time"
)

type Interface struct {
	PrivateKey Key
	Address    netip.Prefix
	ListenPort int
	MTU        int
}

// Peer is one remote machine the tunnel talks to.
type Peer struct {
	Name                string
	PublicKey           Key
	AllowedIPs          []netip.Prefix
	Endpoint            netip.AddrPort
	PersistentKeepalive time.Duration
}

// Config is the whole desired state of the tunnel.
type Config struct {
	Interface Interface
	Peers     []Peer
}

// ParsePeer builds a Peer from the string form used by the config file and the server protocol.
// allowedIPs is a comma-separated list of prefixes.
func ParsePeer(name, publicKey, allowedIPs, endpoint string, keepaliveSeconds int) (Peer, error) {
	key, err := ParseKey(strings.TrimSpace(publicKey))
	if err != nil {
		return Peer{}, fmt.Errorf("public key: %w", err)
	}

	p := Peer{
		Name:                name,
		PublicKey:           key,
		PersistentKeepalive: time.Duration(keepaliveSeconds) * time.Second,
	}

	for _, s := range strings.Split(allowedIPs, ",") {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(s))
		if err != nil {
			return Peer{}, fmt.Errorf("allowed IP %q: %w", s, err)
		}
		p.AllowedIPs = append(p.AllowedIPs, prefix.Masked())
	}

	if endpoint = strings.TrimSpace(endpoint); endpoint != "" {
		p.Endpoint, err = netip.ParseAddrPort(endpoint)
		if err != nil {
			return Peer{}, fmt.Errorf("endpoint %q: %w", endpoint, err)
		}
	}

	return p, nil
}
