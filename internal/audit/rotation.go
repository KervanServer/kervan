package audit

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// EventAuditPruned records that retention removed a rotated audit file.
const EventAuditPruned EventType = "audit.pruned"

const rotatedTimeLayout = "20060102T150405.000000000Z"

// rotatedName is "<stem>-<UTC time>.<ext>" next to path, e.g.
// audit-20261009T073500.000000000Z.jsonl; names sort chronologically.
func rotatedName(path string, now time.Time) string {
	dir, base := filepath.Split(path)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	return filepath.Join(dir, stem+"-"+now.UTC().Format(rotatedTimeLayout)+ext)
}

// RotatedFiles lists the rotated siblings of path, oldest first.
func RotatedFiles(path string) ([]string, error) {
	dir, base := filepath.Split(path)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	matches, err := filepath.Glob(filepath.Join(dir, globEscape(stem)+"-*"+globEscape(ext)))
	if err != nil {
		return nil, err
	}
	out := matches[:0]
	for _, m := range matches {
		stamp := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(m), stem+"-"), ext)
		if _, err := time.Parse(rotatedTimeLayout, stamp); err == nil {
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out, nil
}

func globEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`).Replace(s)
}

// IsRotatedFileOf reports whether base is a rotated file name of path.
func IsRotatedFileOf(path, base string) bool {
	if base != filepath.Base(base) {
		return false
	}
	name := filepath.Base(path)
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if !strings.HasPrefix(base, stem+"-") || !strings.HasSuffix(base, ext) {
		return false
	}
	_, err := time.Parse(rotatedTimeLayout, strings.TrimSuffix(strings.TrimPrefix(base, stem+"-"), ext))
	return err == nil
}

// LogFiles lists the whole log, oldest first: rotated files, then path.
func LogFiles(path string) ([]string, error) {
	files, err := RotatedFiles(path)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err == nil {
		files = append(files, path)
	}
	return files, nil
}

type multiCloser struct {
	io.Reader
	files []*os.File
}

func (m *multiCloser) Close() error {
	var first error
	for _, f := range m.files {
		if err := f.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// OpenLog reads the whole log (rotated files then the live file) as one
// stream in chronological order. A missing log reads as empty.
func OpenLog(path string) (io.ReadCloser, error) {
	files, err := LogFiles(path)
	if err != nil {
		return nil, err
	}
	m := &multiCloser{}
	readers := make([]io.Reader, 0, len(files))
	for _, name := range files {
		// #nosec G304 -- audit paths are configured by trusted operators.
		f, err := os.Open(name)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue // pruned concurrently
			}
			_ = m.Close()
			return nil, err
		}
		m.files = append(m.files, f)
		readers = append(readers, f, bytes.NewReader([]byte("\n")))
	}
	m.Reader = io.MultiReader(readers...)
	return m, nil
}

// lastChainState returns the chain state of the last record in file.
func lastChainState(file string) (chainState, bool) {
	// #nosec G304 -- audit paths are configured by trusted operators.
	f, err := os.Open(file)
	if err != nil {
		return chainState{}, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return chainState{}, false
	}
	const tailWindow = 1 << 20
	start := max(info.Size()-tailWindow, 0)
	buf := make([]byte, info.Size()-start)
	if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
		return chainState{}, false
	}
	lines := bytes.Split(bytes.TrimRight(buf, "\n"), []byte("\n"))
	last := bytes.TrimSpace(lines[len(lines)-1])
	if _, st, ok, err := splitChained(last); ok && err == nil {
		return st, true
	}
	return chainState{}, false
}
