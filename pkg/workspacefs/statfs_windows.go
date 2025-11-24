//go:build windows

package workspacefs

import "golang.org/x/sys/windows"

type statfsImpl struct {
	freeBytes uint64
}

func (s *statfsImpl) Statfs(path string) error {
	var freeBytesAvailable, totalBytes, totalFreeBytes uint64
	if err := windows.GetDiskFreeSpaceEx(windows.StringToUTF16Ptr(path), &freeBytesAvailable, &totalBytes, &totalFreeBytes); err != nil {
		return err
	}
	s.freeBytes = freeBytesAvailable
	return nil
}

func (s *statfsImpl) FreeBytes() uint64 {
	return s.freeBytes
}

func newStatfs() syscallStatfs {
	return &statfsImpl{}
}
