//go:build !windows

package png2jxl

import (
	"os"
	"syscall"
	"time"
)

func copyTimes(from, to string) error {
	fi, err := os.Stat(from)
	if err != nil {
		return err
	}
	mtime := fi.ModTime()
	atime := mtime
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		atime = time.Unix(st.Atim.Sec, st.Atim.Nsec)
	}
	return os.Chtimes(to, atime, mtime)
}
