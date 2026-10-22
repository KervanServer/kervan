package memory_test

import (
	"errors"
	"io"
	"os"
	"sync"
	"testing"

	"github.com/kervanserver/kervan/internal/storage/local"
	"github.com/kervanserver/kervan/internal/storage/memory"
	"github.com/kervanserver/kervan/internal/vfs"
)

func TestBackendExclusiveCreatePreservesExisting(t *testing.T) {
	factories := map[string]func(*testing.T) vfs.FileSystem{
		"memory": func(t *testing.T) vfs.FileSystem { return memory.New() },
		"local_control": func(t *testing.T) vfs.FileSystem {
			b, err := local.New(local.Options{Root: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			return b
		},
	}
	for name, factory := range factories {
		t.Run(name, func(t *testing.T) {
			for _, trunc := range []bool{false, true} {
				label := "without_trunc"
				if trunc {
					label = "with_trunc"
				}
				t.Run(label, func(t *testing.T) {
					b := factory(t)
					flags := os.O_CREATE | os.O_EXCL | os.O_WRONLY
					if trunc {
						flags |= os.O_TRUNC
					}
					f, err := b.Open("/file.txt", flags, 0o644)
					if err != nil {
						t.Fatalf("control: exclusive creation of missing path: %v", err)
					}
					const original = "content must survive"
					if _, err := f.Write([]byte(original)); err != nil {
						t.Fatal(err)
					}
					if err := f.Close(); err != nil {
						t.Fatal(err)
					}
					duplicate, err := b.Open("/file.txt", flags, 0o644)
					if duplicate != nil {
						_ = duplicate.Close()
					}
					if !errors.Is(err, os.ErrExist) {
						t.Errorf("FAIL: exclusive create of existing path returned %v, want os.ErrExist", err)
					}
					r, err := b.Open("/file.txt", os.O_RDONLY, 0)
					if err != nil {
						t.Fatal(err)
					}
					got, readErr := io.ReadAll(r)
					closeErr := r.Close()
					if readErr != nil || closeErr != nil {
						t.Fatalf("read=%v close=%v", readErr, closeErr)
					}
					if string(got) != original {
						t.Errorf("FAIL: exclusive create changed existing contents to %q, want %q", got, original)
					}
					// A normal, non-exclusive open must still allow an overwrite.
					w, err := b.Open("/file.txt", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
					if err != nil {
						t.Fatalf("control: ordinary overwrite: %v", err)
					}
					if err := w.Close(); err != nil {
						t.Fatal(err)
					}
					info, err := b.Stat("/file.txt")
					if err != nil || info.Size() != 0 {
						t.Fatalf("control: truncate failed: info=%v err=%v", info, err)
					}
				})
			}
			t.Run("existing_directory", func(t *testing.T) {
				b := factory(t)
				if err := b.Mkdir("/dir", 0o755); err != nil {
					t.Fatal(err)
				}
				f, err := b.Open("/dir", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
				if f != nil {
					_ = f.Close()
				}
				if !errors.Is(err, os.ErrExist) {
					t.Errorf("FAIL: exclusive create over directory returned %v, want os.ErrExist", err)
				}
			})
		})
	}
}

func TestBackendExclusiveCreateHasOneWinner(t *testing.T) {
	b := memory.New()
	const callers = 16
	start := make(chan struct{})
	results := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() {
			<-start
			f, err := b.Open("/claimed.txt", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
			if f != nil {
				if closeErr := f.Close(); closeErr != nil && err == nil {
					err = closeErr
				}
			}
			results <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, os.ErrExist) {
			t.Errorf("unexpected error: %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("FAIL: %d exclusive creators succeeded, want exactly 1", winners)
	}
}
