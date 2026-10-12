package store

import (
	"errors"
	"fmt"
	"net/netip"
)

var ErrPoolExhausted = errors.New("no free VPN addresses left in the configured range")

// VPNRange is the address range machines are given addresses from.
//
// This is deliberately not configurable. The range is only ever consulted by 
// NextFreeIP, which runs once per machine, when a public key is first seen; the
// address it returns is then stored in the database and never recomputed. So
// changing the range does not move machines that are already enrolled so it only
// affects machines that join afterwards, silently splitting the mesh across two
// ranges. Making it settable again needs a re-IP migration that does not exist yet.
const VPNRange = "10.10.0.0/16"

// reservedHosts is how many addresses at the start of the range are never handed out to machines.
const reservedHosts = 2

// ReservedIPs returns the addresses in cidr that must never be handed out.
func ReservedIPs(cidr *netip.Prefix) map[string]bool {
	reserved := make(map[string]bool, reservedHosts)

	addr := cidr.Addr().Next() // first host address
	for i := 0; i < reservedHosts; i++ {
		reserved[addr.String()] = true
		addr = addr.Next()
	}

	return reserved
}

// NextFreeIP returns the next free IP address in the given range that is not in use or reserved.
func NextFreeIP(cidr *netip.Prefix, used map[string]bool) (string, error) {

	// Validate the prefix
	if cidr == nil {
		return "", errors.New("no address range configured")
	}

	reserved := ReservedIPs(cidr)

	addr := cidr.Addr()

	// Ensure we are working with an IPv4 address
	if !addr.Is4() {
		return "", fmt.Errorf("%s is not an IPv4 range (IPv6 is not supported yet)", cidr.String())
	}

	// Iterate over the range to find the next free IP
	for candidate := cidr.Addr().Next(); cidr.Contains(candidate.Next()); candidate = candidate.Next() {
		ip := candidate.String()
		if !used[ip] && !reserved[ip] {
			return ip, nil
		}
	}

	return "", ErrPoolExhausted
}

// ParseCIDR parses s as an IPv4 CIDR range that is large enough to hold a mesh.
func ParseCIDR(s string) (*netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(s)
	if err != nil {
		return nil, fmt.Errorf("%q is not a valid CIDR range like 10.10.0.0/16: %w", s, err)
	}

	if !prefix.Addr().Is4() {
		return nil, fmt.Errorf("%q is an IPv6 range; only IPv4 is supported today", s)
	}

	if prefix.Bits() > 29 {
		return nil, fmt.Errorf("range %q is too small: it must be /29 or larger to hold a useful mesh", s)
	}

	prefix = prefix.Masked()

	return &prefix, nil
}