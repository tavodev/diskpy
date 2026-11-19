package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"

	"github.com/tavodev/diskpy/internal/scan"
)

// cachePath returns where the tree for root is stored between runs.
func cachePath(root string) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(root))
	return filepath.Join(dir, "diskpy", hex.EncodeToString(sum[:8])+".tree"), nil
}

func hasCache(root string) bool {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	path, err := cachePath(root)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

func loadCache(root string) (*scan.Tree, uint64, error) {
	path, err := cachePath(root)
	if err != nil {
		return nil, 0, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	t, stamp, err := scan.Load(f)
	if err == nil && t.Root.Name != root {
		err = os.ErrNotExist // hash collision
	}
	return t, stamp, err
}

// saveCache writes to a temp file and renames it, so readers never see a
// partial tree. Failures only cost a full scan next time, so they're ignored.
func saveCache(t *scan.Tree, stamp uint64) {
	path, err := cachePath(t.Root.Name)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	f, err := os.CreateTemp(filepath.Dir(path), "*.tmp")
	if err != nil {
		return
	}
	err = scan.Save(f, t, stamp)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		os.Remove(f.Name())
	}
}
