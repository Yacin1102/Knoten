package wg

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const DefaultListenPort = 51820

const DefaultKeepaliveSeconds = 25

type Interface struct {
	PrivateKey string
	Address string
	ListenPort string
	DNS string
	MTU string
}

type Peer struct {
	Name string
	PublicKey string
	AllowedIPs string
	Endpoint string
	PersistentKeepalive int
}

func Render(iface Interface, peers []Peer) string {
	
	var b strings.Builder

	// [Interface] section
	b.WriteString("[Interface]\n")

	writeKV(&b, "PrivateKey", iface.PrivateKey)
	writeKV(&b, "Address", iface.Address)
	writeKV(&b, "ListenPort", iface.ListenPort)
	writeKV(&b, "DNS", iface.DNS)
	writeKV(&b, "MTU", iface.MTU)

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

func ConfigPath(dir, tunnelName string) string {
	return filepath.Join(dir, tunnelName+".conf")
}