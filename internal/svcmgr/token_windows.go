//go:build windows

package svcmgr

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// cryptProtectLocalMachine (CRYPTPROTECT_LOCAL_MACHINE) makes the
// encrypted blob decryptable by any sufficiently-privileged process on
// this machine, rather than tied to one interactive user's DPAPI master
// key — the right scope for a Windows Service, which commonly runs as
// LocalSystem or a dedicated service account rather than an interactive
// profile.
const cryptProtectLocalMachine = 0x4

// EncryptToken protects plaintext (the agent's single-use bootstrap
// token) at rest using DPAPI machine-scope, so the persisted service
// config file isn't a plaintext secret between install and first
// successful registration.
func EncryptToken(plaintext []byte) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, nil
	}
	in := windows.DataBlob{Size: uint32(len(plaintext)), Data: &plaintext[0]} //nolint:gosec // G115: len() is always non-negative; G103: unsafe pointer required for Windows DPAPI API
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, cryptProtectLocalMachine, &out); err != nil {
		return nil, fmt.Errorf("CryptProtectData: %w", err)
	}
	defer func() { _, _ = windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(out.Data)))) }() //nolint:gosec // G103: unsafe pointer required for Windows DPAPI API

	result := make([]byte, out.Size)
	copy(result, unsafe.Slice(out.Data, out.Size)) //nolint:gosec // G103: unsafe.Slice required to read DPAPI-allocated buffer
	return result, nil
}

// DecryptToken reverses EncryptToken.
func DecryptToken(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) == 0 {
		return nil, nil
	}
	in := windows.DataBlob{Size: uint32(len(ciphertext)), Data: &ciphertext[0]} //nolint:gosec // G115: len() is always non-negative; G103: unsafe pointer required for Windows DPAPI API
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, cryptProtectLocalMachine, &out); err != nil {
		return nil, fmt.Errorf("CryptUnprotectData: %w", err)
	}
	defer func() { _, _ = windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(out.Data)))) }() //nolint:gosec // G103: unsafe pointer required for Windows DPAPI API

	result := make([]byte, out.Size)
	copy(result, unsafe.Slice(out.Data, out.Size)) //nolint:gosec // G103: unsafe.Slice required to read DPAPI-allocated buffer
	return result, nil
}
