//go:build !windows

package workspacefs

import "syscall"

type statfsImpl struct {
	fs syscall.Statfs_t
}

func (s *statfsImpl) Statfs(path string) error {
	return syscall.Statfs(path, &s.fs)
}

func (s *statfsImpl) FreeBytes() uint64 {
	return s.fs.Bavail * uint64(s.fs.Bsize)
}

func newStatfs() syscallStatfs {
	return &statfsImpl{}
}
