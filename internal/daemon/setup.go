package daemon

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"knoten/internal/protocol"
)

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

func askYesNo(reader *bufio.Reader, question string, defaultYes bool) bool {
	suffix := " (y/N): "
	if defaultYes {
		suffix = " (Y/n): "
	}

	for {
		switch strings.ToLower(ask(reader, question+suffix)) {
		case "":
			return defaultYes
		case "y", "yes":
			return true
		case "n", "no":
			return false
		default:
			fmt.Println("  ! please answer y or n")
		}
	}
}

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


	fmt.Printf("Interface %s, config at %s\n", TunnelName, WireGuardConfigPath)

	fmt.Println()

	portAnswer := askUntilValid(reader, fmt.Sprintf("ListenPort (default %d): ", cfg.ListenPort), true, func(s string) error {
		n, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("%q is not a number", s)
		}
		return protocol.ValidateListenPort(n)
	})
	if portAnswer != "" {
		cfg.ListenPort, _ = strconv.Atoi(portAnswer)
	}

	fmt.Println()

	fmt.Println("A coordination server distributes the peer list automatically, so you never")
	fmt.Println("have to type another machine's public key by hand. Say no if every machine")
	fmt.Println("already has a public, static IP address and you want to list peers yourself.")
	fmt.Println()

	cfg.UseCoordinationServer = askYesNo(reader, "Use a coordination server?", false)
	fmt.Println()

	if cfg.UseCoordinationServer {
		if err := setupCoordinated(reader, &cfg); err != nil {
			return err
		}
	} else {
		if err := setupStandalone(reader, &cfg); err != nil {
			return err
		}
	}

	cfg.Name = askUntilValid(reader,
		fmt.Sprintf("Name for this machine (leave empty for %q): ", cfg.MachineName()), true,
		protocol.ValidateName)

	fmt.Println()

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("the answers do not make a valid configuration: %w", err)
	}

	if err := SaveConfig(configPath, cfg); err != nil {
		return err
	}
	fmt.Printf("Config written to %s\n", configPath)

	fmt.Println()
	return nil
}

const ExampleServerURL = "https://coord.example.com:8443"

func setupCoordinated(reader *bufio.Reader, cfg *Config) error {
	cfg.ServerURL = askUntilValid(reader,
		fmt.Sprintf("Coordination server URL (e.g. %s): ", ExampleServerURL), false,
		func(s string) error {
			if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
				return fmt.Errorf("the URL must start with http:// or https://")
			}
			return nil
		})

	cfg.JoinToken = ask(reader, "Join token (leave empty if the server does not require one): ")

	fmt.Println()
	fmt.Println("The coordination server will assign this machine's VPN address and keep the")
	fmt.Println("peer list up to date. No peers need to be entered by hand.")
	fmt.Println()

	return nil
}

func setupStandalone(reader *bufio.Reader, cfg *Config) error {
	cfg.Address = askUntilValid(reader, "This node's Address (e.g. 10.0.0.1/24): ", false, validateCIDRAddress)

	fmt.Println()

	for {
		pub := ask(reader, "Peer PublicKey (leave empty to finish): ")
		if pub == "" {
			break
		}

		if err := protocol.ValidatePublicKey(pub); err != nil {
			fmt.Printf("  ! %v\n", err)
			continue
		}

		ips := askUntilValid(reader, "  AllowedIPs (e.g. 10.0.0.2/32): ", false, validateAllowedIPs)

		end := ask(reader, "  Endpoint (e.g. 192.168.56.102:51820) (leave empty if this peer has no fixed address): ")

		name := ask(reader, "  Name for this peer (optional): ")

		cfg.StaticPeers = append(cfg.StaticPeers, StaticPeer{
			Name:       name,
			PublicKey:  pub,
			AllowedIPs: ips,
			Endpoint:   end,
		})
	}

	fmt.Println()
	return nil
}

func validateCIDRAddress(s string) error {
	ip, prefix, found := strings.Cut(s, "/")
	if !found {
		return fmt.Errorf("an address needs a prefix length, e.g. %s/24", s)
	}
	if err := validateIPv4(ip); err != nil {
		return err
	}
	n, err := strconv.Atoi(prefix)
	if err != nil || n < 0 || n > 32 {
		return fmt.Errorf("%q is not a valid prefix length (0-32)", prefix)
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

func validateIPv4(s string) error {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return fmt.Errorf("%q should have four parts separated by dots", s)
	}
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return fmt.Errorf("%q in %q is not a number", p, s)
		}
		if n < 0 || n > 255 {
			return fmt.Errorf("%d in %q is not between 0 and 255", n, s)
		}
	}
	return nil
}


func PrintDefaultConfig() error {

	cfg := DefaultConfig()
	cfg.UseCoordinationServer = false
	cfg.ServerURL = ExampleServerURL
	cfg.TokenFile = DefaultTokenPath

	fmt.Fprintln(os.Stderr, "# meshd configuration template. Edit and save as "+DefaultConfigPath)
	fmt.Fprintln(os.Stderr, "#")
	fmt.Fprintln(os.Stderr, "#   use_coordination_server  true  = poll a coordination server for peers")
	fmt.Fprintln(os.Stderr, "#                            false = use the static_peers list below")
	fmt.Fprintln(os.Stderr, "#   server_url               the coordination server, including http:// or https://")
	fmt.Fprintln(os.Stderr, "#   token_file               a 0600 file holding the shared join token")
	fmt.Fprintln(os.Stderr, "#                            (preferred over putting the token in this file)")
	fmt.Fprintln(os.Stderr, "#   address                  REQUIRED only when use_coordination_server is false;")
	fmt.Fprintln(os.Stderr, "#                            otherwise the server assigns it")
	fmt.Fprintln(os.Stderr, "#   persistent_keepalive     25 keeps NAT mappings open; 0 disables the line")
	fmt.Fprintln(os.Stderr, "")

	data, err := marshalConfig(cfg)
	if err != nil {
		return err
	}
	fmt.Print(string(data))
	return nil
}

// banner is the splash shown at the top of interactive setup.
const banner = `
      ___           ___           ___                       ___           ___     
     /__/|         /__/\         /  /\          ___        /  /\         /__/\    
    |  |:|         \  \:\       /  /::\        /  /\      /  /:/_        \  \:\   
    |  |:|          \  \:\     /  /:/\:\      /  /:/     /  /:/ /\        \  \:\  
  __|  |:|      _____\__\:\   /  /:/  \:\    /  /:/     /  /:/ /:/_   _____\__\:\ 
 /__/\_|:|____ /__/::::::::\ /__/:/ \__\:\  /  /::\    /__/:/ /:/ /\ /__/::::::::\
 \  \:\/:::::/ \  \:\~~\~~\/ \  \:\ /  /:/ /__/:/\:\   \  \:\/:/ /:/ \  \:\~~\~~\/
  \  \::/~~~~   \  \:\  ~~~   \  \:\  /:/  \__\/  \:\   \  \::/ /:/   \  \:\  ~~~ 
   \  \:\        \  \:\        \  \:\/:/        \  \:\   \  \:\/:/     \  \:\     
    \  \:\        \  \:\        \  \::/          \__\/    \  \::/       \  \:\    
     \__\/         \__\/         \__\/                     \__\/         \__\/    
                                                                                                                                                  
`
