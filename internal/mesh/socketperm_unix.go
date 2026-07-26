//go:build !windows

package mesh

import "os"

func protectSocket(path string) error {
	return os.Chmod(path, 0o600)
}
