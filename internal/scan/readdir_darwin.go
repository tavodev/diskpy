package scan

/*
#include <sys/attr.h>
#include <sys/vnode.h>
#include <errno.h>
#include <unistd.h>

// bulk fills buf with packed attributes for the next batch of entries in the
// directory fd. It returns the entry count, 0 at the end, or -errno.
static int bulk(int fd, void *buf, size_t size) {
	struct attrlist al = {0};
	al.bitmapcount = ATTR_BIT_MAP_COUNT;
	al.commonattr = ATTR_CMN_RETURNED_ATTRS | ATTR_CMN_NAME | ATTR_CMN_ERROR |
		ATTR_CMN_OBJTYPE | ATTR_CMN_FILEID;
	al.dirattr = ATTR_DIR_MOUNTSTATUS;
	al.fileattr = ATTR_FILE_LINKCOUNT | ATTR_FILE_ALLOCSIZE;
	int n = getattrlistbulk(fd, &al, buf, size, 0);
	return n < 0 ? -errno : n;
}
*/
import "C"

import (
	"encoding/binary"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const bulkBufSize = 128 << 10

var bufPool = sync.Pool{New: func() any { b := make([]byte, bulkBufSize); return &b }}

var le = binary.LittleEndian

// readDir lists path with getattrlistbulk: one syscall returns names, types and
// allocated sizes for many entries, instead of a readdir plus an lstat per entry.
func (s *scanner) readDir(path string, fn func(entry)) error {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)

	bp := bufPool.Get().(*[]byte)
	defer bufPool.Put(bp)
	buf := *bp

	for {
		n := C.bulk(C.int(fd), unsafe.Pointer(&buf[0]), C.size_t(len(buf)))
		if n < 0 {
			return syscall.Errno(-n)
		}
		if n == 0 {
			return nil
		}
		off := 0
		for range int(n) {
			e, ok := parseEntry(buf[off:])
			if ok {
				fn(e)
			} else {
				s.progress.Errors.Add(1)
			}
			off += int(le.Uint32(buf[off:]))
		}
	}
}

// parseEntry decodes one packed record. Fields appear only when set in the
// returned attribute set, in the order: error, then common, dir, file attrs.
func parseEntry(b []byte) (e entry, ok bool) {
	common := le.Uint32(b[4:])
	dirAttrs := le.Uint32(b[12:])
	fileAttrs := le.Uint32(b[16:])
	p := 4 + 20 // length + attribute_set_t

	if common&C.ATTR_CMN_ERROR != 0 {
		if le.Uint32(b[p:]) != 0 {
			return e, false
		}
		p += 4
	}
	if common&C.ATTR_CMN_NAME != 0 {
		nameOff := int(int32(le.Uint32(b[p:])))
		nameLen := int(le.Uint32(b[p+4:]))
		e.name = b[p+nameOff : p+nameOff+nameLen-1] // drop NUL
		p += 8
	}
	if common&C.ATTR_CMN_OBJTYPE != 0 {
		e.isDir = le.Uint32(b[p:]) == C.VDIR
		p += 4
	}
	if common&C.ATTR_CMN_FILEID != 0 {
		e.ino = le.Uint64(b[p:])
		p += 8
	}
	if dirAttrs&C.ATTR_DIR_MOUNTSTATUS != 0 {
		e.mount = le.Uint32(b[p:])&C.DIR_MNTSTATUS_MNTPOINT != 0
		p += 4
	}
	if fileAttrs&C.ATTR_FILE_LINKCOUNT != 0 {
		e.nlink = le.Uint32(b[p:])
		p += 4
	}
	if fileAttrs&C.ATTR_FILE_ALLOCSIZE != 0 {
		e.size = int64(le.Uint64(b[p:]))
	}
	return e, len(e.name) > 0
}
