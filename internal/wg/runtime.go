package wg

// runtime.go turns a Config into commands for the embedded wireguard-go device.
// It talks to the device directly (IpcSet), in the same text format that wg(8)
// sends over the control socket: https://www.wireguard.com/xplatform/

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"knoten/internal/key"
)

const DefaultListenPort = 51820

const DefaultKeepaliveSeconds = 25

// Changes counts what one Apply did, for logging.
type Changes struct {
	Interface               bool // private key or listen port changed
	Added, Updated, Removed int
}

// Apply makes the device match want. It compares want with what Knoten applied
// last time and sends only the difference, so unchanged peers keep their
// sessions and peers added by other tools are left alone.
func (t *Tunnel) Apply(want Config) (Changes, error) {
	cmds, ch := diff(t.applied, want)
	if ch == (Changes{}) {
		return ch, nil // already up to date
	}

	if err := t.dev.IpcSet(cmds); err != nil {
		return Changes{}, fmt.Errorf("could not configure %s: %w", TunnelName, err)
	}

	t.applied = want
	return ch, nil
}

// diff writes the commands that turn the device from applied into want.
func diff(applied, want Config) (string, Changes) {
	var b strings.Builder
	var ch Changes

	if want.Interface.PrivateKey != applied.Interface.PrivateKey {
		fmt.Fprintf(&b, "private_key=%s\n", want.Interface.PrivateKey.KeyInHex())
		ch.Interface = true
	}

	if want.Interface.ListenPort != applied.Interface.ListenPort {
		fmt.Fprintf(&b, "listen_port=%d\n", want.Interface.ListenPort)
		ch.Interface = true
	}

	before := make(map[key.Key]Peer, len(applied.Peers))
	for _, p := range applied.Peers {
		before[p.PublicKey] = p
	}

	for _, p := range want.Peers {
		old, existed := before[p.PublicKey]
		delete(before, p.PublicKey)

		switch {
		case !existed:
			ch.Added++
		case p.sameSettings(old):
			continue
		default:
			ch.Updated++
		}
		p.writeTo(&b)
	}

	for pub := range before {
		fmt.Fprintf(&b, "public_key=%s\nremove=true\n", pub.KeyInHex())
		ch.Removed++
	}

	return b.String(), ch
}

// sameSettings reports whether p and q would configure the device the same way.
func (p Peer) sameSettings(q Peer) bool {
	return p.Endpoint == q.Endpoint &&
		p.PersistentKeepalive == q.PersistentKeepalive &&
		slices.Equal(p.AllowedIPs, q.AllowedIPs)
}

// writeTo writes p as a peer block. Sending a peer that already exists updates
// it in place without dropping its session.
func (p Peer) writeTo(b *strings.Builder) {
	fmt.Fprintf(b, "public_key=%s\n", p.PublicKey.KeyInHex())
	if p.Endpoint.IsValid() {
		fmt.Fprintf(b, "endpoint=%s\n", p.Endpoint)
	}
	fmt.Fprintf(b, "persistent_keepalive_interval=%d\n", int(p.PersistentKeepalive/time.Second))
	b.WriteString("replace_allowed_ips=true\n")
	for _, a := range p.AllowedIPs {
		fmt.Fprintf(b, "allowed_ip=%s\n", a)
	}
}
