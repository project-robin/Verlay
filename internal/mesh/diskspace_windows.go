//go:build windows

package mesh

import "golang.org/x/sys/windows"

func availableDiskBytes(path string) (int64, bool) {
	directory, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, false
	}
	var available uint64
	if err := windows.GetDiskFreeSpaceEx(directory, &available, nil, nil); err != nil {
		return 0, false
	}
	if available > uint64(^uint64(0)>>1) {
		return int64(^uint64(0) >> 1), true
	}
	return int64(available), true
}
