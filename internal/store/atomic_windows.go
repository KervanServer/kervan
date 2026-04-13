//go:build windows

package store

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func replaceFile(from, to string) error {
	fromPtr, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return fmt.Errorf("encode source path %s: %w", from, err)
	}
	toPtr, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return fmt.Errorf("encode target path %s: %w", to, err)
	}
	if err := windows.MoveFileEx(
		fromPtr,
		toPtr,
		windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH,
	); err != nil {
		return fmt.Errorf("replace file %s -> %s: %w", from, to, err)
	}
	return nil
}
