package wg

import (
	"fmt"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

const KeyLen = 32 // wgh.KeyLen

type Key [KeyLen]byte

type KeyPair struct {
	PrivateKey wgtypes.Key
	PublicKey wgtypes.Key
}

// GenerateKeyPair generates a new WireGuard key pair and fills the provided KeyPair struct with the generated keys.
func GenerateKeyPair() (KeyPair, error) {
	privateKey, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		return KeyPair{}, fmt.Errorf("could not generate private key: %w", err)
	}
	publicKey := privateKey.PublicKey()

	return KeyPair{PrivateKey: privateKey, PublicKey: publicKey}, nil
}