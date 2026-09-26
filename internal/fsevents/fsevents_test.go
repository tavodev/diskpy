//go:build darwin

package fsevents

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"diskpy/internal/scan"
)

func TestSince(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "sub")
	os.Mkdir(sub, 0o755)
	time.Sleep(time.Second) // let fseventsd flush the setup events

	id := Now()
	if err := os.WriteFile(filepath.Join(sub, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var changes []scan.Change
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		changes, err = Since(root, id, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if slices.ContainsFunc(changes, func(c scan.Change) bool { return c.Path == sub }) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Errorf("change in %s not reported; got %+v", sub, changes)
}

func TestSinceNothing(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	changes, err := Since(root, Now(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Errorf("expected no changes, got %+v", changes)
	}
}
