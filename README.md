# diskpy

Fast disk usage analyzer for macOS with a native GUI (Go + [Fyne](https://fyne.io)).

- Parallel scanner built on `getattrlistbulk`: names, types and allocated sizes for many entries per syscall.
- Squarified treemap plus a size-sorted list; click a folder to drill in.
- Live results while scanning.
- Right-click any entry to reveal it in Finder, open it, or move it to the Trash.
- Hard links are counted once, and stay counted once across incremental updates.
- Scans are cached in `~/Library/Caches/diskpy`; later runs show the cached tree instantly and re-read only the directories FSEvents reports as changed.

## Build and run

Requires Go and the Xcode command line tools (cgo).

```sh
make app            # build/diskpy.app, ad-hoc signed; `make run` also opens it
go build -o diskpy . && ./diskpy [dir]   # or run unbundled; scans dir
```

Run as the app so macOS asks for (and remembers) folder access for diskpy
itself rather than for your terminal. For a complete scan of `$HOME`, grant it
Full Disk Access in System Settings › Privacy & Security. The ad-hoc signature
changes on every build, so macOS may ask again after rebuilding.

## Development

```sh
go test -race ./...
go run ./cmd/bench [-n runs] [dir]          # time full scans without the GUI
go run ./cmd/bench -save tree.bin [dir]     # full scan, then save
go run ./cmd/bench -update tree.bin         # load and apply FSEvents changes
```

## Layout

- `main.go`, `treemap.go`, `cache.go` — GUI, treemap widget, on-disk cache
- `internal/scan` — parallel scanner, incremental rescan, tree persistence
- `internal/fsevents` — FSEvents journal queries
- `internal/finder` — reveal, open and trash files
- `packaging` — `Info.plist` and icon for the app bundle
- `internal/treemap` — squarified layout algorithm
