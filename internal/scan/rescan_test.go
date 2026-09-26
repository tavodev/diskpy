package scan

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// dump renders a tree canonically: one line per node, children by name.
func dump(n *Node) string {
	var sb strings.Builder
	var walk func(n *Node, path string)
	walk = func(n *Node, path string) {
		fmt.Fprintf(&sb, "%s dir=%v size=%d files=%d\n", path, n.IsDir, n.Size(), n.Files())
		children := slices.Clone(n.Children())
		slices.SortFunc(children, func(a, b *Node) int { return strings.Compare(a.Name, b.Name) })
		for _, c := range children {
			walk(c, path+"/"+c.Name)
		}
	}
	walk(n, ".")
	return sb.String()
}

func mustScan(t *testing.T, root string) *Tree {
	t.Helper()
	n, err := Scan(context.Background(), root, &Progress{})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func fixture(t *testing.T) string {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "a", "x"), 5000)
	write(t, filepath.Join(root, "a", "deep", "y"), 70000)
	write(t, filepath.Join(root, "b", "z"), 9000)
	write(t, filepath.Join(root, "b", "sub", "w"), 9000)
	write(t, filepath.Join(root, "c", "keep"), 100)
	write(t, filepath.Join(root, "top"), 300)
	return root
}

func mutate(t *testing.T, root string) {
	write(t, filepath.Join(root, "a", "x"), 90000)           // grown
	write(t, filepath.Join(root, "a", "new"), 20000)         // added
	os.RemoveAll(filepath.Join(root, "b"))                   // removed subtree
	write(t, filepath.Join(root, "c", "n", "m", "f"), 40000) // new subtree
	os.Remove(filepath.Join(root, "top"))
}

func TestRescan(t *testing.T) {
	for _, tc := range []struct {
		name    string
		changes func(root string) []Change
	}{
		{"per-directory", func(root string) []Change {
			return []Change{{Path: root}, {Path: root + "/a"}, {Path: root + "/b"}, {Path: root + "/b/sub"},
				{Path: root + "/c"}, {Path: root + "/c/n"}, {Path: root + "/c/n/m"}}
		}},
		{"recursive root", func(root string) []Change {
			return []Change{{Path: root, Recursive: true}, {Path: root + "/a"}}
		}},
		{"recursive subtree", func(root string) []Change {
			return []Change{{Path: root}, {Path: root + "/a", Recursive: true}, {Path: root + "/c/"}, {Path: root + "/c/n/m"}}
		}},
		{"missing parent in same level", func(root string) []Change {
			// c/n is new and unreported, so c/n/m resolves to c, which must
			// be rescanned whole even though a/deep is relisted alongside it.
			return []Change{{Path: root}, {Path: root + "/a"}, {Path: root + "/a/deep/q"}, {Path: root + "/c/n/m"}, {Path: root + "/b/sub/q"}}
		}},
		{"only deepest reported", func(root string) []Change {
			return []Change{{Path: root}, {Path: root + "/a"}, {Path: root + "/c/n/m"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := fixture(t)
			tree := mustScan(t, root)
			mutate(t, root)
			changes := append(tc.changes(root), Change{Path: "/elsewhere"})
			if err := Rescan(context.Background(), tree, changes, &Progress{}); err != nil {
				t.Fatal(err)
			}
			if got, want := dump(tree.Root), dump(mustScan(t, root).Root); got != want {
				t.Errorf("rescanned tree differs from a fresh scan\ngot:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

func TestSaveLoad(t *testing.T) {
	tree := mustScan(t, fixture(t))
	var buf bytes.Buffer
	if err := Save(&buf, tree, 42); err != nil {
		t.Fatal(err)
	}
	loaded, stamp, err := Load(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if stamp != 42 {
		t.Errorf("stamp = %d, want 42", stamp)
	}
	if loaded.Root.Name != tree.Root.Name {
		t.Errorf("root = %q, want %q", loaded.Root.Name, tree.Root.Name)
	}
	if got, want := dump(loaded.Root), dump(tree.Root); got != want {
		t.Errorf("loaded tree differs\ngot:\n%s\nwant:\n%s", got, want)
	}
	if _, _, err := Load(bytes.NewReader(buf.Bytes()[:buf.Len()/2])); err == nil {
		t.Error("truncated file loaded without error")
	}
}
