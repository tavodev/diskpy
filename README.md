# diskpy

Fast disk usage analyzer for macOS with a native GUI (Go + [Fyne](https://fyne.io)).

- Parallel scanner built on `getattrlistbulk`: names, types and allocated sizes for many entries per syscall.
- Squarified treemap plus a size-sorted list; click a folder to drill in.
- Live results while scanning.
- Scans are cached in `~/Library/Caches/diskpy`; later runs show the cached tree instantly and re-read only the directories FSEvents reports as changed.

## Build and run

Requires Go and the Xcode command line tools (cgo).

```sh
go build -o diskpy .
./diskpy [dir]      # scans dir, or $HOME
```

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
- `internal/treemap` — squarified layout algorithm
