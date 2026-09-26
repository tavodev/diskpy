package scan

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScan(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "small.txt"), 10)
	write(t, filepath.Join(root, "big", "a.bin"), 1<<20)
	write(t, filepath.Join(root, "big", "deep", "b.bin"), 1<<20)
	if err := os.Symlink(filepath.Join(root, "big"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(root, "big", "a.bin"), filepath.Join(root, "hard.bin")); err != nil {
		t.Fatal(err)
	}

	var p Progress
	tree, err := Scan(context.Background(), root, &p)
	if err != nil {
		t.Fatal(err)
	}
	n := tree.Root

	children := n.Sorted()
	if got := children[0].Name; got != "big" {
		t.Errorf("largest child = %q, want big", got)
	}
	big := children[0]
	if big.Size() < 1<<20 {
		t.Errorf("big size = %d, want >= %d", big.Size(), 1<<20)
	}
	// hard link counted once (wherever seen first); symlink contributes no data
	if n.Size() < 2<<20 || n.Size() >= 3<<20 {
		t.Errorf("root size = %d, want in [2MiB, 3MiB)", n.Size())
	}
	if n.Files() != 5 {
		t.Errorf("files = %d, want 5", n.Files())
	}
	if p.Bytes.Load() != n.Size() {
		t.Errorf("progress bytes = %d, root size = %d", p.Bytes.Load(), n.Size())
	}
	if p.Root.Load() != n {
		t.Error("progress root not set")
	}
	if got := big.Sorted()[0].Path(); got != filepath.Join(n.Name, "big", "a.bin") && got != filepath.Join(n.Name, "big", "deep") {
		t.Errorf("unexpected child path %q", got)
	}
}

func TestScanCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Scan(ctx, t.TempDir(), &Progress{}); err == nil {
		t.Error("expected context error")
	}
}

// TestScanLiveReads reads the tree while it is being built; run with -race.
func TestScanLiveReads(t *testing.T) {
	root := t.TempDir()
	for i := range 50 {
		for j := range 20 {
			write(t, filepath.Join(root, fmt.Sprint("d", i), fmt.Sprint("f", j)), 100)
		}
	}

	var p Progress
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := Scan(context.Background(), root, &p); err != nil {
			t.Error(err)
		}
	}()

	var walk func(n *Node)
	walk = func(n *Node) {
		_ = n.Size() + int64(n.Files())
		for _, c := range n.Sorted() {
			_ = c.Name + c.Path()
			walk(c)
		}
	}
	for {
		if r := p.Root.Load(); r != nil {
			walk(r)
		}
		select {
		case <-done:
			if got := p.Root.Load().Files(); got != 1000 {
				t.Errorf("files = %d, want 1000", got)
			}
			return
		default:
		}
	}
}

func TestChildPath(t *testing.T) {
	for _, tc := range []struct{ dir, name, want string }{
		{"/", "Users", "/Users"},
		{"/Users", "me", "/Users/me"},
		{"/Volumes/X/", "a", "/Volumes/X/a"},
	} {
		if got := childPath(tc.dir, tc.name); got != tc.want {
			t.Errorf("childPath(%q, %q) = %q, want %q", tc.dir, tc.name, got, tc.want)
		}
	}
}
