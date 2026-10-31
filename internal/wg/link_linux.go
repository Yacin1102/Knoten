package wg

// link_linux.go is the Linux side of bringing the tunnel up and keeping it in
// sync. It currently shells out to wg and wg-quick; it is meant to move to
// netlink (link and address setup).

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"net"
	"errors"

	"github.com/vishvananda/netlink"
)

const TunnelName = "knoten-wg"

// MakeInterface creates a WireGuard interface with the specified name using netlink. It returns an error if the interface could not be created.
func MakeInterface() (netlink.Link, error) {
	la := netlink.NewLinkAttrs()
	la.Name = TunnelName
	link := &netlink.Wireguard{LinkAttrs: la}
	err := netlink.LinkAdd(link)

	return link, err
}

// DeleteInterface deletes the specified interface using netlink. It returns an error if the interface could not be deleted.
func DeleteInterface(In netlink.Link) error {
	err := netlink.LinkDel(In)

	return err
}

// AssignInterfaceIP assigns an IP address to the specified interface using netlink. It returns an error if the address could not be assigned.
func AssignInterfaceIP(In netlink.Link, Addr netip.Prefix) error{
	err := netlink.AddrAdd(In, 
		&netlink.Addr{IPNet: &net.IPNet{
			IP: Addr.Addr().AsSlice() ,
			Mask: net.CIDRMask(Addr.Bits(), Addr.Addr().BitLen()),
	}})

	return err
}

// UpInterface brings the specified interface up using netlink. It returns an error if the interface could not be brought up.
func UpInterface(In netlink.Link) error {
	err := netlink.LinkSetUp(In)

	return err
}

// SetInterfaceMTU sets the MTU for the specified interface using netlink. It returns an error if the MTU could not be set.
func SetInterfaceMTU(In netlink.Link, mtu int) error {
	return netlink.LinkSetMTU(In, mtu)
}

// TunnelExists checks if the WireGuard tunnel interface exists using netlink. It returns a boolean indicating existence and an error if the check could not be performed.
func TunnelExists() (Exists bool, err error) {
	_, err = netlink.LinkByName(TunnelName)
	var notFound netlink.LinkNotFoundError
	if errors.As(err, &notFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("could not look up %s: %w", TunnelName, err)
	}
	return true, nil
}

// CreateTunnel creates a WireGuard tunnel interface with the specified IP address and prefix using netlink. It returns an error if the tunnel could not be created or configured.
func CreateTunnel(addr netip.Prefix, mtu int) (error) {
		link, err := MakeInterface()
		if err != nil {
			return err
		}

		if err := AssignInterfaceIP(link, addr); err != nil {
			DeleteInterface(link)
			return err
		}

		if err := UpInterface(link); err != nil {
			DeleteInterface(link)
			return err
		}

		if err := SetInterfaceMTU(link, mtu); err != nil {
			DeleteInterface(link)
			return err
		}
		return nil
}


// The code below will be removed/ changed:


// wgCommandTimeout caps a wg or wg-quick invocation. wg-quick resolves peer endpoints through DNS, so it can block for a long time.
const wgCommandTimeout = 30 * time.Second

const WireGuardConfigPath = "/etc/wireguard/" + TunnelName + ".conf"


func SyncTunnel(ctx context.Context) (err error) {
	// `wg syncconf` rejects the wg-quick-only keys (Address, DNS, MTU), so feed it the stripped form of the file we just wrote.
	stripped, err := runWG(ctx, "wg-quick", "strip", TunnelName)
	if err != nil {
		return err
	}

	tmp := WireGuardConfigPath + ".sync"
	if err := WriteAtomic(tmp, stripped, 0o600); err != nil {
		return err
	}
	defer os.Remove(tmp)

	if _, err := runWG(ctx, "wg", "syncconf", TunnelName, tmp); err != nil {
		return err
	}

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
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}

	return stdout.String(), nil
}

func WriteAtomic(path string, content string, perm os.FileMode) error {

	dir := filepath.Dir(path)

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("could not create directory %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("could not create temporary file in %s: %w", dir, err)
	}

	tmpName := tmp.Name()

	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()

	if _, err := tmp.WriteString(content); err != nil {
		return fmt.Errorf("could not write temporary file %s: %w", tmpName, err)
	}

	if err := tmp.Chmod(perm); err != nil {
		return fmt.Errorf("could not set permissions on %s: %w", tmpName, err)
	}

	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("could not flush %s to disk: %w", tmpName, err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("could not close %s: %w", tmpName, err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("could not move %s into place at %s: %w", tmpName, path, err)
	}

	return nil
}