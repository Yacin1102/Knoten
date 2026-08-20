package protocol

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const (
	PathRegister = "/v1/register"
	PathSync = "/v1/sync"
	PathHealth = "/v1/health"
)

const DefaultSyncIntervalSeconds = 30

type RegisterRequest struct {

	PublicKey string `json:"public_key"`
	Name string `json:"name"`
	ListenPort int `json:"listen_port"`

	JoinToken string `json:"join_token,omitempty"`
}

type RegisterResponse struct {

	VPNIP string `json:"vpn_ip"`
	VPNCIDR string `json:"vpn_cidr"`
	Endpoint string `json:"endpoint"`
}

type SyncRequest struct {

	PublicKey string `json:"public_key"`
	ListenPort int `json:"listen_port"`

	JoinToken string `json:"join_token,omitempty"`
}

type Peer struct {
	
	PublicKey string `json:"public_key"`
	Name string `json:"name"`
	VPNIP string `json:"vpn_ip"`
	Endpoint string `json:"endpoint"`
	LastSeen int64 `json:"last_seen"`
}

type SyncResponse struct {
	Self Peer `json:"self"`
	Peers []Peer `json:"peers"`
	SyncIntervalSeconds int `json:"sync_interval_seconds"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

var ErrUnknownMachine = errors.New("machine is not registered on this server")

var ErrEmptyKey = errors.New("public key is empty")

const wgKeyLength = 32

func ValidatePublicKey(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return ErrEmptyKey
	}
	
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return fmt.Errorf("public key is not valid base64: %w", err)
	}

	if len(raw) != wgKeyLength {
		return fmt.Errorf("public key decodes to %d bytes, want %d", len(raw), wgKeyLength)
	}

	return nil
}

func ValidateListenPort(p int) error {
	if p < 1 || p > 65535 {
		return fmt.Errorf("listen port %d is out of range 1-65535", p)
	}
	
	return nil
}

func ValidateName(s string) error {
	if len(s) > 64 {
		return fmt.Errorf("name is %d characters, maximum is 64", len(s))
	}

	for _, r := range s {
		if r < 32 || r == 127 {
			return errors.New("name contains a control character")
		}
	}

	return nil
}