//go:build unix

package load

import (
	"os"
	"syscall"
)

// Nonblocking opens let the regular-file check reject FIFOs without waiting
// for a writer. Regular files keep their normal read behavior.
const localFileOpenFlags = os.O_RDONLY | syscall.O_NONBLOCK
