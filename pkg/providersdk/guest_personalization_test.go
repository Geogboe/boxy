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
