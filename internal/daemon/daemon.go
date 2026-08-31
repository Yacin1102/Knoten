package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"strings"
	"time"

	"knoten/internal/client"
	"knoten/internal/protocol"
	"knoten/internal/wg"
)

const (
	minBackoff = 1 * time.Second
	maxBackoff = 60 * time.Second
)

// wgCommandTimeout caps a wg or wg-quick invocation. wg-quick resolves peer
// endpoints through DNS, so it can block for a long time.
const wgCommandTimeout = 30 * time.Second

type Daemon struct {
	cfg   Config
	log   *log.Logger
	http  *client.Client
	state State
	lastWritten string
	backoff time.Duration
}

func New(cfg Config, logger *log.Logger) (*Daemon, error) {
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

func (d *Daemon) Run(ctx context.Context, once bool, refresh <-chan struct{}) error {
	if err := d.ensureKeyPair(); err != nil {
		return err
	}

	if !d.cfg.UseCoordinationServer {
		return d.runStandalone(ctx)
	}

	return d.runCoordinated(ctx, once, refresh)
}

func (d *Daemon) ensureKeyPair() error {
	st, err := LoadState(d.cfg.StatePath)
	if err != nil {
		return err
	}

	if st.PrivateKey == "" {
		d.log.Printf("no key found at %s — generating a new identity for this machine", d.cfg.StatePath)

		kp, err := wg.GenerateKeyPair()
		if err != nil {
			return fmt.Errorf("could not generate a key pair: %w", err)
		}

		st.PrivateKey = kp.PrivateKey
		st.PublicKey = kp.PublicKey

		if err := SaveState(d.cfg.StatePath, st); err != nil {
			return fmt.Errorf("could not save the new key: %w", err)
		}

		d.log.Printf("generated key pair; this machine's PUBLIC key is: %s", kp.PublicKey)
	} else {
		pub, err := wg.PublicKeyFrom(st.PrivateKey)
		if err != nil {
			return fmt.Errorf("the private key in %s is unusable: %w", d.cfg.StatePath, err)
		}

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
	d.log.Printf("coordination server disabled — writing a static config from %d configured peer(s)", len(d.cfg.StaticPeers))

	iface := wg.Interface{
		PrivateKey: d.state.PrivateKey,
		Address:    d.cfg.Address,
		ListenPort: fmt.Sprintf("%d", d.cfg.ListenPort),
		DNS:        d.cfg.DNS,
		MTU:        d.cfg.MTU,
	}

	peers := make([]wg.Peer, 0, len(d.cfg.StaticPeers))
	for _, p := range d.cfg.StaticPeers {
		peers = append(peers, wg.Peer{
			Name:                p.Name,
			PublicKey:           p.PublicKey,
			AllowedIPs:          p.AllowedIPs,
			Endpoint:            p.Endpoint,
			PersistentKeepalive: d.cfg.PersistentKeepalive,
		})
	}

	return d.applyConfig(ctx, wg.Render(iface, peers))
}

func (d *Daemon) runCoordinated(ctx context.Context, once bool, refresh <-chan struct{}) error {
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

			if !sleepOrCancel(ctx, d.nextBackoff(), refresh) {
				return nil
			}
			continue
		}

		if once {
			return nil
		}

		if !sleepOrCancel(ctx, interval, refresh) {
			return nil
		}
	}
}

// cycle performs one register-if-needed, sync, and write, and returns the interval the server wants next.
func (d *Daemon) cycle(ctx context.Context) (time.Duration, error) {
	if d.state.VPNIP == "" {
		if err := d.register(ctx); err != nil {
			return 0, err
		}
	}

	resp, err := d.http.Sync(ctx, protocol.SyncRequest{
		PublicKey:  d.state.PublicKey,
		ListenPort: d.cfg.ListenPort,
	})

	if errors.Is(err, protocol.ErrUnknownMachine) {
		d.log.Printf("the coordination server does not know this machine any more — registering again")

		d.state.VPNIP = ""
		d.state.VPNCIDR = ""

		if err := d.register(ctx); err != nil {
			return 0, err
		}

		resp, err = d.http.Sync(ctx, protocol.SyncRequest{
			PublicKey:  d.state.PublicKey,
			ListenPort: d.cfg.ListenPort,
		})
	}

	if err != nil {
		return 0, err
	}

	if resp.Self.VPNIP != "" && resp.Self.VPNIP != d.state.VPNIP {
		d.log.Printf("the server says our VPN address is %s (we had %q) — accepting the server's answer",
			resp.Self.VPNIP, d.state.VPNIP)
		d.state.VPNIP = resp.Self.VPNIP
		if err := SaveState(d.cfg.StatePath, d.state); err != nil {
			d.log.Printf("warning: could not save the corrected address: %v", err)
		}
	}

	if err := d.applyConfig(ctx, d.renderFromPeers(resp.Peers)); err != nil {
		return 0, err
	}

	return time.Duration(resp.SyncIntervalSeconds) * time.Second, nil
}

func (d *Daemon) register(ctx context.Context) error {
	d.log.Printf("registering with %s", d.cfg.ServerURL)

	resp, err := d.http.Register(ctx, protocol.RegisterRequest{
		PublicKey:  d.state.PublicKey,
		Name:       d.cfg.MachineName(),
		ListenPort: d.cfg.ListenPort,
	})
	if err != nil {
		return err
	}

	d.state.VPNIP = resp.VPNIP
	d.state.VPNCIDR = resp.VPNCIDR

	if err := SaveState(d.cfg.StatePath, d.state); err != nil {
		return fmt.Errorf("registered, but could not save the assigned address: %w", err)
	}

	d.log.Printf("registered: our VPN address is %s in %s", resp.VPNIP, resp.VPNCIDR)

	if resp.Endpoint != "" {
		d.log.Printf("the server sees us at %s", resp.Endpoint)
	}

	return nil
}

// renderFromPeers turns a peer list from the server into WireGuard config text.
func (d *Daemon) renderFromPeers(peers []protocol.Peer) string {
	iface := wg.Interface{
		PrivateKey: d.state.PrivateKey,
		Address:    addressWithPrefix(d.state.VPNIP, d.state.VPNCIDR),
		ListenPort: fmt.Sprintf("%d", d.cfg.ListenPort),
		DNS:        d.cfg.DNS,
		MTU:        d.cfg.MTU,
	}

	out := make([]wg.Peer, 0, len(peers))
	for _, p := range peers {
		if p.VPNIP == "" {
			d.log.Printf("skipping peer %q: the server gave it no VPN address", p.Name)
			continue
		}

		out = append(out, wg.Peer{
			Name:      p.Name,
			PublicKey: p.PublicKey,
			AllowedIPs: p.VPNIP + "/32",
			Endpoint: p.Endpoint,
			PersistentKeepalive: d.cfg.PersistentKeepalive,
		})
	}

	return wg.Render(iface, out)
}

func (d *Daemon) applyConfig(ctx context.Context, rendered string) error {
	changed := rendered != d.lastWritten

	if changed {
		if err := wg.WriteAtomic(WireGuardConfigPath, rendered, 0o600); err != nil {
			return fmt.Errorf("could not write the WireGuard config: %w", err)
		}
		d.lastWritten = rendered

		peerCount := strings.Count(rendered, "[Peer]")
		d.log.Printf("wrote %s (%d peer(s))", WireGuardConfigPath, peerCount)
	}

	if err := d.applyTunnel(ctx, changed); err != nil {
		d.log.Printf("WARNING: the config is written, but the tunnel was not updated: %v", err)
		d.log.Printf("         if the tunnel is down: sudo wg-quick up %s", TunnelName)
		d.log.Printf("         if it is already up:   sudo bash -c 'wg syncconf %s <(wg-quick strip %s)'", TunnelName, TunnelName)
	}

	return nil
}

// applyTunnel brings the tunnel up if it is down, or pushes the new peers into the
// running interface if the config changed. Syncing does not drop the interface, so
// connections through the tunnel survive a peer list change.

// Temporary: this shells out to wireguard-tools. We will later replace it with wgctrl.
func (d *Daemon) applyTunnel(ctx context.Context, changed bool) error {
	if _, err := runWG(ctx, "wg", "show", TunnelName); err != nil {
		if _, err := runWG(ctx, "wg-quick", "up", TunnelName); err != nil {
			return err
		}

		d.log.Printf("brought %s up", TunnelName)
		return nil
	}

	if !changed {
		return nil
	}

	// `wg syncconf` rejects the wg-quick-only keys (Address, DNS, MTU), so feed it the stripped form of the file we just wrote.
	stripped, err := runWG(ctx, "wg-quick", "strip", TunnelName)
	if err != nil {
		return err
	}

	tmp := WireGuardConfigPath + ".sync"
	if err := wg.WriteAtomic(tmp, stripped, 0o600); err != nil {
		return err
	}
	defer os.Remove(tmp)

	if _, err := runWG(ctx, "wg", "syncconf", TunnelName, tmp); err != nil {
		return err
	}

	d.log.Printf("synced %s", TunnelName)
	return nil
}

// runWG runs a wireguard-tools command and returns its standard output.
func runWG(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, wgCommandTimeout)
	defer cancel()

	var stdout, stderr strings.Builder

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %w: %s",name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}

	return stdout.String(), nil
}

func sleepOrCancel(ctx context.Context, d time.Duration, refresh <-chan struct{}) bool {
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

func addressWithPrefix(ip, cidr string) string {
	if ip == "" {
		return ""
	}

	_, prefix, found := strings.Cut(cidr, "/")
	if !found || prefix == "" {
		return ip + "/32"
	}

	return ip + "/" + prefix
}