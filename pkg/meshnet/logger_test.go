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
		"interface=bxmtest0",
		"component=meshnet",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("slog output missing %q; got:\n%s", want, out)
		}
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
