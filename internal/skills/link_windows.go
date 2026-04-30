//go:build windows

package skills

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
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

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, name)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o750)
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		return copyLocalFile(name, target)
	})
}

func copyLocalFile(src, dst string) (retErr error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := in.Close(); cerr != nil && retErr == nil {
			retErr = fmt.Errorf("close source file %q: %w", src, cerr)
		}
	}()
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); cerr != nil && retErr == nil {
			retErr = fmt.Errorf("close dest file %q: %w", dst, cerr)
		}
	}()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}
