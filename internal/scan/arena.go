package scan

import "unsafe"

const (
	nodeChunk = 4096
	ptrChunk  = 8192
	nameChunk = 64 << 10
)

// arena hands out nodes, child slices and name strings from large chunks,
// replacing millions of small heap objects with a few thousand big ones.
// Each worker owns one, so no locking is needed. Chunks stay alive as long
// as anything points into them, i.e. as long as the tree does.
type arena struct {
	nodes []Node
	ptrs  []*Node
	names []byte
}

func (a *arena) node() *Node {
	if len(a.nodes) == 0 {
		a.nodes = make([]Node, nodeChunk)
	}
	n := &a.nodes[0]
	a.nodes = a.nodes[1:]
	return n
}

// children returns an exactly sized copy of src.
func (a *arena) children(src []*Node) []*Node {
	dst := a.ptrSlice(len(src))
	copy(dst, src)
	return dst
}

// ptrSlice returns a zeroed slice of exactly n pointers.
func (a *arena) ptrSlice(n int) []*Node {
	if n == 0 {
		return nil
	}
	if n > ptrChunk/4 {
		return make([]*Node, n)
	}
	if len(a.ptrs) < n {
		a.ptrs = make([]*Node, ptrChunk)
	}
	dst := a.ptrs[:n:n]
	a.ptrs = a.ptrs[n:]
	return dst
}

// str copies b into the arena and returns it as a string.
func (a *arena) str(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	if len(b) > nameChunk/16 {
		return string(b)
	}
	if len(a.names) < len(b) {
		a.names = make([]byte, nameChunk)
	}
	s := unsafe.String(&a.names[0], len(b))
	copy(a.names, b)
	a.names = a.names[len(b):]
	return s
}
