package wg

// link_linux.go creates the tunnel on Linux: a TUN interface driven by wireguard-go inside this process,
// plus the control socket /var/run/wireguard/knoten-wg.sock, which is how wg(8) and other
// tools find and manage the interface.

import (
	"fmt"
	"net"
	"net/netip"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/ipc"
	"golang.zx2c4.com/wireguard/tun"

	"github.com/vishvananda/netlink"
)

const TunnelName = "knoten-wg"

// Tunnel is the running WireGuard device. It exists for as long as this process holds it.
type Tunnel struct {
	dev     *device.Device
	uapi    net.Listener
	applied Config // what Knoten last sent to the device
}

// AssignInterfaceIP assigns an IP address to the specified interface using netlink. It returns an error if the address could not be assigned.
func AssignInterfaceIP(In netlink.Link, Addr netip.Prefix) error {
	err := netlink.AddrAdd(In,
		&netlink.Addr{IPNet: &net.IPNet{
			IP:   Addr.Addr().AsSlice(),
			Mask: net.CIDRMask(Addr.Bits(), Addr.Addr().BitLen()),
		}})

	return err
}

// UpInterface brings the specified interface up using netlink. It returns an error if the interface could not be brought up.
func UpInterface(In netlink.Link) error {
	err := netlink.LinkSetUp(In)

	return err
}

// OpenTunnel creates the interface, starts WireGuard on it, opens the control socket,
// then gives the interface addr and brings it up. logf receives WireGuard's error messages.
func OpenTunnel(addr netip.Prefix, mtu int, logf func(format string, args ...any)) (_ *Tunnel, err error) {
	if !addr.IsValid() {
		return nil, fmt.Errorf("no valid address for %s", TunnelName)
	}
	if mtu == 0 {
		mtu = device.DefaultMTU // CreateTUN always sets the MTU, and rejects 0
	}

	tdev, err := tun.CreateTUN(TunnelName, mtu)
	if err != nil {
		return nil, fmt.Errorf("could not create %s: %w", TunnelName, err)
	}

	logger := &device.Logger{
		Verbosef: device.DiscardLogf,
		Errorf: func(format string, args ...any) {
			logf("wireguard: "+format, args...)
		},
	}

	t := &Tunnel{dev: device.NewDevice(tdev, conn.NewDefaultBind(), logger)}
	defer func() {
		if err != nil {
			t.Close()
		}
	}()

	//ipc.UAPIOpen creates the file /var/run/wireguard/knoten-wg.sock
	sock, err := ipc.UAPIOpen(TunnelName)
	if err != nil {
		return nil, fmt.Errorf("could not open the control socket (is another meshd running?): %w", err)
	}
	defer sock.Close()

	if t.uapi, err = ipc.UAPIListen(TunnelName, sock); err != nil {
		return nil, fmt.Errorf("could not listen on the control socket: %w", err)
	}
	go t.serveControl()

	link, err := netlink.LinkByName(TunnelName)
	if err != nil {
		return nil, fmt.Errorf("could not find %s: %w", TunnelName, err)
	}
	if err := AssignInterfaceIP(link, addr); err != nil {
		return nil, fmt.Errorf("could not give %s its address: %w", TunnelName, err)
	}

	if err := UpInterface(link); err != nil {
		return nil, fmt.Errorf("could not bring %s up: %w", TunnelName, err)
	}

	return t, nil
}

// serveControl answers requests on the control socket.
// If the socket is deleted, the device shuts down, as the wireguard-go program does.
func (t *Tunnel) serveControl() {
	for {
		c, err := t.uapi.Accept()
		if err != nil {
			t.dev.Close()
			return
		}
		go t.dev.IpcHandle(c)
	}
}

// Done is closed when the device stops,
// including when another tool deletes the interface or its control socket.
func (t *Tunnel) Stopped() <-chan struct{} {
	return t.dev.Wait()
}

// Close removes the control socket and the interface.
func (t *Tunnel) Close() {
	if t.uapi != nil {
		t.uapi.Close() // also deletes the .sock file
	}
	t.dev.Close() // the kernel deletes the interface once its TUN is closed
}
