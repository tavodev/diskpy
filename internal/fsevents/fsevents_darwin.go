// Package fsevents asks macOS which directories changed since a point in time,
// using the FSEvents journal (which persists across app launches and reboots).
package fsevents

/*
#cgo LDFLAGS: -framework CoreServices
#include <CoreServices/CoreServices.h>
#include <dispatch/dispatch.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
	char **paths;
	uint32_t *flags;
	size_t n, cap;
	int done;
	dispatch_semaphore_t sem;
} collector;

static void on_events(ConstFSEventStreamRef s, void *info, size_t num, void *eventPaths,
		const FSEventStreamEventFlags flags[], const FSEventStreamEventId ids[]) {
	collector *c = info;
	char **paths = eventPaths;
	for (size_t i = 0; i < num; i++) {
		if (c->done) {
			return; // only history is wanted, not live events
		}
		if (flags[i] & kFSEventStreamEventFlagHistoryDone) {
			c->done = 1;
			dispatch_semaphore_signal(c->sem);
			return;
		}
		if (c->n == c->cap) {
			c->cap = c->cap ? c->cap * 2 : 256;
			c->paths = realloc(c->paths, c->cap * sizeof(char *));
			c->flags = realloc(c->flags, c->cap * sizeof(uint32_t));
		}
		c->paths[c->n] = strdup(paths[i]);
		c->flags[c->n] = flags[i];
		c->n++;
	}
}

static void noop(void *ctx) {}

// collect replays the journal for root since the given event id. It returns
// 0 when the whole history was read, -1 on timeout, -2 if the stream failed.
static int collect(const char *root, uint64_t since, double timeout, collector *c) {
	CFStringRef path = CFStringCreateWithCString(NULL, root, kCFStringEncodingUTF8);
	CFArrayRef paths = CFArrayCreate(NULL, (const void **)&path, 1, &kCFTypeArrayCallBacks);
	FSEventStreamContext ctx = {0, c, NULL, NULL, NULL};
	c->sem = dispatch_semaphore_create(0);

	FSEventStreamRef stream = FSEventStreamCreate(NULL, on_events, &ctx, paths, since, 0.0,
		kFSEventStreamCreateFlagNone);
	CFRelease(paths);
	CFRelease(path);
	if (!stream) {
		dispatch_release(c->sem);
		return -2;
	}
	dispatch_queue_t q = dispatch_queue_create("diskpy.fsevents", NULL);
	FSEventStreamSetDispatchQueue(stream, q);

	int rc = -2;
	if (FSEventStreamStart(stream)) {
		long r = dispatch_semaphore_wait(c->sem,
			dispatch_time(DISPATCH_TIME_NOW, (int64_t)(timeout * NSEC_PER_SEC)));
		rc = r == 0 ? 0 : -1;
		FSEventStreamStop(stream);
	}
	FSEventStreamInvalidate(stream);
	FSEventStreamRelease(stream);
	dispatch_sync_f(q, NULL, noop); // drain callbacks before c goes away
	dispatch_release(q);
	dispatch_release(c->sem);
	return rc;
}

static void release(collector *c) {
	for (size_t i = 0; i < c->n; i++) free(c->paths[i]);
	free(c->paths);
	free(c->flags);
}
*/
import "C"

import (
	"errors"
	"strings"
	"time"
	"unsafe"

	"diskpy/internal/scan"
)

// ErrFullRescan means the history can't be trusted and a full scan is needed.
var ErrFullRescan = errors.New("fsevents: history unavailable, full rescan needed")

// Now returns the current event id; changes after it can be asked for later.
func Now() uint64 { return uint64(C.FSEventsGetCurrentEventId()) }

// Since returns the directories under root that changed after event id since.
// root must be a real path (no symlinks), as FSEvents reports real paths.
func Since(root string, since uint64, timeout time.Duration) ([]scan.Change, error) {
	croot := C.CString(root)
	defer C.free(unsafe.Pointer(croot))
	var c C.collector
	defer C.release(&c)

	if rc := C.collect(croot, C.uint64_t(since), C.double(timeout.Seconds()), &c); rc != 0 {
		return nil, ErrFullRescan
	}

	paths := unsafe.Slice(c.paths, c.n)
	flags := unsafe.Slice(c.flags, c.n)
	changes := make([]scan.Change, 0, len(paths))
	for i := range paths {
		f := flags[i]
		if f&(C.kFSEventStreamEventFlagEventIdsWrapped|C.kFSEventStreamEventFlagRootChanged) != 0 {
			return nil, ErrFullRescan
		}
		changes = append(changes, scan.Change{
			Path:      strings.TrimSuffix(C.GoString(paths[i]), "/"),
			Recursive: f&C.kFSEventStreamEventFlagMustScanSubDirs != 0,
		})
	}
	return changes, nil
}
