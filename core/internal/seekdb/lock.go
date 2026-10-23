package seekdb

import (
	"context"
	"fmt"
	"os"
	"time"
)

// The server and its clients agree on two lock files in run/. Each client
// holds seekdb.clients shared for as long as it uses the server; the server
// blocks on an exclusive lock of the same file and exits the moment it gets
// it, so the server ends when its last client releases the lock or dies.
// seekdb.startup is held exclusively while a client spawns the server, so
// two clients never start two servers on one directory.
const (
	clientsLockName = "seekdb.clients"
	startupLockName = "seekdb.startup"
	lockPollEvery   = 200 * time.Millisecond
)

type fileLock struct {
	file *os.File
}

func openLock(path string) (*fileLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return &fileLock{file: file}, nil
}

// lock waits for the lock until ctx ends.
func (lock *fileLock) lock(ctx context.Context, exclusive bool) error {
	for {
		acquired, err := lock.tryLock(exclusive)
		if err != nil {
			return err
		}
		if acquired {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(lockPollEvery):
		}
	}
}

func (lock *fileLock) close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	_ = lock.unlock()
	err := lock.file.Close()
	lock.file = nil
	return err
}
