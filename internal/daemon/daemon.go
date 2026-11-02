package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/netip"
	"time"

	"knoten/internal/client"
	"knoten/internal/protocol"
	"knoten/internal/wg"
)

const (
	minBackoff = 1 * time.Second
	maxBackoff = 60 * time.Second
)

type Daemon struct {
	cfg     Config_file
	log     *log.Logger
	http    *client.Client
	state   State_file
	tun     *wg.Tunnel
	backoff time.Duration
}

func New(cfg Config_file, logger *log.Logger) (*Daemon, error) {
	d := &Daemon{
		cfg:     cfg,
		log:     logger,
		backoff: minBackoff,
	}

	if cfg.UseCoordinationServer {
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

func (d *Daemon) Run(ctx context.Context, refresh <-chan struct{}) error {
	if err := d.ensureKeyPair(); err != nil {
		return err
	}

	defer d.closeTunnel()

	if !d.cfg.UseCoordinationServer {
		return d.runStandalone(ctx)
	}

	return d.runCoordinated(ctx, refresh)
}

func (d *Daemon) ensureKeyPair() error {
	st, err := LoadState(d.cfg.StatePath)
	if err != nil {
		return err
	}

	if st.PrivateKey.IsZero() {
		d.log.Printf("no key found at %s... generating a new identity for this machine", d.cfg.StatePath)

		kp := wg.GenerateKeyPair()

		st.PrivateKey = kp.PrivateKey
		st.PublicKey = kp.PublicKey

		if err := SaveState(d.cfg.StatePath, st); err != nil {
			return fmt.Errorf("could not save the new key: %w", err)
		}

		d.log.Printf("generated key pair; this machine's PUBLIC key is: %s", kp.PublicKey)
	} else {
		prv := st.PrivateKey
		pub := prv.PublicKey()

		if st.PublicKey != pub {
			st.PublicKey = pub
			if err := SaveState(d.cfg.StatePath, st); err != nil {
				d.log.Printf("WARNING: could not refresh the stored public key: %v", err)
			}
		}
	}

	d.state = st
	return nil
}

func (d *Daemon) runStandalone(ctx context.Context) error {
	d.log.Printf("coordination server disabled, using %d configured peer(s)", len(d.cfg.StaticPeers))

	want := wg.Config{Interface: d.iface(d.cfg.Address)}
	for _, p := range d.cfg.StaticPeers {
		peer, err := wg.ParsePeer(p.Name, p.PublicKey, p.AllowedIPs, p.Endpoint, d.cfg.PersistentKeepalive)
		if err != nil {
			return fmt.Errorf("static peer %q: %w", p.Name, err)
		}
		want.Peers = append(want.Peers, peer)
	}

	if err := d.applyConfig(want); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return nil
	case <-d.tun.Stopped():
		return fmt.Errorf("%s was shut down from outside meshd", wg.TunnelName)
	}
}

func (d *Daemon) runCoordinated(ctx context.Context, refresh <-chan struct{}) error {
	d.log.Printf("coordination server: %s", d.cfg.ServerURL)
	d.log.Printf("this machine: %q, public key %s", d.cfg.MachineName(), d.state.PublicKey)

	interval := time.Duration(protocol.DefaultSyncIntervalSeconds) * time.Second

	for {
		newInterval, err := d.cycle(ctx)

		switch {
		case err == nil:
			d.backoff = minBackoff
			interval = newInterval

		case ctx.Err() != nil:
			return nil

		default:
			d.log.Printf("sync failed (will retry in %s): %v", d.backoff.Round(time.Second), err)

			if !sleepOrCancel(ctx, d.nextBackoff(), refresh, d.tunnelStopped()) {
				return nil
			}
			continue
		}

		if !sleepOrCancel(ctx, interval, refresh, d.tunnelStopped()) {
			return nil
		}
	}
}

// cycle performs one register-if-needed, sync, and write, and returns the interval the server wants next.
func (d *Daemon) cycle(ctx context.Context) (time.Duration, error) {
	d.dropDeadTunnel()
	if !d.state.VPNIP.IsValid() {
		if err := d.register(ctx); err != nil {
			return 0, err
		}
	}

	resp, err := d.http.Sync(ctx, protocol.SyncRequest{
		PublicKey:  d.state.PublicKey.String(),
		ListenPort: d.cfg.ListenPort,
	})

	if errors.Is(err, protocol.ErrUnknownMachine) {
		d.log.Printf("the coordination server does not know this machine any more — registering again")

		d.state.VPNIP = netip.Addr{}
		d.state.VPNCIDR = netip.Prefix{}

		if err := d.register(ctx); err != nil {
			return 0, err
		}

		resp, err = d.http.Sync(ctx, protocol.SyncRequest{
			PublicKey:  d.state.PublicKey.String(),
			ListenPort: d.cfg.ListenPort,
		})
	}

	if err != nil {
		return 0, err
	}

	if resp.Self.VPNIP != "" && resp.Self.VPNIP != d.state.VPNIP.String() {
		d.log.Printf("the server says our VPN address is %s (we had %q) — accepting the server's answer",
			resp.Self.VPNIP, d.state.VPNIP)
		d.state.VPNIP, _ = netip.ParseAddr(resp.Self.VPNIP)
		if err := SaveState(d.cfg.StatePath, d.state); err != nil {
			d.log.Printf("warning: could not save the corrected address: %v", err)
		}
	}

	if err := d.applyConfig(d.configFromServer(resp.Peers)); err != nil {
		return 0, err
	}

	return time.Duration(resp.SyncIntervalSeconds) * time.Second, nil
}

func (d *Daemon) register(ctx context.Context) error {
	d.log.Printf("registering with %s", d.cfg.ServerURL)

	resp, err := d.http.Register(ctx, protocol.RegisterRequest{
		PublicKey:  d.state.PublicKey.String(),
		Name:       d.cfg.MachineName(),
		ListenPort: d.cfg.ListenPort,
	})
	if err != nil {
		return err
	}

	d.state.VPNIP, _ = netip.ParseAddr(resp.VPNIP)
	d.state.VPNCIDR, _ = netip.ParsePrefix(resp.VPNCIDR)

	if err := SaveState(d.cfg.StatePath, d.state); err != nil {
		return fmt.Errorf("registered, but could not save the assigned address: %w", err)
	}

	d.log.Printf("registered: our VPN address is %s in %s", resp.VPNIP, resp.VPNCIDR)

	if resp.Endpoint != "" {
		d.log.Printf("the server sees us at %s", resp.Endpoint)
	}

	return nil
}

// iface is this machine's side of the tunnel, with the given address.
func (d *Daemon) iface(addr netip.Prefix) wg.Interface {
	return wg.Interface{
		PrivateKey: d.state.PrivateKey,
		Address:    addr,
		ListenPort: d.cfg.ListenPort,
		MTU:        d.cfg.MTU,
	}
}

// configFromServer turns the server's peer list into the desired tunnel config.
func (d *Daemon) configFromServer(peers []protocol.Peer) wg.Config {
	want := wg.Config{Interface: d.iface(addressWithPrefix(d.state.VPNIP, d.state.VPNCIDR))}

	for _, p := range peers {
		if p.VPNIP == "" {
			d.log.Printf("skipping peer %q: the server gave it no VPN address", p.Name)
			continue
		}

		peer, err := wg.ParsePeer(p.Name, p.PublicKey, p.VPNIP+"/32", p.Endpoint, d.cfg.PersistentKeepalive)
		if err != nil {
			d.log.Printf("skipping peer %q: %v", p.Name, err)
			continue
		}
		want.Peers = append(want.Peers, peer)
	}

	return want
}

// applyConfig creates the tunnel if it does not exist yet, then makes it match want.
func (d *Daemon) applyConfig(want wg.Config) error {
	if d.tun == nil {
		t, err := wg.OpenTunnel(want.Interface.Address, want.Interface.MTU, d.log.Printf)
		if err != nil {
			return err
		}
		d.tun = t
		d.log.Printf("brought %s up", wg.TunnelName)
	}

	ch, err := d.tun.Apply(want)
	if err != nil {
		return err
	}

	//if ch != (wg.Changes{}) {
	d.log.Printf("synced %s: %d added, %d updated, %d removed",
		wg.TunnelName, ch.Added, ch.Updated, ch.Removed)
	//}
	return nil
}

// closeTunnel removes the tunnel, if there is one.
func (d *Daemon) closeTunnel() {
	if d.tun == nil {
		return
	}
	d.tun.Close()
	d.tun = nil
	d.log.Printf("removed %s", wg.TunnelName)
}

// tunnelStopped fires when the tunnel stops. With no tunnel it returns nil, which never fires.
func (d *Daemon) tunnelStopped() <-chan struct{} {
	if d.tun == nil {
		return nil
	}
	return d.tun.Stopped()
}

// dropDeadTunnel forgets a tunnel that was shut down from outside meshd
func (d *Daemon) dropDeadTunnel() {
	select {
	case <-d.tunnelStopped():
		d.log.Printf("%s was shut down from outside meshd; recreating it", wg.TunnelName)
		d.closeTunnel()
	default:
	}
}

func sleepOrCancel(ctx context.Context, d time.Duration, refresh <-chan struct{}, tunnelStopped <-chan struct{}) bool {
	if d <= 0 {
		d = time.Second
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-refresh:
		return true
	case <-tunnelStopped:
		return true
	case <-timer.C:
		return true
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

// addressWithPrefix returns a netip.Prefix that combines the given IP address with the given CIDR prefix length.
// If the CIDR is invalid, it returns a single-host prefix (/32 for IPv4, /128 for IPv6).
func addressWithPrefix(ip netip.Addr, cidr netip.Prefix) netip.Prefix {
	if !cidr.IsValid() {
		return netip.PrefixFrom(ip, ip.BitLen()) // single host: /32 for IPv4
	}
	return netip.PrefixFrom(ip, cidr.Bits())
}
