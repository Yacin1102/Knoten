package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/netip"
	"os"
	"slices"
	"strings"
	"time"

	"knoten/internal/client"
	"knoten/internal/ip6gen"
	"knoten/internal/key"
	"knoten/internal/peerbook"
	"knoten/internal/protocol"
	"knoten/internal/wg"
)

const (
	minBackoff = 1 * time.Second
	maxBackoff = 60 * time.Second

	tick = 5 * time.Second // tick is how often meshd checks meshd.json for edits.
)

type Daemon struct {
	cfg        Config_file // the settings in use
	configPath string
	log        *log.Logger
	http       *client.Client // nil without a coordination server
	pub        key.Key        // this machine's public key
	book       *peerbook.Book
	tun        *wg.Tunnel
	tunAddr    netip.Prefix // the address the tunnel was opened with

	addr  netip.Addr   // this machine's address
	rules ip6gen.Rules // which peers' subnets are accepted
	stale bool         // the peers or settings changed since the tunnel was last updated

	backoff  time.Duration
	nextSync time.Time // when to sync next (zero means now)

	newKey   bool   // a key meshd just made is not in meshd.json yet
	lastRead []byte // meshd.json as meshd last read or wrote it

	fileProblem, applyProblem, saveProblem quietLog
}

func New(cfg Config_file, configPath string, logger *log.Logger) (*Daemon, error) {
	d := &Daemon{
		cfg:        cfg,
		configPath: configPath,
		log:        logger,
		book:       peerbook.New(),
		backoff:    minBackoff,
	}

	if cfg.UsesServer() {
		token, err := cfg.ResolveToken()
		if err != nil {
			return nil, err
		}
		c, err := client.New(cfg.ServerURL, token)
		if err != nil {
			return nil, err
		}
		d.http = c
	}

	return d, nil
}

// Run keeps the tunnel matching the peer book until ctx is cancelled.
// bringUpToDate runs every tick, when a sync is due, on SIGHUP, and when the tunnel stops.
func (d *Daemon) Run(ctx context.Context, syncNow <-chan struct{}) error {
	d.loadPeers()
	d.checkConfig()

	if err := d.ensureKey(); err != nil {
		return err
	}
	defer d.closeTunnel()

	if d.cfg.UsesServer() {
		d.log.Printf("coordination server: %s", d.cfg.ServerURL)
	}
	d.log.Printf("this machine: %q, public key %s, address %s", d.cfg.MachineName(), d.pub, d.addr)

	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	for {
		if err := d.bringUpToDate(ctx); err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return nil
		case <-syncNow:
			d.nextSync = time.Time{}
		case <-ticker.C:
		case <-d.syncDue():
		case <-d.tunnelStopped():
		}
	}
}

// bringUpToDate picks up edits to meshd.json, makes the tunnel match the peer
// book, syncs with the server when due, and saves what is worth remembering.
// Each call is one pass. The tunnel is updated before the sync as well, so it
// never waits for the server.
func (d *Daemon) bringUpToDate(ctx context.Context) error {
	d.dropDeadTunnel()
	d.checkConfig()

	if err := d.updateTunnel(); err != nil {
		return err
	}
	if d.syncIfDue(ctx) {
		if err := d.updateTunnel(); err != nil {
			return err
		}
	}

	d.saveIfNeeded()
	return nil
}

// updateTunnel makes the tunnel match the peer book. The peers are only
// checked and applied when something changed since the last update.
func (d *Daemon) updateTunnel() error {
	if err := d.ensureTunnel(); err != nil {
		return err
	}
	if !d.stale {
		return nil
	}
	if err := d.applyPeers(); err != nil {
		d.applyProblem.Printf(d.log, "could not update %s (will retry): %v", wg.TunnelName, err)
		return nil
	}
	d.stale = false
	d.applyProblem.Clear()
	return nil
}

// ensureKey makes sure this machine has a private key. A new key is saved right away
// (losing it would give the machine a new identity).
func (d *Daemon) ensureKey() error {
	if d.cfg.PrivateKey.IsZero() {
		d.cfg.PrivateKey = key.GenerateKeyPair().PrivateKey
		d.newKey = true
		if err := d.saveFile(); err != nil {
			return fmt.Errorf("could not save the new key: %w", err)
		}
		d.log.Printf("generated a new key for this machine")
	}
	d.pub = d.cfg.PrivateKey.PublicKey()
	d.useAddress()
	return nil
}

// useAddress computes this machine's address and rules from its key, mesh and subnets.
func (d *Daemon) useAddress() {
	d.addr = ip6gen.Address(d.cfg.Mesh, d.cfg.Subnet, d.pub)
	d.rules = ip6gen.NewRules(d.cfg.Subnet, d.cfg.AcceptSubnets)
}

// loadPeers puts the peers in meshd.json in the book, so the tunnel has them,
// the remembered server peers included, before the server answers.
func (d *Daemon) loadPeers() {
	d.book.LoadFromConfig(d.cfg.Peers)

	n := len(peerbook.WithSource(d.cfg.Peers, peerbook.Synced))
	switch {
	case n == 0:
	case !d.cfg.UsesServer():
		d.log.Printf("using %d synced peer(s) as written; without server_url nothing updates them", n)
	default:
		d.log.Printf("using %d remembered synced peer(s) until the server answers", n)
	}
}

// checkConfig looks for edits to meshd.json and puts them in use.
func (d *Daemon) checkConfig() {
	data, err := os.ReadFile(d.configPath)
	if err != nil {
		d.fileProblem.Printf(d.log, "could not read %s, keeping the current settings: %v", d.configPath, err)
		return
	}
	if bytes.Equal(data, d.lastRead) {
		return // nothing changed since meshd last read or wrote it
	}
	d.lastRead = data

	cfg, err := parseConfig(data)
	if err != nil {
		d.fileProblem.Printf(d.log, "%s has an error, keeping the current settings until it is fixed: %v", d.configPath, err)
		return
	}
	d.fileProblem.Clear()
	d.useEdits(cfg)
}

// useEdits puts an edited meshd.json in use. (The file is an order)
func (d *Daemon) useEdits(cfg Config_file) {
	var changed []string

	if d.book.UpdateManualPeers(cfg.Peers, d.cfg.UsesServer()) {
		changed = append(changed, "peers")
	}
	if cfg.ListenPort != d.cfg.ListenPort {
		d.cfg.ListenPort = cfg.ListenPort
		changed = append(changed, "listen_port")
	}
	if cfg.PersistentKeepalive != d.cfg.PersistentKeepalive {
		d.cfg.PersistentKeepalive = cfg.PersistentKeepalive
		changed = append(changed, "persistent_keepalive")
	}
	if cfg.Mesh != d.cfg.Mesh {
		d.cfg.Mesh = cfg.Mesh
		changed = append(changed, "mesh")
	}
	if cfg.Subnet != d.cfg.Subnet {
		d.cfg.Subnet = cfg.Subnet
		changed = append(changed, "subnet")
	}
	if !slices.Equal(cfg.AcceptSubnets, d.cfg.AcceptSubnets) {
		d.cfg.AcceptSubnets = cfg.AcceptSubnets
		changed = append(changed, "accept_subnets")
	}

	d.cfg.Name = cfg.Name

	if len(changed) > 0 {
		d.stale = true
		d.useAddress()
		d.log.Printf("%s changed: %s", d.configPath, strings.Join(changed, ", "))
	}

	if cfg.ServerURL != d.cfg.ServerURL || cfg.JoinToken != d.cfg.JoinToken ||
		cfg.TokenFile != d.cfg.TokenFile || cfg.MTU != d.cfg.MTU || cfg.PrivateKey != d.cfg.PrivateKey {
		d.log.Printf("%s: changes to server_url, join_token, token_file, mtu or private_key take effect after a restart", d.configPath)
	}

}

// syncDue fires when the next sync is due. Without a server it is nil, which never fires.
func (d *Daemon) syncDue() <-chan time.Time {
	if d.http == nil {
		return nil
	}
	return time.After(time.Until(d.nextSync))
}

// syncIfDue syncs with the server when it is time, and plans the next sync:
// the server's interval after a success, a growing backoff after a failure.
// It reports whether a sync succeeded.
func (d *Daemon) syncIfDue(ctx context.Context) bool {
	if d.http == nil || time.Now().Before(d.nextSync) {
		return false
	}

	interval, err := d.syncServer(ctx)
	if err != nil {
		wait := d.nextBackoff()
		d.nextSync = time.Now().Add(wait)
		if ctx.Err() == nil {
			d.log.Printf("sync failed (will retry in %s): %v", wait.Round(time.Second), err)
		}
		return false
	}

	if interval <= 0 {
		interval = protocol.DefaultSyncIntervalSeconds * time.Second
	}
	d.backoff = minBackoff
	d.nextSync = time.Now().Add(interval)
	return true
}

// syncServer asks the server for the current peers, registering first if the
// server does not know this machine, and puts them in the book. It returns how
// long the server wants until the next sync.
func (d *Daemon) syncServer(ctx context.Context) (time.Duration, error) {

	resp, err := d.http.Sync(ctx, d.syncRequest())
	if err != nil {
		return 0, err
	}

	if d.book.SetFromServer(d.serverPeers(resp.Peers)) {
		d.stale = true
	}
	return time.Duration(resp.SyncIntervalSeconds) * time.Second, nil
}

func (d *Daemon) syncRequest() protocol.SyncRequest {
	return protocol.SyncRequest{
		PublicKey:  d.pub.String(),
		ListenPort: d.cfg.ListenPort,
		Subnet:     d.cfg.Subnet,
		Network:    d.network(),
		Name:       d.cfg.MachineName(),
	}
}

// network is this machine's network part (fd + mesh hash) as a /48. Peers
// from the server are only used if theirs is the same.
func (d *Daemon) network() string {
	return netip.PrefixFrom(d.addr, ip6gen.NetworkBits).Masked().String()
}

// serverPeers turns the server's peer list into peer book entries, skipping
// peers of other meshes and any the server sent in a broken form.
func (d *Daemon) serverPeers(list []protocol.Peer) []peerbook.Peer {
	out := make([]peerbook.Peer, 0, len(list))
	own := d.network()
	for _, p := range list {
		if p.Network != own {
			continue // a machine of another mesh on the same server
		}
		peer, err := peerFromServer(p)
		if err != nil {
			d.log.Printf("skipping peer %q from the server: %v", p.Name, err)
			continue
		}
		out = append(out, peer)
	}
	return out
}

// peerFromServer turns a server peer into a peer book entry, or returns an error if the server sent it in a broken form.
func peerFromServer(p protocol.Peer) (peerbook.Peer, error) {
	pub, err := key.ParseKey(p.PublicKey)
	if err != nil {
		return peerbook.Peer{}, fmt.Errorf("public key: %w", err)
	}

	peer := peerbook.Peer{
		Source:    peerbook.Synced,
		Name:      p.Name,
		PublicKey: pub,
		Subnet:    strings.TrimSpace(p.Subnet),
	}
	if peer.Subnet == ip6gen.AllSubnets {
		return peerbook.Peer{}, errors.New(`subnet "*" is not valid`)
	}
	if ep := strings.TrimSpace(p.Endpoint); ep != "" {
		if peer.Endpoint, err = netip.ParseAddrPort(ep); err != nil {
			return peerbook.Peer{}, fmt.Errorf("endpoint %q: %w", ep, err)
		}
	}
	return peer, nil
}

// ensureTunnel opens the tunnel if there is none, and recreates it if this
// machine's address changed (mesh or subnet edited). A new tunnel starts
// empty, so it marks the peers for applying.
func (d *Daemon) ensureTunnel() error {
	addr := d.ifaceAddr()
	if d.tun != nil && d.tunAddr != addr {
		d.log.Printf("our address changed to %s; recreating %s", addr, wg.TunnelName)
		d.closeTunnel()
	}
	if d.tun != nil {
		return nil
	}

	t, err := wg.OpenTunnel(addr, d.cfg.MTU, d.log.Printf)
	if err != nil {
		return err
	}
	d.tun, d.tunAddr = t, addr
	d.stale = true
	d.log.Printf("brought %s up with address %s", wg.TunnelName, addr)
	return nil
}

// ifaceAddr is this machine's address with the mesh's prefix length, so every
// subnet of the mesh is routed into the tunnel.
func (d *Daemon) ifaceAddr() netip.Prefix {
	return netip.PrefixFrom(d.addr, ip6gen.NetworkBits)
}

// applyPeers makes the tunnel match the peer book. Peers in a subnet this
// machine does not accept are left out.
func (d *Daemon) applyPeers() error {
	want := wg.Config{Interface: wg.Interface{
		PrivateKey: d.cfg.PrivateKey,
		ListenPort: d.cfg.ListenPort,
	}}

	keepalive := time.Duration(d.cfg.PersistentKeepalive) * time.Second
	for _, p := range d.book.Peers() {
		if p.PublicKey == d.pub {
			continue
		}
		if !d.rules.Accepts(p.Subnet) {
			d.log.Printf("leaving out peer %q: subnet %q is not accepted", p.Name, p.Subnet)
			continue
		}

		addr := ip6gen.Address(d.cfg.Mesh, p.Subnet, p.PublicKey)
		want.Peers = append(want.Peers, wg.Peer{
			Name:                p.Name,
			PublicKey:           p.PublicKey,
			AllowedIPs:          []netip.Prefix{netip.PrefixFrom(addr, 128)},
			Endpoint:            p.Endpoint,
			PersistentKeepalive: keepalive,
		})
	}

	ch, err := d.tun.Apply(want)
	if err != nil {
		return err
	}
	// Settings changes are logged where they happen (useEdits, ensureTunnel).
	if ch.Added+ch.Updated+ch.Removed > 0 {
		d.log.Printf("synced %s: %d added, %d updated, %d removed",
			wg.TunnelName, ch.Added, ch.Updated, ch.Removed)
	}
	return nil
}

// saveIfNeeded writes meshd's parts of meshd.json when something worth
// remembering changed. A failed write is retried on the next pass; the
// tunnel does not depend on it.
func (d *Daemon) saveIfNeeded() {
	if !d.book.NeedsSave() {
		return
	}
	if err := d.saveFile(); err != nil {
		d.saveProblem.Printf(d.log, "could not save to %s (will retry): %v", d.configPath, err)
		return
	}
	d.saveProblem.Clear()
}

// saveFile writes meshd's parts into meshd.json and leaves manually added entries as they are.
func (d *Daemon) saveFile() error {
	data, err := os.ReadFile(d.configPath)
	if err != nil {
		return err
	}
	disk, err := parseConfig(data)
	if err != nil {
		return fmt.Errorf("it has an error, so it is left as it is: %w", err)
	}
	if !bytes.Equal(data, d.lastRead) {
		d.useEdits(disk) // edited since the last check
	}

	if d.newKey {
		disk.PrivateKey = d.cfg.PrivateKey
	}
	if d.cfg.UsesServer() && disk.UsesServer() {
		disk.Peers = mergePeers(disk.Peers, d.book.ServerPeers())
	}

	out, err := marshalConfig(disk)
	if err != nil {
		return err
	}
	if err := WriteAtomic(d.configPath, string(out), 0o600); err != nil {
		return err
	}

	d.lastRead = out
	d.newKey = false
	d.book.MarkSaved()
	return nil
}

// mergePeers returns manually added entries (static and blocked) as they are on disk, followed by the server's peers
func mergePeers(onDisk, fromServer []peerbook.Peer) []peerbook.Peer {
	var out []peerbook.Peer
	manual := map[key.Key]bool{}
	for _, p := range onDisk {
		if p.Source != peerbook.Synced {
			out = append(out, p)
			manual[p.PublicKey] = true
		}
	}
	for _, p := range fromServer {
		if !manual[p.PublicKey] {
			out = append(out, p)
		}
	}
	return out
}

// closeTunnel removes the tunnel, if there is one.
func (d *Daemon) closeTunnel() {
	if d.tun == nil {
		return
	}
	d.tun.Close()
	d.tun = nil
	d.tunAddr = netip.Prefix{}
	d.log.Printf("removed %s", wg.TunnelName)
}

// tunnelStopped fires when the tunnel stops. With no tunnel it returns nil, which never fires.
func (d *Daemon) tunnelStopped() <-chan struct{} {
	if d.tun == nil {
		return nil
	}
	return d.tun.Stopped()
}

// dropDeadTunnel forgets a tunnel that was shut down from outside meshd, so
// this pass creates it again.
func (d *Daemon) dropDeadTunnel() {
	select {
	case <-d.tunnelStopped():
		d.log.Printf("%s was shut down from outside meshd; recreating it", wg.TunnelName)
		d.closeTunnel()
	default:
	}
}

func (d *Daemon) nextBackoff() time.Duration {
	current := d.backoff

	d.backoff *= 2
	if d.backoff > maxBackoff {
		d.backoff = maxBackoff
	}

	jitter := time.Duration(rand.Int63n(int64(current/4) + 1))
	return current + jitter
}

// quietLog logs a problem that comes back on every pass only once, and
// again only when the message changes or after Clear.
type quietLog struct {
	last string
}

func (q *quietLog) Printf(l *log.Logger, format string, args ...any) {
	if msg := fmt.Sprintf(format, args...); msg != q.last {
		l.Print(msg)
		q.last = msg
	}
}

// Clear forgets the last problem, so the next one is logged again.
func (q *quietLog) Clear() {
	q.last = ""
}

func ValidateListenPort(p int) error {
	if p < 1 || p > 65535 {
		return fmt.Errorf("listen port %d is out of range 1-65535", p)
	}

	return nil
}

func ValidateName(s string) error {
	if len(s) > 64 {
		return fmt.Errorf("name is %d characters, maximum is 64", len(s))
	}

	for _, r := range s {
		if r < 32 || r == 127 {
			return errors.New("name contains a control character")
		}
	}

	return nil
}