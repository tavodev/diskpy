# diskpy

diskpy is a macOS disk usage analyzer built with Go and [Fyne](https://fyne.io). It shows which files and folders take up space in an interactive treemap and a size-sorted list.

- Browse results while the parallel scan runs.
- Right-click an entry to show it in Finder, open it, copy its path, or move it to the Trash after confirming.
- Reload previous results from a local cache. When the macOS FSEvents journal is available, diskpy refreshes the directories that changed.

## Build from source

Requirements are macOS 13 Ventura or later, [Go 1.27.4](https://go.dev/dl/) or later, and the Xcode command line tools (`xcode-select --install`).

```sh
git clone https://github.com/tavodev/diskpy.git
cd diskpy
make app
open build/diskpy.app
```

`make app` builds an ad-hoc signed app for the current Mac. macOS may ask for folder access again after a rebuild. To start a scan of a specific directory from the terminal, use `open build/diskpy.app --args /path/to/folder` with diskpy closed.

## Scan a disk or folder

The interface is in Spanish. Choose **Carpeta** to scan a folder or a mounted volume, **Disco** to scan from `/`, and **Escanear** to refresh the selected location. Click a folder to drill down.

diskpy measures allocated file bytes, counts hard-linked data once per scan, and skips symlinks when descending directories. A full scan stays on the selected filesystem. To include protected folders, grant diskpy Full Disk Access in **System Settings → Privacy & Security**. The status line shows how many entries or directories the scan could not read.

Scan results, including file and directory names, are cached locally in `~/Library/Caches/diskpy`. To start fresh after changing disk access permissions, quit diskpy, delete that cache folder, and reopen the app.

## Development

```sh
make test                         # tests with Go's race detector
make test-integration             # also exercises the macOS Trash and FSEvents journal
go run ./cmd/bench -n 3 /path/to/folder
```

The integration tests need access to the local Trash and FSEvents history. For bug reports, include your macOS version, CPU architecture and steps to reproduce. Remove private paths and file names before posting logs or screenshots.

## License

[MIT](LICENSE). Dependencies are available under their respective licenses.
