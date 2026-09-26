package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"

	"diskpy/internal/scan"
)

// cachePath returns where the tree for root is stored between runs.
func cachePath(root string) string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	sum := sha256.Sum256([]byte(root))
	return filepath.Join(dir, "diskpy", hex.EncodeToString(sum[:8])+".tree")
}

func hasCache(root string) bool {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	_, err = os.Stat(cachePath(root))
	return err == nil
}

func loadCache(root string) (*scan.Node, uint64, error) {
	f, err := os.Open(cachePath(root))
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	n, stamp, err := scan.Load(f)
	if err == nil && n.Name != root {
		err = os.ErrNotExist // hash collision
	}
	return n, stamp, err
}

// saveCache writes to a temp file and renames it, so readers never see a
// partial tree. Failures only cost a full scan next time, so they're ignored.
func saveCache(n *scan.Node, stamp uint64) {
	path := cachePath(n.Name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.CreateTemp(filepath.Dir(path), "*.tmp")
	if err != nil {
		return
	}
	err = scan.Save(f, n, stamp)
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
