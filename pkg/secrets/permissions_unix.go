//go:build !windows

package secrets

import (
	"fmt"
	"os"
)

func checkSecretFilePermissions(info os.FileInfo) error {
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("file permissions %04o are too broad; require 0600 or stricter", info.Mode().Perm())
	}
	return nil
}
