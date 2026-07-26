//go:build windows

package mesh

func protectSocket(string) error {
	// The socket inherits the current user's temporary-directory ACL.
	return nil
}
