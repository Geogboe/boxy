//go:build windows

package skills

import (
	"errors"
	"os"
	"syscall"
)

func createDirLink(canonicalPath, targetPath string) (bool, error) {
	if err := os.Symlink(canonicalPath, targetPath); err == nil {
		return false, nil
	} else if !errors.Is(err, syscall.Errno(1314)) {
		return false, err
	}
	if err := copyDir(canonicalPath, targetPath); err != nil {
		return false, err
	}
	return true, nil
}
