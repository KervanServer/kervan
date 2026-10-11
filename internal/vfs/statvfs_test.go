package vfs

import "testing"

type fakeReporter struct{ usedBytes, maxBytes, usedFiles, maxFiles int64 }

func (f fakeReporter) Usage() (int64, int64, int64, int64) {
	return f.usedBytes, f.maxBytes, f.usedFiles, f.maxFiles
}

func TestCapStatVFS(t *testing.T) {
	// Backend reports nothing: the quota becomes the whole picture.
	st := &StatVFS{}
	capStatVFS(st, fakeReporter{usedBytes: 1 << 20, maxBytes: 10 << 20, usedFiles: 3, maxFiles: 10})
	if st.BlockSize != 4096 || st.TotalBlocks != 2560 || st.FreeBlocks != 2304 || st.AvailBlocks != 2304 || st.TotalFiles != 10 || st.FreeFiles != 7 {
		t.Fatalf("unknown backend: %+v", st)
	}

	// Disk smaller than the quota: the disk's figures win.
	st = &StatVFS{BlockSize: 1024, TotalBlocks: 100, FreeBlocks: 50, AvailBlocks: 40}
	capStatVFS(st, fakeReporter{maxBytes: 1 << 30})
	if st.TotalBlocks != 100 || st.FreeBlocks != 50 || st.AvailBlocks != 40 {
		t.Fatalf("small disk: %+v", st)
	}

	// Large disk, small quota: the quota wins; over-quota usage floors at 0.
	st = &StatVFS{BlockSize: 1024, TotalBlocks: 1 << 30, FreeBlocks: 1 << 29, AvailBlocks: 1 << 29}
	capStatVFS(st, fakeReporter{usedBytes: 2048, maxBytes: 1024})
	if st.TotalBlocks != 1 || st.FreeBlocks != 0 || st.AvailBlocks != 0 {
		t.Fatalf("over quota: %+v", st)
	}

	// No limits: untouched.
	st = &StatVFS{BlockSize: 512, TotalBlocks: 9}
	capStatVFS(st, fakeReporter{})
	if st.TotalBlocks != 9 || st.BlockSize != 512 {
		t.Fatalf("unlimited changed figures: %+v", st)
	}
}
