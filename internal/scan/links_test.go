package scan

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// linkFixture has one file reachable from a/, b/ and c/deep/, plus plain files.
func linkFixture(t *testing.T) string {
	root := fixture(t)
	orig := filepath.Join(root, "a", "shared")
	write(t, orig, 50000)
	for _, p := range []string{"b/shared", "c/deep/shared"} {
		p = filepath.Join(root, p)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.Link(orig, p); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// sameTotals checks got against a fresh scan. Which path owns a hard link may
// differ, so only totals are compared, plus that exactly one path is counted.
func sameTotals(t *testing.T, got *Tree, root string) {
	t.Helper()
	want := mustScan(t, root)
	if got.Root.Size() != want.Root.Size() || got.Root.Files() != want.Root.Files() {
		t.Errorf("size/files = %d/%d, fresh scan %d/%d",
			got.Root.Size(), got.Root.Files(), want.Root.Size(), want.Root.Files())
	}
	counted := 0
	var walk func(n *Node) int64
	walk = func(n *Node) int64 {
		if !n.IsDir {
			if _, ok := got.links.ino(n); ok && n.Size() > 0 {
				counted++
			}
			return n.Size()
		}
		var sum int64
		for _, c := range n.Children() {
			sum += walk(c)
		}
		if sum != n.Size() {
			t.Errorf("%s: size %d, children add up to %d", n.Path(), n.Size(), sum)
		}
		return sum
	}
	walk(got.Root)
	if linked := len(got.links.inodes); counted != linked {
		t.Errorf("%d hard-linked paths counted, want %d", counted, linked)
	}
}

func TestRescanLinks(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(root string)
		dirs   []string
	}{
		{"relisted, unchanged", func(string) {}, []string{"a", "b"}},
		{"every path relisted", func(string) {}, []string{"", "a", "b", "c", "c/deep"}},
		{"one path removed", func(root string) {
			os.Remove(filepath.Join(root, "a", "shared"))
		}, []string{"a"}},
		{"all but one removed", func(root string) {
			os.Remove(filepath.Join(root, "a", "shared"))
			os.RemoveAll(filepath.Join(root, "c", "deep"))
		}, []string{"a", "c"}},
		{"grown", func(root string) {
			write(t, filepath.Join(root, "b", "shared"), 200000)
		}, []string{"b"}},
		{"new path", func(root string) {
			os.Link(filepath.Join(root, "b", "shared"), filepath.Join(root, "top2"))
		}, []string{""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := linkFixture(t)
			tree := mustScan(t, root)
			tc.mutate(root)
			var changes []Change
			for _, d := range tc.dirs {
				changes = append(changes, Change{Path: filepath.Join(root, d)})
			}
			// twice: a second pass must not count anything again
			for range 2 {
				if err := Rescan(context.Background(), tree, changes, &Progress{}); err != nil {
					t.Fatal(err)
				}
				sameTotals(t, tree, root)
			}
		})
	}
}

func TestRemoveLink(t *testing.T) {
	root := linkFixture(t)
	tree := mustScan(t, root)
	before := tree.Root.Size()
	for _, n := range tree.links.inodes {
		owner := n.owner
		if err := os.Remove(owner.Path()); err != nil {
			t.Fatal(err)
		}
		tree.Remove(owner)
	}
	if tree.Root.Size() != before {
		t.Errorf("size = %d after removing one path, want %d (still linked)", tree.Root.Size(), before)
	}
	sameTotals(t, tree, root)

	os.RemoveAll(filepath.Join(root, "c"))
	tree.Remove(tree.Root.Sorted()[slicesIndex(tree.Root, "c")])
	sameTotals(t, tree, root)
}

func slicesIndex(n *Node, name string) int {
	for i, c := range n.Sorted() {
		if c.Name == name {
			return i
		}
	}
	panic(name)
}

func TestSaveLoadLinks(t *testing.T) {
	root := linkFixture(t)
	tree := mustScan(t, root)
	var buf bytes.Buffer
	if err := Save(&buf, tree, 1); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := Load(&buf)
	if err != nil {
		t.Fatal(err)
	}
	sameTotals(t, loaded, root)

	os.Remove(filepath.Join(root, "a", "shared"))
	changes := []Change{{Path: filepath.Join(root, "a")}, {Path: filepath.Join(root, "b")}}
	if err := Rescan(context.Background(), loaded, changes, &Progress{}); err != nil {
		t.Fatal(err)
	}
	sameTotals(t, loaded, root)
}
