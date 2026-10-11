//go:build windows

package local

import (
	"golang.org/x/sys/windows"

	"github.com/kervanserver/kervan/internal/vfs"
)

func diskStats(path string) (*vfs.StatVFS, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	var availToCaller, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &availToCaller, &total, &free); err != nil {
		return nil, err
	}
	const blockSize = 4096
	return &vfs.StatVFS{
		BlockSize:   blockSize,
		TotalBlocks: total / blockSize,
		FreeBlocks:  free / blockSize,
		AvailBlocks: availToCaller / blockSize,
		NameMaxLen:  255,
	}, nil
}
