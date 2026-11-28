//go:build !windows

package agentstore

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

type profileLock struct{ file *os.File }

// acquireProfileLock takes an exclusive, non-blocking advisory lock. The OS
// releases it if the process dies, so a crash never leaves a stale lock.
func acquireProfileLock(path string) (*profileLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("agent store: open profile lock: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrProfileOwned
		}
		return nil, fmt.Errorf("agent store: lock profile: %w", err)
	}
	return &profileLock{file: file}, nil
}

func (l *profileLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := l.file.Close() // closing the descriptor releases the flock
	l.file = nil
	return err
}
