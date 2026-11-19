package scan

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"path/filepath"
	"strings"
)

// File layout: magic, 8-byte stamp, then nodes in pre-order. Each node is a
// kind byte, a uvarint-prefixed name, and then the allocated size for a file,
// the size and inode for a hard-linked file, or the child count for a
// directory. Directory totals are recomputed on load.
const magic = "DSKP\x02"

const (
	kindFile = iota
	kindDir
	kindLink
)

var errCorrupt = errors.New("scan: corrupt tree file")

// Save writes a finished tree to w, with an opaque stamp (e.g. an FSEvents id).
func Save(w io.Writer, t *Tree, stamp uint64) error {
	t.links.mu.Lock()
	defer t.links.mu.Unlock()
	bw := bufio.NewWriterSize(w, 1<<20)
	bw.WriteString(magic)
	bw.Write(binary.LittleEndian.AppendUint64(nil, stamp))

	var buf []byte
	var write func(n *Node)
	write = func(n *Node) {
		ino, linked := t.links.byNode[n]
		kind := byte(kindFile)
		switch {
		case n.IsDir:
			kind = kindDir
		case linked:
			kind = kindLink
		}
		buf = append(buf[:0], kind)
		buf = binary.AppendUvarint(buf, uint64(len(n.Name)))
		buf = append(buf, n.Name...)
		switch kind {
		case kindFile:
			buf = binary.AppendUvarint(buf, uint64(n.Size()))
			bw.Write(buf)
			return
		case kindLink:
			buf = binary.AppendUvarint(buf, uint64(t.links.inodes[ino].size))
			buf = binary.AppendUvarint(buf, ino)
			bw.Write(buf)
			return
		}
		children := n.Children()
		buf = binary.AppendUvarint(buf, uint64(len(children)))
		bw.Write(buf)
		for _, c := range children {
			write(c)
		}
	}
	write(t.Root)
	return bw.Flush()
}

// Load reads a tree written by Save and returns it with its stamp.
func Load(r io.Reader) (*Tree, uint64, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	head := make([]byte, len(magic)+8)
	if _, err := io.ReadFull(br, head); err != nil {
		return nil, 0, err
	}
	if string(head[:len(magic)]) != magic {
		return nil, 0, errCorrupt
	}
	stamp := binary.LittleEndian.Uint64(head[len(magic):])

	var a arena
	l := newLinks()
	var name []byte
	var read func(parent *Node) (*Node, error)
	read = func(parent *Node) (*Node, error) {
		kind, err := br.ReadByte()
		if err != nil || kind > kindLink {
			return nil, errCorrupt
		}
		nameLen, err := binary.ReadUvarint(br)
		if err != nil || nameLen > 1<<16 {
			return nil, errCorrupt
		}
		if uint64(cap(name)) < nameLen {
			name = make([]byte, nameLen)
		}
		name = name[:nameLen]
		if _, err := io.ReadFull(br, name); err != nil {
			return nil, errCorrupt
		}
		entryName := a.str(name)
		if strings.ContainsRune(entryName, 0) {
			return nil, errCorrupt
		}
		if parent == nil {
			if !filepath.IsAbs(entryName) || filepath.Clean(entryName) != entryName {
				return nil, errCorrupt
			}
		} else if !filepath.IsLocal(entryName) || entryName == "." || strings.ContainsRune(entryName, filepath.Separator) {
			return nil, errCorrupt
		}
		n := a.node()
		n.Name, n.Parent, n.IsDir = entryName, parent, kind == kindDir

		v, err := binary.ReadUvarint(br)
		if err != nil {
			return nil, errCorrupt
		}
		switch kind {
		case kindFile:
			n.size.Store(int64(v))
			n.files.Store(1)
			return n, nil
		case kindLink:
			ino, err := binary.ReadUvarint(br)
			if err != nil {
				return nil, errCorrupt
			}
			if l.add(ino, int64(v), n) {
				n.size.Store(int64(v))
			}
			n.files.Store(1)
			return n, nil
		}
		if v > 1<<28 {
			return nil, errCorrupt
		}
		children := a.ptrSlice(int(v))
		var size int64
		var files uint32
		for i := range children {
			c, err := read(n)
			if err != nil {
				return nil, err
			}
			children[i] = c
			size += c.Size()
			files += c.Files()
		}
		if len(children) > 0 {
			n.children.Store(&children)
		}
		n.size.Store(size)
		n.files.Store(files)
		return n, nil
	}
	root, err := read(nil)
	if err != nil {
		return nil, 0, err
	}
	return &Tree{Root: root, links: l}, stamp, nil
}
