package log

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenFileRotatesAndKeepsBackups(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "kervan.log")
	writer, err := OpenFile(logPath, 1, 2)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer writer.Close()

	chunk := make([]byte, 600*1024)
	for i := 0; i < 5; i++ {
		if _, err := writer.Write(chunk); err != nil {
			t.Fatalf("Write #%d: %v", i+1, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	for _, path := range []string{
		logPath,
		logPath + ".1",
		logPath + ".2",
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
	}
	if _, err := os.Stat(logPath + ".3"); !os.IsNotExist(err) {
		t.Fatalf("expected no third backup, got err=%v", err)
	}
}

func TestOpenFileReturnsNilForEmptyPath(t *testing.T) {
	writer, err := OpenFile("", 1, 1)
	if err != nil {
		t.Fatalf("OpenFile returned error: %v", err)
	}
	if writer != nil {
		t.Fatalf("expected nil writer for empty path")
	}
}
