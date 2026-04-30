//go:build !windows

package skills

import "os"

func createDirLink(canonicalPath, targetPath string) (bool, error) {
	return false, os.Symlink(canonicalPath, targetPath)
}
