//go:build darwin || linux

package auth

import (
	"os"
	"syscall"
)

type osFileSystem struct{}

func (osFileSystem) Lstat(path string) (os.FileInfo, error) {
	return os.Lstat(path)
}

func (osFileSystem) OpenReadOnlyNoFollow(path string) (*os.File, error) {
	flags := syscall.O_RDONLY | syscall.O_CLOEXEC | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
	fd, err := syscall.Open(path, flags, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func ownerID(info os.FileInfo) (uint32, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return stat.Uid, true
}
