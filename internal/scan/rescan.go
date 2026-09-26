package scan

import (
	"cmp"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
)

// Change is a directory reported as modified since a tree was scanned.
type Change struct {
	Path      string
	Recursive bool // rescan the whole subtree, not just the entries
}

// Rescan brings a tree from an earlier scan up to date by re-listing only the
// changed directories; subdirectories that did not change keep their subtree.
// The tree is updated in place and stays readable while this runs. If it is
// cancelled, the tree is left partially updated and should be discarded.
func Rescan(ctx context.Context, t *Tree, changes []Change, p *Progress) error {
	root := t.Root
	info, err := os.Lstat(root.Name)
	if err != nil {
		return err
	}
	p.Root.Store(root)
	s := newScanner(ctx, p, uint64(info.Sys().(*syscall.Stat_t).Dev), t.links)

	var jobs []job
	queued := map[*Node]bool{}   // will be scanned recursively
	relisted := map[*Node]bool{} // entries already refreshed
	covered := func(n *Node) bool {
		for d := n; d != nil; d = d.Parent {
			if queued[d] {
				return true
			}
		}
		return false
	}

	// Level by level, so a parent is refreshed before its descendants.
	// Directories at the same depth are disjoint and refreshed in parallel.
	for _, level := range byDepth(normalize(root.Name, changes)) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Resolve first, then apply full rescans, so a directory is never
		// relisted when an ancestor is being rescanned in the same round.
		type resolved struct {
			node      *Node
			recursive bool
		}
		var todo []resolved
		for _, c := range level {
			n, exact := root.find(c.Path)
			for !n.IsDir {
				n, exact = n.Parent, false
			}
			todo = append(todo, resolved{n, c.Recursive || !exact})
		}
		for _, r := range todo {
			if r.recursive && !covered(r.node) && !relisted[r.node] {
				s.reset(r.node)
				queued[r.node] = true
				jobs = append(jobs, job{r.node, r.node.Path()})
			}
		}
		var targets []*Node
		for _, r := range todo {
			if !r.recursive && !covered(r.node) && !relisted[r.node] {
				relisted[r.node] = true
				targets = append(targets, r.node)
			}
		}
		for _, j := range s.relistAll(targets) {
			queued[j.node] = true
			jobs = append(jobs, j)
		}
	}
	s.run(jobs)
	if err := ctx.Err(); err != nil {
		return err
	}
	t.links.settle()
	return nil
}

// relistAll refreshes dirs concurrently and returns their new subdirectories.
func (s *scanner) relistAll(dirs []*Node) []job {
	var mu sync.Mutex
	var jobs []job
	next := make(chan *Node)
	var wg sync.WaitGroup
	for range min(Workers, len(dirs)) {
		wg.Go(func() {
			var a arena
			for d := range next {
				subdirs := s.relist(d, d.Path(), &a)
				mu.Lock()
				jobs = append(jobs, subdirs...)
				mu.Unlock()
			}
		})
	}
	for _, d := range dirs {
		next <- d
	}
	close(next)
	wg.Wait()
	return jobs
}

// byDepth groups changes that are already sorted by depth.
func byDepth(changes []Change) [][]Change {
	var levels [][]Change
	depth := -1
	for _, c := range changes {
		if d := strings.Count(c.Path, "/"); d != depth {
			levels = append(levels, nil)
			depth = d
		}
		levels[len(levels)-1] = append(levels[len(levels)-1], c)
	}
	return levels
}

// normalize keeps the changes inside root, merges duplicates and orders them
// by depth.
func normalize(root string, changes []Change) []Change {
	byPath := map[string]bool{}
	for _, c := range changes {
		p := filepath.Clean(c.Path)
		if rel, ok := strings.CutPrefix(p, root); !ok || (rel != "" && rel[0] != '/' && root != "/") {
			continue
		}
		byPath[p] = byPath[p] || c.Recursive
	}
	out := make([]Change, 0, len(byPath))
	for p, r := range byPath {
		out = append(out, Change{p, r})
	}
	slices.SortFunc(out, func(a, b Change) int {
		return cmp.Or(
			cmp.Compare(strings.Count(a.Path, "/"), strings.Count(b.Path, "/")),
			strings.Compare(a.Path, b.Path))
	})
	return out
}

// find returns the node at path, or its deepest existing ancestor with exact
// set to false. path must be inside the tree rooted at n.
func (n *Node) find(path string) (node *Node, exact bool) {
	rel := strings.Trim(strings.TrimPrefix(path, n.Name), "/")
	if rel == "" {
		return n, true
	}
	for name := range strings.SplitSeq(rel, "/") {
		i := slices.IndexFunc(n.Children(), func(c *Node) bool { return c.Name == name })
		if i < 0 {
			return n, false
		}
		n = n.Children()[i]
	}
	return n, true
}

// reset empties a directory and removes its totals from its ancestors.
func (s *scanner) reset(n *Node) {
	s.links.drop(n)
	size, files := n.Size(), int64(n.Files())
	n.children.Store(nil)
	n.addUp(-size, -files)
}

// relist refreshes the entries of dir. Subdirectories that still exist keep
// their subtree; new ones are returned to be scanned.
func (s *scanner) relist(dir *Node, path string, a *arena) []job {
	old := map[string]*Node{}
	for _, c := range dir.Children() {
		if c.IsDir {
			old[c.Name] = c
		} else {
			s.links.drop(c) // files are always replaced
		}
	}

	var children []*Node
	var subdirs []job
	var bytes, files int64         // new files
	var keptBytes, keptFiles int64 // reused subtrees
	err := s.readDir(path, func(e entry) {
		if e.isDir {
			if o, ok := old[string(e.name)]; ok {
				delete(old, o.Name)
				children = append(children, o)
				keptBytes += o.Size()
				keptFiles += int64(o.Files())
				return
			}
		}
		child := a.node()
		child.Name, child.Parent, child.IsDir = a.str(e.name), dir, e.isDir
		children = append(children, child)
		if e.isDir {
			if !e.mount {
				subdirs = append(subdirs, job{child, childPath(path, child.Name)})
			}
			return
		}
		child.files.Store(1)
		files++
		if e.nlink > 1 && !s.links.add(e.ino, e.size, child) {
			return
		}
		child.size.Store(e.size)
		bytes += e.size
	})
	if err != nil {
		s.progress.Errors.Add(1)
	}
	for _, gone := range old {
		s.links.drop(gone)
	}

	delta := bytes + keptBytes - dir.Size()
	deltaFiles := files + keptFiles - int64(dir.Files())
	cs := a.children(children)
	dir.children.Store(&cs)
	dir.addUp(delta, deltaFiles)
	s.progress.Files.Add(files)
	s.progress.Bytes.Add(bytes)
	return subdirs
}
