package wg

// link_linuxo.go is the Linux side of bringing the tunnel up and keeping it in
// sync. It currently shells out to wg and wg-quick; it is meant to move to
// netlink (link and address setup).

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Runtime struct {
	log *log.Logger
}

// wgCommandTimeout caps a wg or wg-quick invocation. wg-quick resolves peer endpoints through DNS, so it can block for a long time.
const wgCommandTimeout = 30 * time.Second

const TunnelName = "knoten-wg"
const WireGuardConfigPath = "/etc/wireguard/" + TunnelName + ".conf"

func (r *Runtime) ApplyTunnel(ctx context.Context, changed bool) error {
	if _, err := runWG(ctx, "wg", "show", TunnelName); err != nil {
		if _, err := runWG(ctx, "wg-quick", "up", TunnelName); err != nil {
			return err
		}

		r.log.Printf("brought %s up", TunnelName)
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
	if err := WriteAtomic(tmp, stripped, 0o600); err != nil {
		return err
	}
	defer os.Remove(tmp)

	if _, err := runWG(ctx, "wg", "syncconf", TunnelName, tmp); err != nil {
		return err
	}

	r.log.Printf("synced %s", TunnelName)
	return nil
}

func NewRuntime(logger *log.Logger) *Runtime {
    return &Runtime{
        log: logger,
    }
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


