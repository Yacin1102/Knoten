package store

import (
	"errors"
	"fmt"
	"net"
)

var ErrPoolExhausted = errors.New("no free VPN addresses left in the configured range")

const reservedHostSuffix = 1

func NextFreeIP(cidr *net.IPNet, used map[string]bool) (string, error) {
	if cidr == nil {
		return "", errors.New("no address range configured")
	}

	base := cidr.IP.To4()

	if base == nil {
		return "", fmt.Errorf("%s is not an IPv4 range (IPv6 is not supported yet)", cidr.String())
	}

	mask := net.IP(cidr.Mask).To4()
	
	if mask == nil {
		return "", fmt.Errorf("%s does not have a usable IPv4 mask", cidr.String())
	}

	network := ipToUint32(base) & ipToUint32(mask) 

	broadcast := network | ^ipToUint32(mask)

	for candidate := network + 1; candidate < broadcast; candidate++ {
		
		if candidate == network+reservedHostSuffix {
			continue
		}

		ip := uint32ToIP(candidate).String()

		if !used[ip] {
			return ip, nil
		}
	}

	return "", ErrPoolExhausted
}

func ipToUint32(ip net.IP) uint32 {
	return uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
}

func uint32ToIP(n uint32) net.IP {
	return net.IPv4(byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
}

func ParseCIDR(s string) (*net.IPNet, error) {

	_, network, err := net.ParseCIDR(s)

	if err != nil {
		return nil, fmt.Errorf("%q is not a valid CIDR range like 10.42.0.0/16: %w", s, err)
	}

	if network.IP.To4() == nil {
		return nil, fmt.Errorf("%q is an IPv6 range; only IPv4 is supported today", s)
	}

	ones, bits := network.Mask.Size()

	if bits-ones < 3 {
		return nil, fmt.Errorf("range %q is too small: it must be /29 or larger to hold a useful mesh", s)
	}

	return network, nil
}