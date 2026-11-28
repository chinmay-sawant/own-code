//go:build windows

package agentstore

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

type profileLock struct{ file *os.File }

func acquireProfileLock(path string) (*profileLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("agent store: open profile lock: %w", err)
	}
	overlapped := new(windows.Overlapped)
	err = windows.LockFileEx(windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlapped)
	if err != nil {
		_ = file.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
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
	err := l.file.Close()
	l.file = nil
	return err
}
