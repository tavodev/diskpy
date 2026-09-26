// Package scan walks a directory tree in parallel and builds a size-annotated tree.
package scan

import (
	"cmp"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
)

// Workers is the number of goroutines listing directories concurrently.
var Workers = runtime.NumCPU() * 2

// Node is a file or directory in the scanned tree. All methods are safe to
// call while a scan is still filling the tree in.
type Node struct {
	Name     string
	Parent   *Node
	children atomic.Pointer[[]*Node] // published once, when the dir is listed
	size     atomic.Int64            // bytes allocated on disk, grows during a scan
	files    atomic.Uint32           // files in this subtree (1 for a file)
	IsDir    bool
}

func (n *Node) Size() int64   { return n.size.Load() }
func (n *Node) Files() uint32 { return n.files.Load() }

// addUp adds to the totals of n and all its ancestors. Deltas may be negative.
func (n *Node) addUp(bytes, files int64) {
	for d := n; d != nil; d = d.Parent {
		d.size.Add(bytes)
		d.files.Add(uint32(files)) // two's complement: negative files subtract
	}
}

// Children returns the entries listed so far, in directory order.
func (n *Node) Children() []*Node {
	if c := n.children.Load(); c != nil {
		return *c
	}
	return nil
}

// Sorted returns a copy of n's children, largest first.
func (n *Node) Sorted() []*Node {
	type sized struct {
		size int64
		node *Node
	}
	children := n.Children()
	tmp := make([]sized, len(children))
	for i, c := range children {
		tmp[i] = sized{c.Size(), c} // snapshot: sizes may change while sorting
	}
	slices.SortFunc(tmp, func(a, b sized) int { return cmp.Compare(b.size, a.size) })
	out := make([]*Node, len(tmp))
	for i, t := range tmp {
		out[i] = t.node
	}
	return out
}

// Path returns the absolute path of the node.
func (n *Node) Path() string {
	if n.Parent == nil {
		return n.Name
	}
	return filepath.Join(n.Parent.Path(), n.Name)
}

// Tree is a scanned directory tree plus what keeping it up to date needs.
// Its methods must not be called while a scan or rescan is updating it.
type Tree struct {
	Root  *Node
	links *links
}

// Remove takes n, which must not be the root, out of the tree, e.g. after it
// was deleted. Data still reachable through a hard link elsewhere stays counted.
func (t *Tree) Remove(n *Node) {
	parent := n.Parent
	children := slices.DeleteFunc(slices.Clone(parent.Children()), func(c *Node) bool { return c == n })
	parent.children.Store(&children)
	parent.addUp(-n.Size(), -int64(n.Files()))
	t.links.drop(n)
	t.links.settle()
}

// Progress holds live counters, safe to read concurrently while a scan runs.
type Progress struct {
	Root   atomic.Pointer[Node] // set as soon as the scan starts
	Files  atomic.Int64
	Bytes  atomic.Int64
	Errors atomic.Int64
}

// entry is one directory entry as reported by readDir.
type entry struct {
	name         []byte // only valid during the readDir callback
	isDir, mount bool
	size         int64 // allocated bytes (files only)
	ino          uint64
	nlink        uint32
}

type job struct {
	node *Node
	path string
}

type scanner struct {
	ctx      context.Context
	progress *Progress
	dev      uint64

	// Pending directories, used as a stack so the walk stays depth-first and
	// the queue stays small.
	mu      sync.Mutex
	cond    *sync.Cond
	stack   []job
	pending int // queued + in progress

	links *links
}

// Scan walks root without following symlinks or crossing filesystem
// boundaries. The partial tree is available from p.Root while it runs.
func Scan(ctx context.Context, root string, p *Progress) (*Tree, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	st := info.Sys().(*syscall.Stat_t)
	n := &Node{Name: root, IsDir: info.IsDir()}
	if !n.IsDir {
		n.size.Store(st.Blocks * 512)
		n.files.Store(1)
	}
	p.Root.Store(n)
	t := &Tree{Root: n, links: newLinks()}
	if !n.IsDir {
		return t, nil
	}

	s := newScanner(ctx, p, uint64(st.Dev), t.links)
	s.run([]job{{n, root}})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	t.links.settle()
	return t, nil
}

func newScanner(ctx context.Context, p *Progress, dev uint64, l *links) *scanner {
	s := &scanner{
		ctx:      ctx,
		progress: p,
		dev:      dev,
		links:    l,
	}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// run scans the given directories, and everything below them, with a pool of
// workers. It returns when all are done or the context is cancelled.
func (s *scanner) run(jobs []job) {
	if len(jobs) == 0 {
		return
	}
	s.stack = append(s.stack[:0], jobs...)
	s.pending = len(jobs)
	var wg sync.WaitGroup
	for range Workers {
		wg.Go(s.work)
	}
	wg.Wait()
}

func (s *scanner) work() {
	var a arena
	var scratch []*Node
	for {
		s.mu.Lock()
		for len(s.stack) == 0 && s.pending > 0 {
			s.cond.Wait()
		}
		if s.pending == 0 {
			s.mu.Unlock()
			return
		}
		j := s.stack[len(s.stack)-1]
		s.stack = s.stack[:len(s.stack)-1]
		s.mu.Unlock()

		var subdirs []job
		if s.ctx.Err() == nil {
			subdirs, scratch = s.list(j, &a, scratch[:0])
		}

		s.mu.Lock()
		s.stack = append(s.stack, subdirs...)
		s.pending += len(subdirs) - 1
		if len(subdirs) > 0 || s.pending == 0 {
			s.cond.Broadcast()
		}
		s.mu.Unlock()
	}
}

// childPath joins a directory path and an entry name. It is filepath.Join
// without the cleaning, which the scanner doesn't need.
func childPath(dir, name string) string {
	if strings.HasSuffix(dir, "/") {
		return dir + name
	}
	return dir + "/" + name
}

// list reads one directory, attaches its children and returns its
// subdirectories. scratch is a reusable buffer, returned for the next call.
func (s *scanner) list(j job, a *arena, scratch []*Node) ([]job, []*Node) {
	dir := j.node
	var subdirs []job
	var files, bytes int64
	err := s.readDir(j.path, func(e entry) {
		child := a.node()
		child.Name, child.Parent, child.IsDir = a.str(e.name), dir, e.isDir
		scratch = append(scratch, child)

		if e.isDir {
			if !e.mount { // another filesystem: leave it empty
				subdirs = append(subdirs, job{child, childPath(j.path, child.Name)})
			}
			return
		}
		child.files.Store(1)
		files++
		if e.nlink > 1 && !s.links.add(e.ino, e.size, child) {
			return // hard link counted elsewhere
		}
		child.size.Store(e.size)
		bytes += e.size
	})
	if err != nil {
		s.progress.Errors.Add(1)
	}
	children := a.children(scratch)
	dir.children.Store(&children)
	dir.addUp(bytes, files)
	s.progress.Files.Add(files)
	s.progress.Bytes.Add(bytes)
	return subdirs, scratch
}
