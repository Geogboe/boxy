package providersdk

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const MaxScriptBytes = 4 << 20

// ScriptInterpreter identifies the guest-side runner for a staged script.
type ScriptInterpreter string

const (
	ScriptInterpreterAuto       ScriptInterpreter = "auto"
	ScriptInterpreterPowerShell ScriptInterpreter = "powershell"
	ScriptInterpreterSH         ScriptInterpreter = "sh"
)

// ScriptSpec is provider-neutral metadata for a script staged on a guest.
// Content is intentionally carried only for the duration of execution; the
// control plane must not persist or log it.
type ScriptSpec struct {
	Content     []byte            `json:"content"`
	Digest      string            `json:"digest"`
	Interpreter ScriptInterpreter `json:"interpreter"`
	Args        []string          `json:"args,omitempty"`
}

// NewScriptSpec builds a validated script payload from raw bytes.
func NewScriptSpec(content []byte, interpreter ScriptInterpreter, args []string) (*ScriptSpec, error) {
	digest := sha256.Sum256(content)
	s := &ScriptSpec{
		Content:     append([]byte(nil), content...),
		Digest:      hex.EncodeToString(digest[:]),
		Interpreter: interpreter,
		Args:        append([]string(nil), args...),
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return s, nil
}

// Validate checks payload shape. The server uses this before dispatch and
// should additionally call VerifyDigest so client-side hashes are not trusted.
func (s *ScriptSpec) Validate() error {
	if s == nil {
		return errors.New("script is required")
	}
	if len(s.Content) > MaxScriptBytes {
		return fmt.Errorf("script exceeds the %d MiB limit", MaxScriptBytes>>20)
	}
	if strings.TrimSpace(s.Digest) == "" {
		return errors.New("script digest is required")
	}
	if len(s.Digest) != sha256.Size*2 {
		return errors.New("script digest must be a SHA-256 hex string")
	}
	if _, err := hex.DecodeString(s.Digest); err != nil {
		return errors.New("script digest must be a SHA-256 hex string")
	}
	switch s.Interpreter {
	case "", ScriptInterpreterAuto, ScriptInterpreterPowerShell, ScriptInterpreterSH:
		return nil
	default:
		return fmt.Errorf("unsupported script interpreter %q", s.Interpreter)
	}
}

// VerifyDigest recomputes the payload digest. It is deliberately separate
// from Validate so callers can distinguish a malformed request from a client
// claiming a digest that does not match its bytes.
func (s *ScriptSpec) VerifyDigest() error {
	if err := s.Validate(); err != nil {
		return err
	}
	digest := sha256.Sum256(s.Content)
	if !strings.EqualFold(s.Digest, hex.EncodeToString(digest[:])) {
		return errors.New("script digest does not match content")
	}
	return nil
}
