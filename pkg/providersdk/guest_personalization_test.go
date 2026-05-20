package providersdk

import (
	"context"
	"testing"
)

func TestResolveSecretRef_Env(t *testing.T) {
	t.Setenv("BOXY_SECRET_TEST", "super-secret")

	got, err := ResolveSecretRef(context.Background(), SecretRef("env:BOXY_SECRET_TEST"))
	if err != nil {
		t.Fatalf("ResolveSecretRef: %v", err)
	}
	if got != "super-secret" {
		t.Fatalf("ResolveSecretRef = %q, want super-secret", got)
	}
}

func TestResolveSecretRef_Invalid(t *testing.T) {
	if _, err := ResolveSecretRef(context.Background(), SecretRef("bogus")); err == nil {
		t.Fatal("expected invalid secret ref error")
	}
}

func TestResolveSecretRef_RejectsMissingEnvAndUnsupportedKind(t *testing.T) {
	t.Setenv("BOXY_EMPTY_SECRET_TEST", "")

	tests := []SecretRef{
		"",
		"env:BOXY_MISSING_SECRET_TEST",
		"env:BOXY_EMPTY_SECRET_TEST",
		"file:path",
	}
	for _, ref := range tests {
		if _, err := ResolveSecretRef(context.Background(), ref); err == nil {
			t.Fatalf("ResolveSecretRef(%q) error = nil", ref)
		}
	}
}

func TestGuestAccessDetailsToProperties(t *testing.T) {
	if props := (GuestAccessDetails{}).ToProperties(); props != nil {
		t.Fatalf("empty ToProperties = %+v, want nil", props)
	}

	details := GuestAccessDetails{Properties: map[string]string{"ssh_host": "10.0.0.5"}}
	props := details.ToProperties()
	if props["ssh_host"] != "10.0.0.5" {
		t.Fatalf("props = %+v, want ssh_host", props)
	}
	props["ssh_host"] = "changed"
	if details.Properties["ssh_host"] != "10.0.0.5" {
		t.Fatalf("ToProperties aliased source map, got %q", details.Properties["ssh_host"])
	}
}
