//go:build windows

package secrets

import "os"

// Windows ACL validation is performed by the deployment/service boundary.
// The backend still writes restrictive mode metadata where supported and the
// DPAPI backend encrypts values before they reach disk.
func checkSecretFilePermissions(_ os.FileInfo) error { return nil }
