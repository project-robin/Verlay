//go:build windows

package mesh

import "os"

func securePathMode(name string, _, _ os.FileMode) (bool, string) {
	return true, name + " inherits the current Windows account ACL"
}
