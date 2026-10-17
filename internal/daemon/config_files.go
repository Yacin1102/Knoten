package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"path/filepath"
	"net/netip"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"knoten/internal/protocol"
	"knoten/internal/wg"
)

const DefaultKnotenDir = "/etc/knoten"

const DefaultConfigPath = DefaultKnotenDir + "/meshd.json"

const DefaultTokenPath = DefaultKnotenDir + "/token"

const DefaultStatePath = "/var/lib/knoten/meshd-State_file.json"

const TunnelName = "knoten-wg"

const WireGuardConfigPath = "/etc/wireguard/" + TunnelName + ".conf"

type StaticPeer struct {
	Name string `json:"name,omitempty"`
	PublicKey string `json:"public_key"`
	AllowedIPs string `json:"allowed_ips"`
	Endpoint string `json:"endpoint,omitempty"`
}

type Config_file struct {
	UseCoordinationServer bool `json:"use_coordination_server"`
	ServerURL string `json:"server_url,omitempty"`
	JoinToken string `json:"join_token,omitempty"`
	TokenFile string `json:"token_file,omitempty"`
	Name string `json:"name,omitempty"`
	ListenPort int `json:"listen_port"`
	StatePath string `json:"state_path"`
	Address netip.Prefix `json:"address,omitempty"`
	PersistentKeepalive int `json:"persistent_keepalive"`
	MTU int `json:"mtu,omitempty"`
	StaticPeers []StaticPeer `json:"static_peers,omitempty"`
}

type State_file struct {
	PrivateKey wgtypes.Key `json:"private_key"`
	PublicKey wgtypes.Key `json:"public_key"`
	VPNIP netip.Addr `json:"vpn_ip,omitempty"`
	VPNCIDR netip.Prefix `json:"vpn_cidr,omitempty"`
}

func DefaultConfig() Config_file {
	return Config_file{
		UseCoordinationServer: false,
		ListenPort:          wg.DefaultListenPort,
		StatePath:           DefaultStatePath,
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

	cfg := DefaultConfig()

	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config_file{}, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}

	if err := cfg.Validate(); err != nil {
		return Config_file{}, fmt.Errorf("%s: %w", path, err)
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
	c.Name = c.MachineName()
	c.JoinToken = strings.TrimSpace(c.JoinToken)

	if c.ListenPort == 0 {
		c.ListenPort = wg.DefaultListenPort
	}
	if c.StatePath == "" {
		c.StatePath = DefaultStatePath
	}

	if err := protocol.ValidateListenPort(c.ListenPort); err != nil {
		return fmt.Errorf("listen_port: %w", err)
	}
	if err := protocol.ValidateName(c.Name); err != nil {
		return fmt.Errorf("name: %w", err)
	}

	if c.PersistentKeepalive < 0 || c.PersistentKeepalive > 65535 {
		return fmt.Errorf("persistent_keepalive %d is out of range 0-65535", c.PersistentKeepalive)
	}

	if c.UseCoordinationServer {
		if c.ServerURL == "" {
			return errors.New("use_coordination_server is true but server_url is empty")
		}
	} else {
		if c.Address.IsValid() {
			return errors.New("use_coordination_server is false, so a valid address must be set (e.g. \"10.0.0.1/24\")")
		}
		for i, p := range c.StaticPeers {
			if strings.TrimSpace(p.PublicKey) == "" {
				return fmt.Errorf("static_peers[%d] has no public_key", i)
			}
			if err := protocol.ValidatePublicKey(p.PublicKey); err != nil {
				return fmt.Errorf("static_peers[%d].public_key: %w", i, err)
			}
			if strings.TrimSpace(p.AllowedIPs) == "" {
				return fmt.Errorf("static_peers[%d] (%s) has no allowed_ips", i, p.Name)
			}
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

func LoadState(path string) (State_file, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return State_file{}, nil
		}
		return State_file{}, fmt.Errorf("could not read the state file %s: %w", path, err)
	}

	var st State_file
	if err := json.Unmarshal(data, &st); err != nil {
		return State_file{}, fmt.Errorf("the state file %s is corrupted (%w); if you delete it this machine will get a NEW identity and a NEW VPN address", path, err)
	}

	return st, nil
}

func SaveState(path string, st State_file) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("could not encode the state: %w", err)
	}
	data = append(data, '\n')

	return WriteAtomic(path, string(data), 0o600)
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