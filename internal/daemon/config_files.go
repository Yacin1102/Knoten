package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"knoten/internal/key"
	"knoten/internal/peerbook"
	"knoten/internal/protocol"
	"knoten/internal/wg"
)

const DefaultKnotenDir = "/etc/knoten"

const DefaultConfigPath = DefaultKnotenDir + "/meshd.json"

const DefaultTokenPath = DefaultKnotenDir + "/token"

// Config_file is meshd.json, the only file meshd keeps. the user can write the
// settings and static and blocked peers. meshd writes its own parts.
type Config_file struct {
	ServerURL           string `json:"server_url,omitempty"`
	JoinToken           string `json:"join_token,omitempty"`
	TokenFile           string `json:"token_file,omitempty"`
	Name                string `json:"name,omitempty"`
	ListenPort          int    `json:"listen_port"`
	PersistentKeepalive int    `json:"persistent_keepalive"`
	MTU                 int    `json:"mtu,omitempty"`

	// Written by meshd once, when it makes a new key.
	PrivateKey key.Key `json:"private_key,omitzero"`

	// Manually set without a server. With a server, meshd writes the one it assigned.
	Address netip.Prefix `json:"address,omitzero"`

	// "static" and "blocked" peers are manually set, "synced" peers are meshd's.
	Peers []peerbook.Peer `json:"peers,omitempty"`
}

// UsesServer reports whether a coordination server is configured.
func (c *Config_file) UsesServer() bool {
	return c.ServerURL != ""
}

func DefaultConfig() Config_file {
	return Config_file{
		ListenPort:          wg.DefaultListenPort,
		PersistentKeepalive: wg.DefaultKeepaliveSeconds,
	}
}

func LoadConfig(path string) (Config_file, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config_file{}, fmt.Errorf("no config file at %s (run `meshd -setup` to create one)", path)
		}
		return Config_file{}, fmt.Errorf("could not read %s: %w", path, err)
	}

	cfg, err := parseConfig(data)
	if err != nil {
		return Config_file{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// parseConfig decodes meshd.json and checks it. Unknown fields are an error,
// so a typo or a removed setting is reported instead of silently ignored.
func parseConfig(data []byte) (Config_file, error) {
	cfg := DefaultConfig()

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config_file{}, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return Config_file{}, errors.New("unexpected text after the closing }")
	}

	if err := cfg.Validate(); err != nil {
		return Config_file{}, err
	}
	return cfg, nil
}

func marshalConfig(cfg Config_file) ([]byte, error) {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("could not encode the config: %w", err)
	}

	return append(data, '\n'), nil
}

func SaveConfig(path string, cfg Config_file) error {
	data, err := marshalConfig(cfg)
	if err != nil {
		return err
	}

	return WriteAtomic(path, string(data), 0o600)
}

func (c *Config_file) Validate() error {
	c.ServerURL = strings.TrimSpace(c.ServerURL)
	c.JoinToken = strings.TrimSpace(c.JoinToken)

	if c.ListenPort == 0 {
		c.ListenPort = wg.DefaultListenPort
	}

	if err := protocol.ValidateListenPort(c.ListenPort); err != nil {
		return fmt.Errorf("listen_port: %w", err)
	}
	if err := protocol.ValidateName(c.MachineName()); err != nil {
		return fmt.Errorf("name: %w", err)
	}
	if c.PersistentKeepalive < 0 || c.PersistentKeepalive > 65535 {
		return fmt.Errorf("persistent_keepalive %d is out of range 0-65535", c.PersistentKeepalive)
	}

	if !c.UsesServer() && !c.Address.IsValid() {
		return errors.New("without a server_url, a valid address must be set (e.g. \"10.0.0.1/24\")")
	}

	seen := map[key.Key]int{} // public key → index of the peer that has it

	for i := range c.Peers {
		p := &c.Peers[i]

		if p.Source == "" {
			p.Source = peerbook.Static
		}
		switch p.Source {
		case peerbook.Static, peerbook.Synced, peerbook.Blocked:
		default:
			return fmt.Errorf("peers[%d] (%s): source must be %q, %q or %q, not %q",
				i, p.Name, peerbook.Static, peerbook.Synced, peerbook.Blocked, p.Source)
		}

		if p.PublicKey.IsZero() {
			return fmt.Errorf("peers[%d] (%s) has no public_key", i, p.Name)
		}
		if first, ok := seen[p.PublicKey]; ok {
			return fmt.Errorf("peers[%d] (%s): this public_key is already listed at peers[%d]; "+
				"list each peer once and change its source to pin or block it", i, p.Name, first)
		}
		seen[p.PublicKey] = i

		if p.Source == peerbook.Blocked {
			continue
		}
		if len(p.AllowedIPs) == 0 {
			return fmt.Errorf("peers[%d] (%s) has no allowed_ips", i, p.Name)
		}
		for j, ip := range p.AllowedIPs {
			if !ip.IsValid() {
				return fmt.Errorf("peers[%d] (%s): allowed_ips[%d] is not valid", i, p.Name, j)
			}
			p.AllowedIPs[j] = ip.Masked()
		}
	}

	return nil
}

func (c *Config_file) ResolveToken() (string, error) {
	if c.TokenFile == "" {
		return c.JoinToken, nil
	}

	data, err := os.ReadFile(c.TokenFile)
	if err != nil {
		return "", fmt.Errorf("could not read token_file %s: %w", c.TokenFile, err)
	}

	return strings.TrimSpace(string(data)), nil
}

func (c *Config_file) MachineName() string {
	if name := strings.TrimSpace(c.Name); name != "" {
		return name
	}

	host, err := os.Hostname()
	if err != nil {
		return "unnamed-machine"
	}
	if host = strings.TrimSpace(host); host == "" {
		return "unnamed-machine"
	}
	return host
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
