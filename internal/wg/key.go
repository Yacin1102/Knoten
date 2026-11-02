package wg

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

const KeyLen = 32

type Key [KeyLen]byte

type KeyPair struct {
	PrivateKey Key
	PublicKey  Key
}

// GenerateKeyPair generates a new WireGuard key pair and fills the provided KeyPair struct with the generated keys.
func GenerateKeyPair() KeyPair {
	var privateKey Key
	rand.Read(privateKey[:])
	privateKey[0] &= 248
	privateKey[31] &= 127
	privateKey[31] |= 64

	return KeyPair{PrivateKey: privateKey, PublicKey: privateKey.PublicKey()}
}

// PublicKey computes the public key for private key k, as `wg pubkey` does.
func (k Key) PublicKey() Key {
	priv, err := ecdh.X25519().NewPrivateKey(k[:])
	if err != nil {
		panic(err) // only possible for a key that is not 32 bytes
	}
	return Key(priv.PublicKey().Bytes())
}

// String returns the key in base64.
func (k Key) String() string {
	return base64.StdEncoding.EncodeToString(k[:])
}

// hex returns the key the way the device protocol wants it.
func (k Key) KeyInHex() string {
	return hex.EncodeToString(k[:])
}

// IsZero reports whether k is unset.
func (k Key) IsZero() bool {
	return k == Key{}
}

// ParseKey reads a base64 key
func ParseKey(s string) (Key, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return Key{}, fmt.Errorf("key is not valid base64: %w", err)
	}
	if len(b) != KeyLen {
		return Key{}, fmt.Errorf("key is %d bytes, want %d", len(b), KeyLen)
	}
	return Key(b), nil
}
