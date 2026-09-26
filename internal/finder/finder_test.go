//go:build darwin

package finder

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTrash(t *testing.T) {
	if testing.Short() {
		t.Skip("touches the user's Trash")
	}
	path := filepath.Join(t.TempDir(), "diskpy-trash-test")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest, err := trash(path)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(dest)
	if _, err := os.Lstat(dest); err != nil {
		t.Errorf("not in the Trash: %v", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Errorf("still there after Trash: %v", err)
	}
	if err := Trash(path); err == nil {
		t.Error("trashing a missing file succeeded")
	}
}
