package main

import (
	"os"
	"path/filepath"
)

// detachEnv marks a child that is already running in its own session, so -d
// cannot relaunch itself forever.
const detachEnv = "OWN_CODE_DETACHED"

// detachLogLimit is the size after which the next detached launch moves the
// active log to own-code.log.1. Only one previous file is kept, so the two
// logs stay bounded at roughly twice this size.
const detachLogLimit = 1 << 20

// rotateDetachLog moves an oversized log aside before the next detached run.
// The rename replaces the previous .1 atomically on Unix; if the platform
// refuses because an older process still has the file open, the current log
// is kept and rotation is retried on a later launch.
func rotateDetachLog(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < detachLogLimit {
		return
	}
	_ = os.Rename(path, path+".1")
}

// detachLogPath is where detached runs append their stderr.
func detachLogPath() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "own-code")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "own-code.log"), nil
}
