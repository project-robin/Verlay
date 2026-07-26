//go:build windows

package mesh

func syncDirectory(string) error {
	// Windows FlushFileBuffers does not support directory handles. The
	// temporary file itself is flushed before the atomic rename.
	return nil
}
