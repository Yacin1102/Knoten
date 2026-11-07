// Package ip6gen gives machines their tunnel addresses and decides which
// subnets are accepted. An address depends only on the mesh name, the subnet
// name and the public key, so a machine can compute any peer's address from
// the peer's key and subnet.
//
// The default subnet, "global", is 0000 instead of a hash.
package ip6gen

import (
	"crypto/sha256"
	"net/netip"

	"knoten/internal/key"
)

// NetworkBits is the length of the part every machine in a mesh shares.
const NetworkBits = 48

// AllSubnets in accept_subnets accepts every subnet.
const AllSubnets = "*"

// DefaultSubnet is the subnet setup gives when none is entered. Its part of
// the address is 0000.
const DefaultSubnet = "global"

// Address returns the address of the machine with public key pub in subnet of mesh.
func Address(mesh, subnet string, pub key.Key) netip.Addr {
	m := sha256.Sum256(append([]byte("knoten-mesh:"), mesh...))
	h := sha256.Sum256(append([]byte("knoten-host:"), pub[:]...))

	var a [16]byte
	a[0] = 0xfd
	copy(a[1:6], m[:5])
	if subnet != DefaultSubnet { // the default subnet stays 0000
		s := sha256.Sum256(append([]byte("knoten-subnet:"), subnet...))
		copy(a[6:8], s[:2])
	}
	copy(a[8:], h[:8])
	return netip.AddrFrom16(a)
}

// Rules decide which peers' subnets this machine accepts.
type Rules struct {
	all     bool            // every subnet is accepted
	subnets map[string]bool // accepted subnets
}

// NewRules returns the rules of a machine in subnet own. accept lists the
// accepted subnets, or AllSubnets for all; when it is empty, only own is.
func NewRules(own string, accept []string) Rules {
	if len(accept) == 0 {
		accept = []string{own}
	}
	r := Rules{subnets: map[string]bool{}}
	for _, name := range accept {
		if name == AllSubnets {
			r.all = true
		} else {
			r.subnets[name] = true
		}
	}
	return r
}

// Accepts reports whether a peer in subnet may be used.
func (r Rules) Accepts(subnet string) bool {
	return r.all || r.subnets[subnet]
}
