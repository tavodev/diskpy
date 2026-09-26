package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"diskpy/internal/fsevents"
	"diskpy/internal/scan"
)

type ui struct {
	win      fyne.Window
	root     string
	current  *scan.Node
	items    []*scan.Node // current's children, largest first, as last refreshed
	total    int64        // current's size at the last refresh
	cancel   context.CancelFunc
	scanning bool
	rootNode *scan.Node // last complete tree for root
	stamp    uint64     // FSEvents id rootNode is current as of

	pathLabel *widget.Label
	status    *widget.Label
	list      *widget.List
	back      *widget.Button
	tree      *Treemap
	summary   string
}

func main() {
	a := app.NewWithID("dev.tavo.diskpy")
	w := a.NewWindow("diskpy")
	w.Resize(fyne.NewSize(900, 650))

	home, _ := os.UserHomeDir()
	u := &ui{win: w, root: home}
	w.SetContent(u.build())
	u.showPath()
	if len(os.Args) > 1 {
		u.root = os.Args[1]
	}
	if len(os.Args) > 1 || hasCache(u.root) {
		u.startScan()
	}
	w.ShowAndRun()
}

func (u *ui) build() fyne.CanvasObject {
	u.pathLabel = widget.NewLabel("")
	u.pathLabel.Truncation = fyne.TextTruncateEllipsis
	u.status = widget.NewLabel("Elige una carpeta y pulsa Escanear")

	u.back = widget.NewButtonWithIcon("", theme.NavigateBackIcon(), u.goUp)
	u.back.Disable()
	pick := widget.NewButtonWithIcon("Carpeta", theme.FolderOpenIcon(), u.pickFolder)
	run := widget.NewButtonWithIcon("Escanear", theme.ViewRefreshIcon(), u.startScan)
	run.Importance = widget.HighImportance

	u.list = widget.NewList(u.rowCount, newRow, u.updateRow)
	u.list.OnSelected = u.open

	u.tree = NewTreemap()
	u.tree.OnOpen = u.navigate
	u.tree.OnHover = u.hover

	split := container.NewHSplit(u.list, u.tree)
	split.Offset = 0.35

	top := container.NewBorder(nil, nil, u.back, container.NewHBox(pick, run), u.pathLabel)
	return container.NewBorder(top, u.status, nil, nil, split)
}

func (u *ui) showPath() {
	if u.current != nil {
		u.pathLabel.SetText(u.current.Path())
	} else {
		u.pathLabel.SetText(u.root)
	}
}

func (u *ui) pickFolder() {
	dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
		if err != nil || uri == nil {
			return
		}
		u.root = uri.Path()
		u.startScan()
	}, u.win)
}

// maxChanges is how many changed directories an incremental update handles
// before a full scan is likely faster.
const maxChanges = 50_000

// startScan shows the most recent tree for u.root at once, from memory or the
// on-disk cache, and brings it up to date with only the directories FSEvents
// reports as changed. Without a usable tree or history it does a full scan.
func (u *ui) startScan() {
	if u.cancel != nil {
		u.cancel()
	}
	root, err := filepath.EvalSymlinks(u.root) // FSEvents reports real paths
	if err == nil {
		root, err = filepath.Abs(root)
	}
	if err != nil {
		u.status.SetText("Error: " + err.Error())
		return
	}
	u.root = root

	// A tree is owned by the run updating it; a cancelled run may still be
	// touching its tree, so only a finished one is reused.
	tree, stamp := u.rootNode, u.stamp
	if tree == nil || tree.Name != root || u.scanning {
		tree = nil
		u.clearView()
	}
	u.rootNode = nil
	u.scanning = true
	u.summary = ""

	ctx, cancel := context.WithCancel(context.Background())
	u.cancel = cancel
	var p scan.Progress
	var verb atomic.Pointer[string]
	setVerb := func(v string) { verb.Store(&v) }
	setVerb("Actualizando")
	start := time.Now()
	done := make(chan struct{})

	go func() {
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for tick := 1; ; tick++ {
			select {
			case <-done:
				return
			case <-t.C:
				fyne.Do(func() {
					if ctx.Err() != nil {
						return
					}
					u.status.SetText(fmt.Sprintf("%s… %d archivos · %s · %.1fs",
						*verb.Load(), p.Files.Load(), humanSize(p.Bytes.Load()), time.Since(start).Seconds()))
					if tick > 1 && tick%5 != 0 { // partial results right away, then every 500ms
						return
					}
					if u.current == nil {
						if root := p.Root.Load(); root != nil {
							u.navigate(root)
						}
					} else {
						u.refreshView()
					}
				})
			}
		}
	}()

	go func() {
		defer close(done)
		tree, stamp := tree, stamp // owned by this goroutine from here on
		var err error
		if tree == nil {
			if tree, stamp, err = loadCache(root); err == nil {
				cached := tree
				fyne.Do(func() {
					if ctx.Err() == nil {
						u.navigate(cached)
					}
				})
			}
		}

		id := fsevents.Now()
		var changes []scan.Change
		if tree != nil {
			changes, err = fsevents.Since(root, stamp, 10*time.Second)
			if err != nil || len(changes) > maxChanges {
				tree = nil
			}
		}
		if tree != nil {
			err = scan.Rescan(ctx, tree, changes, &p)
		} else {
			setVerb("Escaneando")
			fyne.Do(func() {
				if ctx.Err() == nil {
					u.clearView() // drop the stale cached view; the new tree shows up live
				}
			})
			tree, err = scan.Scan(ctx, root, &p)
			changes = nil
		}
		if ctx.Err() != nil {
			return // superseded by a newer run
		}
		elapsed := time.Since(start)
		if err == nil {
			saveCache(tree, id)
		}
		// Hand back scan garbage and any previous tree to the OS.
		defer debug.FreeOSMemory()
		fyne.Do(func() {
			u.scanning = false
			if err != nil {
				u.status.SetText("Error: " + err.Error())
				return
			}
			u.rootNode, u.stamp = tree, id
			how := fmt.Sprintf("escaneo completo en %.2fs", elapsed.Seconds())
			if changes != nil {
				how = fmt.Sprintf("actualizado en %.2fs (%d carpetas cambiadas)", elapsed.Seconds(), len(changes))
			}
			u.summary = fmt.Sprintf("%d archivos · %s · %s · %d sin acceso",
				tree.Files(), humanSize(tree.Size()), how, p.Errors.Load())
			u.status.SetText(u.summary)
			if u.current == nil {
				u.navigate(tree)
			} else {
				u.refreshView() // keep wherever the user browsed to
			}
		})
	}()
}

func (u *ui) clearView() {
	u.current, u.items, u.total = nil, nil, 0
	u.back.Disable()
	u.list.UnselectAll()
	u.list.Refresh()
	u.tree.SetNode(nil)
	u.showPath()
}

func (u *ui) open(id widget.ListItemID) {
	u.list.UnselectAll()
	n := u.items[id]
	if !n.IsDir || (!u.scanning && len(n.Children()) == 0) {
		return
	}
	u.navigate(n)
}

func (u *ui) goUp() {
	if u.current != nil && u.current.Parent != nil {
		u.navigate(u.current.Parent)
	}
}

func (u *ui) navigate(n *scan.Node) {
	u.current = n
	if n.Parent != nil {
		u.back.Enable()
	} else {
		u.back.Disable()
	}
	u.showPath()
	u.refreshView()
	u.list.ScrollToTop()
}

// refreshView re-reads the current node, which grows while a scan runs.
func (u *ui) refreshView() {
	u.items = u.current.Sorted()
	u.total = u.current.Size()
	u.list.Refresh()
	u.tree.SetNode(u.current)
}

func (u *ui) hover(n *scan.Node) {
	if n == nil {
		u.status.SetText(u.summary)
		return
	}
	u.status.SetText(fmt.Sprintf("%s · %s · %d archivos", n.Path(), humanSize(n.Size()), n.Files()))
}

func (u *ui) rowCount() int {
	return len(u.items)
}

func newRow() fyne.CanvasObject {
	bar := widget.NewProgressBar()
	bar.TextFormatter = func() string { return "" }
	size := widget.NewLabel("")
	size.Alignment = fyne.TextAlignTrailing
	name := widget.NewLabel("")
	name.Truncation = fyne.TextTruncateEllipsis
	right := container.NewHBox(container.NewGridWrap(fyne.NewSize(90, 30), size), container.NewGridWrap(fyne.NewSize(140, 30), bar))
	return container.NewBorder(nil, nil, widget.NewIcon(nil), right, name)
}

func (u *ui) updateRow(id widget.ListItemID, o fyne.CanvasObject) {
	n := u.items[id]
	row := o.(*fyne.Container)
	name := row.Objects[0].(*widget.Label)
	icon := row.Objects[1].(*widget.Icon)
	right := row.Objects[2].(*fyne.Container)
	size := right.Objects[0].(*fyne.Container).Objects[0].(*widget.Label)
	bar := right.Objects[1].(*fyne.Container).Objects[0].(*widget.ProgressBar)

	name.SetText(n.Name)
	if n.IsDir {
		icon.SetResource(theme.FolderIcon())
	} else {
		icon.SetResource(theme.FileIcon())
	}
	size.SetText(humanSize(n.Size()))
	if u.total > 0 {
		bar.SetValue(min(1, float64(n.Size())/float64(u.total)))
	} else {
		bar.SetValue(0)
	}
}

func humanSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
