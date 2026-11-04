package daemon

import (
	"bufio"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"

	"knoten/internal/key"
	"knoten/internal/peerbook"
	"knoten/internal/protocol"
	"knoten/internal/wg"
)

const ExampleServerURL = "http://coord.example.com:8443"

func ask(reader *bufio.Reader, question string) string {
	fmt.Print(question)
	answer, _ := reader.ReadString('\n')
	return strings.TrimSpace(answer)
}

func askUntilValid(reader *bufio.Reader, question string, allowEmpty bool, validate func(string) error) string {
	for {
		answer := ask(reader, question)

		if answer == "" {
			if allowEmpty {
				return ""
			}
			fmt.Println("  ! this question needs an answer")
			continue
		}

		if err := validate(answer); err != nil {
			fmt.Printf("  ! %v\n", err)
			continue
		}

		return answer
	}
}

// Setup asks for the settings and writes a new meshd.json, with a new key.
// Each question is about one setting.
func Setup(configPath string, force bool) error {
	if _, err := os.Stat(configPath); err == nil && !force {
		return fmt.Errorf(
			"%s already exists.\n"+
				"Re-running setup would give this machine a NEW identity and disconnect it from every peer.\n"+
				"If that is really what you want, run: meshd -setup -force",
			configPath)
	}

	reader := bufio.NewReader(os.Stdin)
	cfg := DefaultConfig()

	fmt.Print(banner)
	fmt.Println()
	fmt.Println("---Welcome to meshd setup!")
	fmt.Println()

	fmt.Printf("Interface %s\n", wg.TunnelName)
	fmt.Println()

	portAnswer := askUntilValid(reader, fmt.Sprintf("ListenPort (default %d): ", cfg.ListenPort), true, validateListenPort)

	if portAnswer != "" {
		cfg.ListenPort, _ = strconv.Atoi(portAnswer)
	}
	fmt.Println()

	askServer(reader, &cfg)

	if !cfg.UsesServer() {
		cfg.Address, _ = netip.ParsePrefix(askUntilValid(reader, "This machine's VPN address (e.g. 10.0.0.1/24): ", false, validateCIDRAddress))
		fmt.Println()
	}

	askPeers(reader, &cfg)

	cfg.Name = askUntilValid(reader, fmt.Sprintf("Name for this machine (leave empty for %q): ", cfg.MachineName()), true, protocol.ValidateName)
	fmt.Println()

	cfg.PrivateKey = key.GenerateKeyPair().PrivateKey

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("the answers do not make a valid configuration: %w", err)
	}
	if err := SaveConfig(configPath, cfg); err != nil {
		return err
	}

	fmt.Printf("Config written to %s\n", configPath)
	fmt.Printf("This machine's public key: %s\n", cfg.PrivateKey.PublicKey())
	fmt.Println("To change settings or peers later, edit that file; the README explains each field.")
	fmt.Println("https://github.com/Yacin1102/Knoten")
	fmt.Println()
	return nil
}

// askServer asks for an optional coordination server. With one, the server
// gives this machine its address and keeps the synced peers up to date.
func askServer(reader *bufio.Reader, cfg *Config_file) {
	fmt.Println("A coordination server hands out VPN addresses and keeps the peer list up to")
	fmt.Println("date, so you never type another machine's key by hand. Leave it empty if you")
	fmt.Println("want to list every peer yourself.")
	fmt.Println()

	cfg.ServerURL = askUntilValid(reader, fmt.Sprintf("Coordination server URL (e.g. %s, leave empty for none): ", ExampleServerURL), true, validateServerURL)

	if cfg.UsesServer() {
		cfg.JoinToken = ask(reader, "Join token (leave empty if the server does not require one): ")
	}
	fmt.Println()
}

// askPeers asks for peers to list by hand. They are tagged static, so meshd
// never changes them. Without a server they are the whole peer list; with
// one they come in addition to the server's.
func askPeers(reader *bufio.Reader, cfg *Config_file) {
	if cfg.UsesServer() {
		fmt.Println("The server will send the list of other machines. You can also add peers")
		fmt.Println("by hand (leave the public key empty to skip this step).")

	} else {
		fmt.Println("List the machines this one should connect to.")
	}
	fmt.Println()

	for {
		pub := ask(reader, "Peer PublicKey (leave empty to finish): ")
		if pub == "" {
			break
		}

		peerKey, err := key.ParseKey(pub)
		if err != nil {
			fmt.Printf("  ! %v\n", err)
			continue
		}

		ips := askUntilValid(reader, "  AllowedIPs (e.g. 10.0.0.2/32, 192.168.1.0/24): ", false, validateAllowedIPs)

		end := askUntilValid(reader, "  Endpoint (e.g. 192.168.56.102:51820) (leave empty if this peer has no fixed address): ", true, validateEndpoint)

		name := ask(reader, "  Name for this peer (optional): ")

		peer := peerbook.Peer{
			Source:     peerbook.Static,
			Name:       name,
			PublicKey:  peerKey,
			AllowedIPs: parsePrefixList(ips),
		}
		if end != "" {
			peer.Endpoint, _ = netip.ParseAddrPort(end)
		}
		cfg.Peers = append(cfg.Peers, peer)
	}
	fmt.Println()
}

// parsePrefixList reads a list like "10.0.0.2/32, 10.0.1.0/24" that
// validateAllowedIPs has already checked.
func parsePrefixList(s string) []netip.Prefix {
	var out []netip.Prefix
	for _, part := range strings.Split(s, ",") {
		p, _ := netip.ParsePrefix(strings.TrimSpace(part))
		out = append(out, p)
	}
	return out
}

func validateListenPort(s string) error {
	n, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("%q is not a number", s)
	}
	return protocol.ValidateListenPort(n)
}

func validateServerURL(s string) error {
	if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
		return fmt.Errorf("the URL must start with http:// or https://")
	}
	return nil
}

func validateEndpoint(s string) error {
	if _, err := netip.ParseAddrPort(s); err != nil {
		return fmt.Errorf("%q is not ip:port: %w", s, err)
	}
	return nil
}

func validateCIDRAddress(s string) error {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(s))
	if err != nil {
		return fmt.Errorf("%q is not a valid address/prefix: %w", s, err)
	}
	if !prefix.Addr().Is4() {
		return fmt.Errorf("%q is not a valid IPv4 address", s)
	}
	return nil
}

func validateAllowedIPs(s string) error {
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return fmt.Errorf("empty entry in the list")
		}
		if err := validateCIDRAddress(part); err != nil {
			return err
		}
	}
	return nil
}

// banner is the splash shown at the top of interactive setup.
const banner = `
  __  __           _         _            _               
 |  \/  |         | |       | |          | |              
 | \  / | ___  ___| |__   __| |  ___  ___| |_ _   _ _ __  
 | |\/| |/ _ \/ __| '_ \ / _' | / __|/ _ \ __| | | | '_ \ 
 | |  | |  __/\__ \ | | | (_| | \__ \  __/ |_| |_| | |_) |
 |_|  |_|\___||___/_| |_|\__,_| |___/\___|\__|\__,_| .__/ 
                                                   | |    
                                                   |_|  
`
