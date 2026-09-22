package meshnet

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// TestDeviceLogger_ForwardsToSlog covers the reason this logger exists at
// all: wireguard-go's own handshake diagnostics have to be reachable, or a
// mesh that reports success while passing no traffic is undiagnosable (see
// deviceLogger's doc comment and #372).
func TestDeviceLogger_ForwardsToSlog(t *testing.T) {
	var buf bytes.Buffer
	restore := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(restore) })

	logger := deviceLogger("bxmtest0")
	logger.Verbosef("Receiving handshake initiation from peer %d", 7)
	logger.Errorf("Failed to send handshake to peer %d", 7)

	out := buf.String()
	for _, want := range []string{
		"Receiving handshake initiation from peer 7",
		"Failed to send handshake to peer 7",
		"component=meshnet",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("slog output missing %q; got:\n%s", want, out)
		}
	}
}

// TestDeviceLogger_InterfaceIdentityIsInTheMessage pins the interface name
// to the message rather than a structured attr. The daemon's durable
// diagnostics path keeps a strict field allowlist with no "interface"
// field, so an attr would be silently dropped there and leave concurrent
// sandbox interfaces indistinguishable in `boxy diagnostics logs`; the
// message always survives. Regression guard for that (PR #377 review).
func TestDeviceLogger_InterfaceIdentityIsInTheMessage(t *testing.T) {
	var buf bytes.Buffer
	restore := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		// Drop every attr, emulating a handler (like the diagnostics
		// one) that only keeps its own allowlisted fields.
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.MessageKey || a.Key == slog.LevelKey || a.Key == slog.TimeKey {
				return a
			}
			return slog.Attr{}
		},
	})))
	t.Cleanup(func() { slog.SetDefault(restore) })

	deviceLogger("bxmtest0").Errorf("Failed to send handshake to peer %d", 7)

	if !strings.Contains(buf.String(), "bxmtest0") {
		t.Fatalf("interface identity lost when attrs are dropped; got:\n%s", buf.String())
	}
}

// TestDeviceLogger_VerboseSuppressedBelowDebug covers the cost guard:
// wireguard-go calls Verbosef per handshake and keepalive, so it must stay
// quiet (and skip formatting) unless debug logging is actually on.
func TestDeviceLogger_VerboseSuppressedBelowDebug(t *testing.T) {
	var buf bytes.Buffer
	restore := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(restore) })

	logger := deviceLogger("bxmtest0")
	logger.Verbosef("chatty per-keepalive line")
	if strings.Contains(buf.String(), "chatty per-keepalive line") {
		t.Fatalf("Verbosef leaked below debug level; got:\n%s", buf.String())
	}

	// Errors are not level-guarded -- a device-level error is worth
	// surfacing at default verbosity.
	logger.Errorf("real failure")
	if !strings.Contains(buf.String(), "real failure") {
		t.Fatalf("Errorf should surface at info level; got:\n%s", buf.String())
	}
}
