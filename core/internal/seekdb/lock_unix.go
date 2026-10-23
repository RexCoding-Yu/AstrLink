//go:build !windows

package seekdb

import (
	"errors"

	"golang.org/x/sys/unix"
)

func (lock *fileLock) tryLock(exclusive bool) (bool, error) {
	how := unix.LOCK_SH
	if exclusive {
		how = unix.LOCK_EX
	}
	err := unix.Flock(int(lock.file.Fd()), how|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}

func (lock *fileLock) unlock() error {
	return unix.Flock(int(lock.file.Fd()), unix.LOCK_UN)
}
