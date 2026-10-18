package wg

// runtime.go is for the platform-independent side of applying the tunnel:
// turning the state from state.go into a live WireGuard device through wgctrl,
// instead of rendering a wg-quick config file (config.go, to be removed).


// Config file making process (Will be removed soon, just moved it here to reduce duplicate files) \/

import (
	"fmt"
	"path/filepath"
	"strings"
	"strconv"	
)

const DefaultListenPort = 51820

const DefaultKeepaliveSeconds = 25

func Render(iface Interface, peers []Peer1) string {
	
	var b strings.Builder

	// [Interface] section
	b.WriteString("[Interface]\n")

	writeKV(&b, "PrivateKey", iface.PrivateKey.String())
	writeKV(&b, "Address", iface.Address.String())
	writeKV(&b, "ListenPort", strconv.Itoa(iface.ListenPort))
	//writeKV(&b, "MTU", strconv.Itoa(iface.MTU))

	// [Peer] section per peer
	for _, p := range peers {
		if strings.TrimSpace(p.PublicKey) == "" {
			continue
		}

		b.WriteString("\n")

		if name := strings.TrimSpace(p.Name); name != "" {

			b.WriteString("# ")
			b.WriteString(sanitiseComment(name))
			b.WriteString("\n")
		}

		b.WriteString("[Peer]\n")
		writeKV(&b, "PublicKey", p.PublicKey)
		writeKV(&b, "AllowedIPs", p.AllowedIPs)
		writeKV(&b, "Endpoint", p.Endpoint)

		if p.PersistentKeepalive > 0 {
			fmt.Fprintf(&b, "PersistentKeepalive = %d\n", p.PersistentKeepalive)
		}
	}

	return b.String()
}

func writeKV(b *strings.Builder, key, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	
	fmt.Fprintf(b, "%s = %s\n", key, value)
}

func sanitiseComment(s string) string {
	return strings.NewReplacer("\n", " ", "\r", " ").Replace(s)
}

func ConfigPath(dir, tunnelName string) string {
	return filepath.Join(dir, tunnelName+".conf")
}