package meshnet

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"golang.org/x/crypto/curve25519"
	"golang.zx2c4.com/wireguard/device"
)

// generateKeypair creates a fresh Curve25519 keypair in the exact form
// WireGuard requires: a private scalar with the standard X25519 clamping
// applied, and its corresponding public key. device.NoisePrivateKey has no
// exported generator of its own -- WireGuard keys are plain X25519 keys,
// so this clamps and derives them directly via golang.org/x/crypto/curve25519,
// the same primitive the device package itself is built on.
func generateKeypair() (device.NoisePrivateKey, device.NoisePublicKey, error) {
	var priv device.NoisePrivateKey
	if _, err := rand.Read(priv[:]); err != nil {
		return priv, device.NoisePublicKey{}, fmt.Errorf("generate private key: %w", err)
	}
	// Standard X25519 clamping (RFC 7748 section 5).
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64

	var pub device.NoisePublicKey
	privBytes := [32]byte(priv)
	pubBytes := [32]byte(pub)
	curve25519.ScalarBaseMult(&pubBytes, &privBytes)
	pub = device.NoisePublicKey(pubBytes)

	return priv, pub, nil
}

func hexEncode(key device.NoisePublicKey) string {
	return hex.EncodeToString(key[:])
}
