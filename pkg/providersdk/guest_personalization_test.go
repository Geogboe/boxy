package providersdk_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Geogboe/boxy/pkg/providersdk"
)

func TestGuestCredentialRoundTripsOpaqueData(t *testing.T) {
	original := providersdk.GuestCredential{
		Kind: "password",
		Data: json.RawMessage(`{"username":"Administrator","password":"rotated"}`),
	}

	b, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal credential: %v", err)
	}

	var got providersdk.GuestCredential
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal credential: %v", err)
	}
	if got.Kind != original.Kind || string(got.Data) != string(original.Data) {
		t.Fatalf("credential = %+v, want %+v", got, original)
	}
}

func TestResolveSecretRef_Env(t *testing.T) {
	t.Setenv("BOXY_SECRET_TEST", "${BOXY_TEST_SECRET}")

	got, err := providersdk.ResolveSecretRef(context.Background(), providersdk.SecretRef("env:BOXY_SECRET_TEST"))
	if err != nil {
		t.Fatalf("ResolveSecretRef: %v", err)
	}
	if got != "${BOXY_TEST_SECRET}" {
		t.Fatalf("ResolveSecretRef = %q, want ${BOXY_TEST_SECRET}", got)
	}
}

func TestResolveSecretRef_Invalid(t *testing.T) {
	if _, err := providersdk.ResolveSecretRef(context.Background(), providersdk.SecretRef("bogus")); err == nil {
		t.Fatal("expected invalid secret ref error")
	}
}

func TestResolveSecretRef_RejectsMissingEnvAndUnsupportedKind(t *testing.T) {
	t.Setenv("BOXY_EMPTY_SECRET_TEST", "")

	tests := []providersdk.SecretRef{
		"",
		"env:BOXY_MISSING_SECRET_TEST",
		"env:BOXY_EMPTY_SECRET_TEST",
		"file:path",
	}
	for _, ref := range tests {
		if _, err := providersdk.ResolveSecretRef(context.Background(), ref); err == nil {
			t.Fatalf("ResolveSecretRef(%q) error = nil", ref)
		}
	}
}

func TestGuestAccessDetailsToProperties(t *testing.T) {
	if props := (providersdk.GuestAccessDetails{}).ToProperties(); props != nil {
		t.Fatalf("empty ToProperties = %+v, want nil", props)
	}

	details := providersdk.GuestAccessDetails{Properties: map[string]string{"ssh_host": "192.0.2.5"}}
	props := details.ToProperties()
	if props["ssh_host"] != "192.0.2.5" {
		t.Fatalf("props = %+v, want ssh_host", props)
	}
	props["ssh_host"] = "changed"
	if details.Properties["ssh_host"] != "192.0.2.5" {
		t.Fatalf("ToProperties aliased source map, got %q", details.Properties["ssh_host"])
	}
}
