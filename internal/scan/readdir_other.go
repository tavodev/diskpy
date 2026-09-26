//go:build !darwin

package scan

import (
	"os"
	"syscall"
)

// readDir is the portable fallback: readdir plus an lstat per entry.
func (s *scanner) readDir(path string, fn func(entry)) error {
	entries, err := os.ReadDir(path)
	for _, de := range entries {
		info, err := de.Info()
		if err != nil {
			s.progress.Errors.Add(1)
			continue
		}
		st := info.Sys().(*syscall.Stat_t)
		fn(entry{
			name:  []byte(de.Name()),
			isDir: de.IsDir(),
			mount: de.IsDir() && uint64(st.Dev) != s.dev,
			size:  int64(st.Blocks) * 512,
			ino:   uint64(st.Ino),
			nlink: uint32(st.Nlink),
		})
	}
	return err
}
