package cli

import (
	"errors"
	"testing"
)

func TestProbeMeshCapability_DisabledNeverProbes(t *testing.T) {
	called := false
	orig := meshnetProbe
	t.Cleanup(func() { meshnetProbe = orig })
	meshnetProbe = func() error {
		called = true
		return nil
	}

	if got := probeMeshCapability(false); got {
		t.Fatal("expected false when the overlay is disabled")
	}
	if called {
		t.Fatal("expected the probe to never run when the overlay is disabled")
	}
}

func TestProbeMeshCapability_EnabledAndProbeSucceeds(t *testing.T) {
	orig := meshnetProbe
	t.Cleanup(func() { meshnetProbe = orig })
	meshnetProbe = func() error { return nil }

	if got := probeMeshCapability(true); !got {
		t.Fatal("expected true when the overlay is enabled and the probe succeeds")
	}
}

func TestProbeMeshCapability_EnabledAndProbeFails(t *testing.T) {
	orig := meshnetProbe
	t.Cleanup(func() { meshnetProbe = orig })
	meshnetProbe = func() error { return errors.New("create TUN device \"boxy-probe\": operation not permitted") }

	if got := probeMeshCapability(true); got {
		t.Fatal("expected false when the probe fails")
	}
}
