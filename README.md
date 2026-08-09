# Wireguard_config_generator

> Knoten is actively being built. Nothing is ready yet. This version v0.1.0 is just a simple WireGuard config file generator, *a first step in my learning journey to build the main project*.

> **Although this program is a transitional step, it will be maintained separately as a standalone tool. We will continue to address its limitations and fix bugs to keep it a simple usable tool.**

# Code documentation

**A single-file Go CLI that generates an X25519 key pair and writes a ready-to-use WireGuard `.conf` file.**

| Property         | Details                                     |
| ---------------- | ------------------------------------------- |
| **Source**       | `Wireguard_config_generator.go`             |
| **Language**     | Go (requires **1.20+**, uses `crypto/ecdh`) |
| **Dependencies** | None (standard library only)                |
| **Audience**     | Anyone setting up a WireGuard node by hand  |
| **Status**       | Working prototype                           |
---

## 1. Program flow

1. It first generates a fresh private/public key pair and prints the **public** key on screen. 
2. It asks you questions in the terminal, assembles everything into a WireGuard config, writes it to `/etc/wireguard/<tunnelName>.conf` with the right permissions, and reports success or failure.

---

## 2. Requirements

- **Go 1.20 or newer** to build. (`crypto/ecdh` did not exist before 1.20)
- **Write permission on the output directory.** The default is `/etc/wireguard/`, which normally means running as root.
- The **`wireguard-tools`** package (`wg`, `wg-quick`) if you actually want to bring the tunnel up. This program only writes the file.
- The output directory **must already exist**. The program does not create it.

---

## 3. Quick start

If you’re on a Windows machine you can cross-compile the program for Linux:

```powershell
$env:GOOS='linux'; $env:GOARCH='amd64'; go build -o Wireguard_config_generator <Path_to_program_file>
```

Otherwise, if you’re already on Linux:

```bash
go build -o Wireguard_config_generator Wireguard_config_generator.go
```

To run the program:

```bash
# Give the program execution permissions if it was not compiled on the same machine: 
chmod +x Wireguard_config_generator

# Run it:
./Wireguard_config_generator

# Use sudo to run it if the write directory is /etc/wireguard:
sudo ./Wireguard_config_generator
```

Then, on the same machine after running the program successfully:

```bash
sudo wg-quick up <tunnelName>
sudo wg show
```

Run the generator **once per node**. Each node gets its own key pair; you exchange the printed public keys between them.

### A complete session example:

Answers typed by the user are shown after each prompt:

```
Generated this node's key pair.
PublicKey (share this with the peer): OCtpKVtWQ9WhZ5lMJ6BUIFkLgb1h61hXa76PVZHCNF8=

Tunnel name (e.g. wg0) (leave empty for wg0): MyTunnel
Where to save the config file (e.g., /home/user) (leave empty to save under /etc/wireguard):

This node's Address (e.g. 10.0.0.1/24): 10.0.0.1/24
ListenPort (default 51820):
Peer PublicKey (leave empty to finish): rT4bM7wQ9pL2zX5vC8nK1jH6gF3dS0aY7uI4oE2tR9M=
  AllowedIPs (e.g. 10.0.0.2/32): 10.0.0.2/32
  Endpoint (e.g. 192.168.56.102:51820): 192.168.56.102:51820
Peer PublicKey (leave empty to finish):
Config written to /etc/wireguard/MyTunnel.conf
```

### The file it produces

```
[Interface]
PrivateKey = PbGmhOAIfNHuiuIv2bQ1hXG8LpF+27jralASjaaZIrY=
Address = 10.0.0.1/24
ListenPort = 51820

[Peer]
PublicKey = rT4bM7wQ9pL2zX5vC8nK1jH6gF3dS0aY7uI4oE2tR9M=
AllowedIPs = 10.0.0.2/32
Endpoint = 192.168.56.102:51820
```

Bring the tunnel up:

```bash
sudo wg-quick up MyTunnel
sudo wg show
```

---

## 4. How it works

### 4.1 Generate the key pair

```go
privKey, err := ecdh.X25519().GenerateKey(rand.Reader)
if err != nil {
    fmt.Println("could not generate keys:", err)
    os.Exit(1)
}
```

WireGuard's handshake is built on Curve25519, so the key material must be X25519. `rand.Reader` is the cryptographically secure source from `crypto/rand`. 

Key generation is the very first thing that happens, so if entropy or the platform RNG is broken, the program fails before touching the filesystem.

### 4.2 Encode the keys

```go
privateKey := base64.StdEncoding.EncodeToString(privKey.Bytes())
publicKey := base64.StdEncoding.EncodeToString(privKey.PublicKey().Bytes())
```

WireGuard config files carry keys as standard base64 of the raw 32 bytes.

The public key is derived from the private key rather than generated separately (the two are mathematically bound and cannot drift apart).

### 4.3 Show the user the public key

```go
fmt.Println("Generated this node's key pair.")
fmt.Println("PublicKey (share this with the peer):", publicKey)
```

### 4.4 The prompt helper

```go
func ask(reader *bufio.Reader, question string) string {
    fmt.Print(question)
    answer, _ := reader.ReadString('\n')
    return strings.TrimSpace(answer)
}
```

One helper for every question in the program. `fmt.Print` (no newline) keeps the cursor on the prompt line. 

`TrimSpace` removes the trailing `\n` plus any stray spaces, which is what makes the "leave empty for the default" checks (`if x == ""`) work reliably.

### 4.5 Output path

The program asks:

```go
outputPath := ask(reader, "Where to save the config file (e.g., /home/user) (leave empty to save under /etc/wireguard): ")
```

This exists to allow the user to save the configuration file in a different directory or skip and use the default one (`/etc/wireguard`):

```go
if outputPath == "" {
	outputPath = fmt.Sprintf("/etc/wireguard/%s.conf", tunnelName)
} else {
	outputPath = outputPath + "/" + tunnelName + ".conf"
}
```

> The whole config is assembled in memory as a single string, then written once. Nothing is written to disk until every question has been answered, so abandoning the program halfway leaves no partial file behind.
> 

### 4.6 Interface section

```go
address := ask(reader, "This node's Address (e.g. 10.0.0.1/24): ")
listenPort := ask(reader, "ListenPort (default 51820): ")
if listenPort == "" {
    listenPort = "51820"
}

config := fmt.Sprintf("[Interface]\nPrivateKey = %s\nAddress = %s\nListenPort = %s\n",
    privateKey, address, listenPort)
```

### 4.7 Peers section

```go
for {
    pub := ask(reader, "Peer PublicKey (leave empty to finish): ")
    if pub == "" {
        break
    }
    ips := ask(reader, "  AllowedIPs (e.g. 10.0.0.2/32): ")
    end := ask(reader, "  Endpoint (e.g. 192.168.56.102:51820): ")

    config += fmt.Sprintf("\n[Peer]\nPublicKey = %s\nAllowedIPs = %s\nEndpoint = %s\n",
        pub, ips, end)
}
```

An unbounded loop, because a node can have any number of peers. A peer without a public key is meaningless, so an empty answer ends the loop.

The leading `\n` in the format string puts a blank line before each `[Peer]` header, which is what makes the output readable.

### 4.8 Write the file

```go
err = os.WriteFile(outputPath, []byte(config), 0600)
if err != nil {
    fmt.Println("could not write config:", err)
    os.Exit(1)
}
fmt.Printf("Config written to %s\n", outputPath)
```

The file contains an unencrypted private key. The mode `0600` means read/write for the owner and nothing for anyone else.

`os.Exit(1)` on failure gives the shell a non-zero status, so the tool composes correctly in scripts.

---

## 5. Variable reference

All variables live inside `main` unless noted otherwise.

| Variable | Type | Purpose |
| --- | --- | --- |
| `privKey` | `*ecdh.PrivateKey` | The freshly generated X25519 private key object. |
| `err` | `error` | Error status, checked twice: after key generation and after writing the file. Non-nil either time aborts with exit code 1. |
| `privateKey` | `string` | Base64 encoding of the raw private key bytes. Written into the `[Interface]` section of the config. Never printed to the screen. |
| `publicKey` | `string` | Base64 encoding of the public key derived from `privKey`. Printed once for the user to share with peers; not stored anywhere. |
| `reader` | `*bufio.Reader` | Buffered reader wrapping `os.Stdin`. Created once and passed to every `ask` call. |
| `tunnelName` | `string` | Name of the tunnel/interface. |
| `outputPath` | `string` | Full path of the config file to write. |
| `address` | `string` | This node's `Address` value (IP with CIDR mask) for the `[Interface]` section. |
| `listenPort` | `string` | The `ListenPort` value for the `[Interface]` section. |
| `config` | `string` | The entire config file assembled in memory. Starts as the `[Interface]` section, grows by one `[Peer]` block per loop iteration, and is written to disk in a single `os.WriteFile` call. |
| `pub`, `ips`, `end` | `string` (loop-local) | One peer's `PublicKey`, `AllowedIPs`, and `Endpoint`, re-read on every iteration of the peer loop. An empty `pub` ends the loop. |

**Inside the `ask` helper:** 

| Variable | Type | Purpose |
| --- | --- | --- |
| `reader` | `*bufio.Reader` | **Parameter.** The shared stdin reader passed in from `main`. |
| `question` | `string` | **Parameter.** The prompt text printed without a trailing newline. |
| `answer` | `string` | Holds the raw line read from stdin before `TrimSpace` strips the trailing newline and surrounding whitespace; the trimmed result is what the function returns. |

---

## 6. Default values

Every default is applied when the user answers a prompt with an empty line (just presses Enter).

| Variable | Default | Notes |
| --- | --- | --- |
| `tunnelName` | `wg0` | The conventional name for a first WireGuard interface. Also determines the config filename. |
| `outputPath` | `/etc/wireguard/<tunnelName>.conf` | The standard directory `wg-quick` searches. Writing there normally requires root. |
| `listenPort` | `51820` | WireGuard's registered default UDP port. |

---

## 7. Known limitations & troubleshooting

| # | Limitation | Impact | Planned fix |
| --- | --- | --- | --- |
| 1 | Empty answers (e.g., an empty `Endpoint` answer still emits `Endpoint =`) | `wg-quick` rejects the file | Only append the line when the answer is non-empty |
| 2 | No input validation | A typo produces a file that fails only at `wg-quick up` time | Add input validation |
| 3 | Output directory is not created | `os.WriteFile` fails with "no such file or directory" | _ |
| 4 | Existing config overwritten without warning | Silent loss of a working key pair | _ |
| 5 | Path is built with string concatenation | A trailing slash yields `/home/user//wg0.conf` | `filepath.Join` |
| 6 | Very basic config generation | Needs manual editing when you need to add additional settings | Add other prompts |
| 7 | Interactive only | Can't be used in provisioning scripts | Add `flag`-based non-interactive mode |
| 8 | No robust error handling | _ | Needs refinements |
| 9 | The tool prints the public key to stdout and stores it nowhere | _ | _ |

| Symptom | Cause | Fix |
| --- | --- | --- |
| `could not write config: permission denied` | Writing to `/etc/wireguard` as a normal user | Run with `sudo`, or give a directory you own |
| `could not write config: no such file or directory` | Output directory doesn't exist | Create it first: `sudo mkdir -p /etc/wireguard` |