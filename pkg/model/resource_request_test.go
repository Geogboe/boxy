package model

import (
	"strings"
	"testing"
)

func TestResourceRequestValidateRejectsMalformedPackageReferences(t *testing.T) {
	for _, rawRef := range []string{"", "baseline", "@1.0.0", "baseline@", "baseline@1@2"} {
		req := ResourceRequest{
			Type:     ResourceTypeContainer,
			Profile:  ResourceProfileDefault,
			Count:    1,
			Packages: []string{rawRef},
		}
		if err := req.Validate(); err == nil || !strings.Contains(err.Error(), "must use name@version") {
			t.Errorf("Validate(%q) error = %v, want name@version validation error", rawRef, err)
		}
	}
}
