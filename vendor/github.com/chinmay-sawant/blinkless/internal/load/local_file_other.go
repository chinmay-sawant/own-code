//go:build !unix

package load

import "os"

const localFileOpenFlags = os.O_RDONLY
