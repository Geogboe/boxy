//go:build !windows

package svcmgr

import (
	"bytes"
	"testing"
)

func TestEncryptDecryptToken_IdentityOnNonWindows(t *testing.T) {
	plain := []byte("single-use-registration-token")

	enc, err := EncryptToken(plain)
	if err != nil {
		t.Fatalf("EncryptToken: %v", err)
	}
	if !bytes.Equal(enc, plain) {
		t.Fatalf("EncryptToken = %q, want unchanged %q (Linux protects via file perms, not DPAPI)", enc, plain)
	}

	dec, err := DecryptToken(enc)
	if err != nil {
		t.Fatalf("DecryptToken: %v", err)
	}
	if !bytes.Equal(dec, plain) {
		t.Fatalf("DecryptToken = %q, want %q", dec, plain)
	}
}
