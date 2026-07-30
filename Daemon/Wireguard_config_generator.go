package main

import (
	"bufio"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

// ASK: ask a question and return the answer, trimming whitespace.
func ask(reader *bufio.Reader, question string) string {
	fmt.Print(question)
	answer, _ := reader.ReadString('\n')
	return strings.TrimSpace(answer)
}

// main: The entry point: when the program starts, Go runs this function.
func main() {
	// Key generation:
	privKey, err := ecdh.X25519().GenerateKey(rand.Reader)

	if err != nil {
		fmt.Println("could not generate keys:", err)
		os.Exit(1)
	}

	// Base64 encoding and PublicKey extraction:
	privateKey := base64.StdEncoding.EncodeToString(privKey.Bytes())
	publicKey := base64.StdEncoding.EncodeToString(privKey.PublicKey().Bytes())

	// User feedback:
	fmt.Println("Generated this node's key pair.")
	fmt.Println("PublicKey (share this with the peer):", publicKey)

	// Printing a blank line for visual separation:
	fmt.Println()

	// Create a buffered reader:
	reader := bufio.NewReader(os.Stdin)

	// Tunnel name (default for wg0):
	tunnelName := ask(reader, "Tunnel name (e.g. wg0) (leave empty for wg0): ")
	if tunnelName == "" {
		tunnelName = "wg0"
	}

	// config file path (defaults to /etc/wireguard/<tunnelName>.conf):
	outputPath := ask(reader, "Where to save the config file (e.g., /home/user) (leave empty to save under /etc/wireguard): ")
	if outputPath == "" {
		outputPath = fmt.Sprintf("/etc/wireguard/%s.conf", tunnelName)
	} else {
		outputPath = outputPath + "/" + tunnelName + ".conf"
	}

	// Print a blank line for visual separation:
	fmt.Println()

	// config file generation:
	// [Interface] section:
	address := ask(reader, "This node's Address (e.g. 10.0.0.1/24): ")
	listenPort := ask(reader, "ListenPort (default 51820): ")
	if listenPort == "" {
		listenPort = "51820"
	}

	// Append the Interface configuration to the config string:
	config := fmt.Sprintf("[Interface]\nPrivateKey = %s\nAddress = %s\nListenPort = %s\n",
		privateKey, address, listenPort)

	// [Peer] section(s):
	for {
		pub := ask(reader, "Peer PublicKey (leave empty to finish): ")
		if pub == "" {
			break
		}
		ips := ask(reader, "  AllowedIPs (e.g. 10.0.0.2/32): ")
		end := ask(reader, "  Endpoint (e.g. 192.168.56.102:51820): ")

		// Append the peer configuration to the config string:
		config += fmt.Sprintf("\n[Peer]\nPublicKey = %s\nAllowedIPs = %s\nEndpoint = %s\n",
			pub, ips, end)
	}

	// Write the config to the specified path with permissions 0600 (read/write for owner only):
	err = os.WriteFile(outputPath, []byte(config), 0600)

	// User feedback:
	// Error feedback:
	if err != nil {
		fmt.Println("could not write config:", err)
		// Stop the program and signal failure to the shell (0 = success, 1 = failure):
		os.Exit(1)
	}

	// Success feedback:
	fmt.Printf("Config written to %s\n", outputPath)
}
