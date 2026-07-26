//go:build !windows

package mesh

import (
	"fmt"
	"os"
)

func securePathMode(name string, actual, expected os.FileMode) (bool, string) {
	if actual != expected {
		return false, fmt.Sprintf("%s has mode %04o, expected %04o", name, actual, expected)
	}
	return true, fmt.Sprintf("%s is protected with mode %04o", name, actual)
}
