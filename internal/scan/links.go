package scan

import "sync"

// links tracks hard-linked files so data reachable by several paths is counted
// once: the owner of an inode carries its size, every other path has size 0.
// It lives with the tree, so updates and removals keep the accounting right.
type links struct {
	mu      sync.Mutex
	inodes  map[uint64]*inode
	byNode  map[*Node]uint64
	touched map[uint64]struct{} // inodes settle has to look at
}

type inode struct {
	size  int64 // allocated bytes, as last listed
	owner *Node // nil once the owner has been dropped
	nodes []*Node
}

func newLinks() *links {
	return &links{
		inodes:  map[uint64]*inode{},
		byNode:  map[*Node]uint64{},
		touched: map[uint64]struct{}{},
	}
}

// add records n as a path to ino and reports whether n owns the inode, i.e.
// should carry its size. A changed size is applied to the owner by settle.
func (l *links) add(ino uint64, size int64, n *Node) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.byNode[n] = ino
	in := l.inodes[ino]
	if in == nil {
		l.inodes[ino] = &inode{size: size, owner: n, nodes: []*Node{n}}
		return true
	}
	in.nodes = append(in.nodes, n)
	if in.size != size {
		in.size = size
		l.touched[ino] = struct{}{}
	}
	if in.owner == nil {
		in.owner = n
		return true
	}
	return false
}

// drop forgets the hard links in the subtree at n, which is leaving the tree.
// The caller takes n's size out of the totals.
func (l *links) drop(n *Node) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.byNode) == 0 {
		return
	}
	var walk func(n *Node)
	walk = func(n *Node) {
		if n.IsDir {
			for _, c := range n.Children() {
				walk(c)
			}
			return
		}
		ino, ok := l.byNode[n]
		if !ok {
			return
		}
		delete(l.byNode, n)
		in := l.inodes[ino]
		for i, o := range in.nodes {
			if o == n {
				in.nodes[i] = in.nodes[len(in.nodes)-1]
				in.nodes = in.nodes[:len(in.nodes)-1]
				break
			}
		}
		if in.owner == n {
			in.owner = nil
		}
		l.touched[ino] = struct{}{}
	}
	walk(n)
}

// settle gives each inode whose owner was dropped to one of its remaining
// paths, and brings owners' sizes up to date. Call it with no scan running.
func (l *links) settle() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for ino := range l.touched {
		in := l.inodes[ino]
		if len(in.nodes) == 0 {
			delete(l.inodes, ino)
			continue
		}
		if in.owner == nil {
			in.owner = in.nodes[0]
		}
		in.owner.addUp(in.size-in.owner.Size(), 0)
	}
	clear(l.touched)
}

// ino returns the inode of n if it is a tracked hard link.
func (l *links) ino(n *Node) (uint64, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	ino, ok := l.byNode[n]
	return ino, ok
}
