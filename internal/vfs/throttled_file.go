package vfs

import "github.com/kervanserver/kervan/internal/throttle"

// SetRateLimiters paces every file opened through u with the given limiters
// (e.g. the user's and the server-wide one); nil limiters are ignored.
func (u *UserVFS) SetRateLimiters(limiters ...*throttle.Limiter) {
	u.limiters = u.limiters[:0]
	for _, l := range limiters {
		if l != nil {
			u.limiters = append(u.limiters, l)
		}
	}
}

// throttledFile charges reads and writes against rate limiters, splitting
// large requests so no single call blocks for long.
type throttledFile struct {
	File
	limiters []*throttle.Limiter
}

func (t *throttledFile) chunk(n int) int {
	for _, l := range t.limiters {
		if c := l.ChunkSize(); c > 0 && c < n {
			n = c
		}
	}
	return n
}

func (t *throttledFile) wait(n int) {
	for _, l := range t.limiters {
		l.Wait(n)
	}
}

func (t *throttledFile) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return t.File.Read(p)
	}
	n, err := t.File.Read(p[:t.chunk(len(p))])
	t.wait(n)
	return n, err
}

func (t *throttledFile) ReadAt(p []byte, off int64) (int, error) {
	total := 0
	for total < len(p) {
		end := total + t.chunk(len(p)-total)
		n, err := t.File.ReadAt(p[total:end], off+int64(total))
		t.wait(n)
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func (t *throttledFile) Write(p []byte) (int, error) {
	total := 0
	for total < len(p) {
		end := total + t.chunk(len(p)-total)
		t.wait(end - total)
		n, err := t.File.Write(p[total:end])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func (t *throttledFile) WriteAt(p []byte, off int64) (int, error) {
	total := 0
	for total < len(p) {
		end := total + t.chunk(len(p)-total)
		t.wait(end - total)
		n, err := t.File.WriteAt(p[total:end], off+int64(total))
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
