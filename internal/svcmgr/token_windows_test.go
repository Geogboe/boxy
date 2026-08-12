//go:build windows

package svcmgr

import (
	"bytes"
	"testing"
)

func TestEncryptDecryptToken_RoundTrips(t *testing.T) {
	plain := []byte("single-use-registration-token")

	enc, err := EncryptToken(plain)
	if err != nil {
		t.Fatalf("EncryptToken: %v", err)
	}
	if bytes.Equal(enc, plain) {
		t.Fatal("EncryptToken returned plaintext unchanged — expected DPAPI-encrypted bytes")
	}

	dec, err := DecryptToken(enc)
	if err != nil {
		t.Fatalf("DecryptToken: %v", err)
	}
	if !bytes.Equal(dec, plain) {
		t.Fatalf("DecryptToken = %q, want %q", dec, plain)
	}
}

func TestEncryptToken_EmptyInput_ReturnsEmptyOutput(t *testing.T) {
	enc, err := EncryptToken(nil)
	if err != nil {
		t.Fatalf("EncryptToken(nil): %v", err)
	}
	if len(enc) != 0 {
		t.Fatalf("EncryptToken(nil) = %v, want empty", enc)
	}
}
