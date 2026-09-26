// Package finder does to files what Finder does: reveal, open and trash them.
package finder

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>
#include <stdlib.h>
#include <string.h>

// trash moves path to the Trash and returns 0 with *out set to where it went,
// or -1 with *out set to an error message. The caller frees *out.
static int trash(const char *path, char **out) {
	@autoreleasepool {
		NSFileManager *fm = [NSFileManager defaultManager];
		NSString *p = [fm stringWithFileSystemRepresentation:path length:strlen(path)];
		NSURL *dest = nil;
		NSError *err = nil;
		if ([fm trashItemAtURL:[NSURL fileURLWithPath:p] resultingItemURL:&dest error:&err]) {
			*out = strdup(dest.fileSystemRepresentation);
			return 0;
		}
		*out = strdup(err.localizedDescription.UTF8String);
		return -1;
	}
}
*/
import "C"

import (
	"errors"
	"os/exec"
	"unsafe"
)

// Trash moves path to the Trash, where it can still be put back from.
func Trash(path string) error {
	_, err := trash(path)
	return err
}

// trash returns where in the Trash path ended up.
func trash(path string) (string, error) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	var out *C.char
	rc := C.trash(cpath, &out)
	defer C.free(unsafe.Pointer(out))
	if rc != 0 {
		return "", errors.New(C.GoString(out))
	}
	return C.GoString(out), nil
}

// Reveal opens a Finder window with path selected.
func Reveal(path string) error { return exec.Command("open", "-R", path).Run() }

// Open opens path with its default application.
func Open(path string) error { return exec.Command("open", path).Run() }
