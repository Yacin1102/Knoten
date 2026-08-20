package wg

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

type KeyPair struct {
	PrivateKey string
	PublicKey string
}

func GenerateKeyPair() (KeyPair, error) {
	privKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return KeyPair{}, fmt.Errorf("could not generate X25519 key: %w", err)
	}

	privateKey := base64.StdEncoding.EncodeToString(privKey.Bytes())
	publicKey := base64.StdEncoding.EncodeToString(privKey.PublicKey().Bytes())

	return KeyPair{PrivateKey: privateKey, PublicKey: publicKey}, nil
}

func PublicKeyFrom(privateKeyBase64 string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(privateKeyBase64)
	if err != nil {
		return "", fmt.Errorf("private key is not valid base64: %w", err)
	}

	privKey, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return "", fmt.Errorf("private key is not a valid X25519 key: %w", err)
	}

	return base64.StdEncoding.EncodeToString(privKey.PublicKey().Bytes()), nil
}