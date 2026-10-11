//go:build linux || darwin || freebsd

package local

import (
	"golang.org/x/sys/unix"

	"github.com/kervanserver/kervan/internal/vfs"
)

func diskStats(path string) (*vfs.StatVFS, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return nil, err
	}
	// #nosec G115 -- statfs counters are non-negative.
	return &vfs.StatVFS{
		BlockSize:   uint64(st.Bsize),
		TotalBlocks: uint64(st.Blocks),
		FreeBlocks:  uint64(st.Bfree),
		AvailBlocks: uint64(st.Bavail),
		TotalFiles:  uint64(st.Files),
		FreeFiles:   uint64(st.Ffree),
		NameMaxLen:  255,
	}, nil
}
