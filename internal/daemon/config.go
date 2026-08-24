package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	"knoten/internal/protocol"
	"knoten/internal/wg"
)

const DefaultConfigPath = "/etc/knoten/meshd.json"

const DefaultStatePath = "/var/lib/knoten/meshd-state.json"

const DefaultWireGuardDir = "/etc/wireguard"

func defaultConfigPathFor(tunnelName string) string {
	return path.Join(DefaultWireGuardDir, tunnelName+".conf")
}

type StaticPeer struct {
	Name string `json:"name,omitempty"`
	PublicKey string `json:"public_key"`
	AllowedIPs string `json:"allowed_ips"`
	Endpoint string `json:"endpoint,omitempty"`
}

type Config struct {
	UseCoordinationServer bool `json:"use_coordination_server"`
	ServerURL string `json:"server_url,omitempty"`
	JoinToken string `json:"join_token,omitempty"`
	TokenFile string `json:"token_file,omitempty"`
	Name string `json:"name,omitempty"`
	TunnelName string `json:"tunnel_name"`
	ListenPort int `json:"listen_port"`
	ConfigPath string `json:"config_path"`
	StatePath string `json:"state_path"`
	Address string `json:"address,omitempty"`
	PersistentKeepalive int `json:"persistent_keepalive"`
	DNS string `json:"dns,omitempty"`
	MTU string `json:"mtu,omitempty"`
	StaticPeers []StaticPeer `json:"static_peers,omitempty"`
}

func DefaultConfig() Config {
	return Config{
		UseCoordinationServer: false,
		ServerURL:             "http://127.0.0.1:8080",
		TunnelName:            "wg0",
		ListenPort:            wg.DefaultListenPort,
		ConfigPath:            defaultConfigPathFor("wg0"),
		StatePath:             DefaultStatePath,
		PersistentKeepalive:   wg.DefaultKeepaliveSeconds,
	}
}

func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, fmt.Errorf("no config file at %s (run `meshd -setup` to create one)", path)
		}
		return Config{}, fmt.Errorf("could not read %s: %w", path, err)
	}

	cfg := DefaultConfig()

	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}

	return cfg, nil
}

func marshalConfig(cfg Config) ([]byte, error) {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("could not encode the config: %w", err)
	}

	return append(data, '\n'), nil
}

func SaveConfig(path string, cfg Config) error {
	data, err := marshalConfig(cfg)
	if err != nil {
		return err
	}

	return wg.WriteAtomic(path, string(data), 0o600)
}

func (c *Config) Validate() error {
	c.ServerURL = strings.TrimSpace(c.ServerURL)
	c.Name = c.MachineName()
	c.TunnelName = strings.TrimSpace(c.TunnelName)
	c.Address = strings.TrimSpace(c.Address)
	c.JoinToken = strings.TrimSpace(c.JoinToken)

	if c.TunnelName == "" {
		c.TunnelName = "wg0"
	}
	if c.ListenPort == 0 {
		c.ListenPort = wg.DefaultListenPort
	}
	if c.ConfigPath == "" {
		c.ConfigPath = defaultConfigPathFor(c.TunnelName)
	}
	if c.StatePath == "" {
		c.StatePath = DefaultStatePath
	}

	if len(c.TunnelName) > 15 {
		return fmt.Errorf("tunnel_name %q is %d characters; Linux allows at most 15", c.TunnelName, len(c.TunnelName))
	}
	if strings.ContainsAny(c.TunnelName, "/\\ ") {
		return fmt.Errorf("tunnel_name %q must not contain slashes or spaces", c.TunnelName)
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
		if c.Address == "" {
			return errors.New("use_coordination_server is false, so address must be set (e.g. \"10.0.0.1/24\")")
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

func (c *Config) ResolveToken() (string, error) {
	if c.TokenFile == "" {
		return c.JoinToken, nil
	}

	data, err := os.ReadFile(c.TokenFile)
	if err != nil {
		return "", fmt.Errorf("could not read token_file %s: %w", c.TokenFile, err)
	}

	return strings.TrimSpace(string(data)), nil
}

func (c *Config) MachineName() string {
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

type State struct {
	PrivateKey string `json:"private_key"`
	PublicKey string `json:"public_key"`
	VPNIP string `json:"vpn_ip,omitempty"`
	VPNCIDR string `json:"vpn_cidr,omitempty"`
}

func LoadState(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return State{}, nil
		}
		return State{}, fmt.Errorf("could not read the state file %s: %w", path, err)
	}

	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, fmt.Errorf("the state file %s is corrupted (%w); if you delete it this machine will get a NEW identity and a NEW VPN address", path, err)
	}

	return st, nil
}

func SaveState(path string, st State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("could not encode the state: %w", err)
	}
	data = append(data, '\n')

	return wg.WriteAtomic(path, string(data), 0o600)
}