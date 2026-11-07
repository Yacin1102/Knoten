// Package peerbook is Knoten's list of peers. Every peer in meshd.json is
// tagged with its source:
//
//   - "static": manual. meshd never changes it.
//   - "synced": from the coordination server. meshd keeps it up to date.
//   - "blocked": manual. The peer is never used, even if the server sends it.
//
// Each key is listed once. Pin or block a server peer by changing its tag;
// the server's copy of that key is then ignored.
package peerbook

import (
	"bytes"
	"net/netip"
	"slices"

	"knoten/internal/key"
)

// Source says where a peer comes from, and so who decides about it.
type Source string

const (
	Static  Source = "static"  // manually set: meshd never changes it
	Synced  Source = "synced"  // the coordination server's: meshd keeps it up to date
	Blocked Source = "blocked" // manually set: never used, whatever the server says
)

// Peer is one peer, exactly as it is written in meshd.json.
type Peer struct {
	Source    Source         `json:"source"`
	Name      string         `json:"name,omitempty"`
	PublicKey key.Key        `json:"public_key"`
	Subnet    string         `json:"subnet,omitempty"`
	Endpoint  netip.AddrPort `json:"endpoint,omitzero"`
}

// Book holds the peers from every source.
type Book struct {
	static  map[key.Key]Peer
	blocked map[key.Key]Peer
	synced  map[key.Key]Peer // never holds a key that is static or blocked
	dirty   bool             // the server's list changed since the last save
}

// same reports whether p and q are identical. The endpoint counts like every
// other field: after a restart with the server down, the remembered endpoint
// is the only way to reach a peer.
func (p Peer) same(q Peer) bool {
	return p.Source == q.Source &&
		p.Name == q.Name &&
		p.Endpoint == q.Endpoint &&
		p.Subnet == q.Subnet
}

// Only returns the peers in list that have source s.
func Only(list []Peer, s Source) []Peer {
	var out []Peer
	for _, p := range list {
		if p.Source == s {
			out = append(out, p)
		}
	}
	return out
}

func New() *Book {
	return &Book{
		static:  map[key.Key]Peer{},
		blocked: map[key.Key]Peer{},
		synced:  map[key.Key]Peer{},
	}
}

// LoadFromConfig fills the book from meshd.json at startup, the remembered
// server peers included, so the tunnel has them before the server answers.
// Server peers are taken only when a server is used.
func (b *Book) LoadFromConfig(list []Peer, useServer bool) {
	b.static = byKey(Only(list, Static))
	b.blocked = byKey(Only(list, Blocked))

	// Validate allows each key only once, so no server entry here shares a
	// key with a static or blocked one.
	b.synced = map[key.Key]Peer{}
	if useServer {
		b.synced = byKey(Only(list, Synced))
	}
}

// SetFromServer replaces the server peers with the server's current list.
// Peers manually pinned (static) or blocked keep manual entry.
func (b *Book) SetFromServer(list []Peer) bool {
	next := make(map[key.Key]Peer, len(list))
	for _, p := range list {
		if isIn(p.PublicKey, b.static, b.blocked) {
			continue
		}
		p.Source = Synced
		next[p.PublicKey] = p
	}

	changed := !sameMaps(b.synced, next)
	if changed {
		b.dirty = true
	}
	b.synced = next
	return changed
}

// UpdateManualPeers is for edits to meshd.json while meshd runs. It takes the
// static and blocked peers from the file and keeps the server's peers as the
// server sent them, except any key manually set static / blocked. An old copy
// of the file (an editor saving what it read earlier) can therefore not bring
// back an old server list; instead, the book is marked for saving, so meshd
// writes the current list back. It reports whether your peers changed.
func (b *Book) UpdateManualPeers(list []Peer, useServer bool) bool {
	static := byKey(Only(list, Static))
	blocked := byKey(Only(list, Blocked))
	changed := !sameMaps(b.static, static) || !sameMaps(b.blocked, blocked)
	b.static, b.blocked = static, blocked

	for k := range b.synced {
		if isIn(k, static, blocked) {
			delete(b.synced, k)
		}
	}

	if useServer && !sameMaps(byKey(Only(list, Synced)), b.synced) {
		b.dirty = true
	}
	return changed
}

// Peers returns the peers the tunnel should have, sorted by key: the static
// ones and the server ones. Blocked peers are left out.
func (b *Book) Peers() []Peer {
	out := make([]Peer, 0, len(b.static)+len(b.synced))
	for _, p := range b.static {
		out = append(out, p)
	}
	for _, p := range b.synced {
		out = append(out, p)
	}
	sortByKey(out)
	return out
}

// ServerPeers returns the server's peers, sorted by key, to be saved.
func (b *Book) ServerPeers() []Peer {
	out := make([]Peer, 0, len(b.synced))
	for _, p := range b.synced {
		out = append(out, p)
	}
	sortByKey(out)
	return out
}

// NeedsSave reports whether the server's list changed since the last save.
func (b *Book) NeedsSave() bool {
	return b.dirty
}

// MarkSaved records that meshd.json now holds the server's list.
func (b *Book) MarkSaved() {
	b.dirty = false
}

// isIn reports whether k is a key in any of the maps.
func isIn(k key.Key, maps ...map[key.Key]Peer) bool {
	for _, m := range maps {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}

// sameMaps reports whether a and b hold the same peers.
func sameMaps(a, b map[key.Key]Peer) bool {
	if len(a) != len(b) {
		return false
	}
	for k, p := range a {
		if q, ok := b[k]; !ok || !p.same(q) {
			return false
		}
	}
	return true
}

func byKey(list []Peer) map[key.Key]Peer {
	m := make(map[key.Key]Peer, len(list))
	for _, p := range list {
		m[p.PublicKey] = p
	}
	return m
}

func sortByKey(list []Peer) {
	slices.SortFunc(list, func(a, b Peer) int {
		return bytes.Compare(a.PublicKey[:], b.PublicKey[:])
	})
}
