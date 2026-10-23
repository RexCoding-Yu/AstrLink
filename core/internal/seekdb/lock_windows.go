//go:build windows

package seekdb

import (
	"errors"
	"math"

	"golang.org/x/sys/windows"
)

// The server locks the whole file the same way, so a shared lock here and
// its exclusive lock overlap exactly.
func (lock *fileLock) tryLock(exclusive bool) (bool, error) {
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	var overlapped windows.Overlapped
	err := windows.LockFileEx(windows.Handle(lock.file.Fd()), flags, 0, math.MaxUint32, math.MaxUint32, &overlapped)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return false, nil
	}
	return err == nil, err
}

func (lock *fileLock) unlock() error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(lock.file.Fd()), 0, math.MaxUint32, math.MaxUint32, &overlapped)
}
