//go:build windows

package imgs2jxl

import (
	"golang.org/x/sys/windows"
)

func copyTimes(from, to string) error {
	// Package os cannot set NTFS creation time.
	src, err := openAttrs(from, windows.FILE_READ_ATTRIBUTES)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(src)

	dst, err := openAttrs(to, windows.FILE_WRITE_ATTRIBUTES)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(dst)

	var creation, lastWrite windows.Filetime
	if err := windows.GetFileTime(src, &creation, nil, &lastWrite); err != nil {
		return err
	}
	return windows.SetFileTime(dst, &creation, nil, &lastWrite)
}

func openAttrs(path string, access uint32) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, err
	}
	return windows.CreateFile(
		p,
		access,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
}
