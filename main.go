package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"diskpy/internal/finder"
	"diskpy/internal/fsevents"
	"diskpy/internal/scan"
)

//go:embed packaging/Icon.png
var icon []byte

// lastRootKey is the preference holding the last folder scanned.
const lastRootKey = "root"

type ui struct {
	win      fyne.Window
	root     string
	current  *scan.Node
	items    []*scan.Node // current's children, largest first, as last refreshed
	total    int64        // current's size at the last refresh
	cancel   context.CancelFunc
	scanning bool
	rootNode *scan.Tree // last complete tree for root
	stamp    uint64     // FSEvents id rootNode is current as of

	pathLabel *widget.Label
	status    *widget.Label
	activity  *widget.ProgressBarInfinite // shown while scanning
	list      *widget.List
	back      *widget.Button
	tree      *Treemap
	summary   string
}

func main() {
	a := app.NewWithID("dev.tavo.diskpy")
	a.SetIcon(fyne.NewStaticResource("Icon.png", icon))
	w := a.NewWindow("diskpy")
	w.Resize(fyne.NewSize(900, 650))

	root, _ := os.UserHomeDir()
	if last := a.Preferences().String(lastRootKey); last != "" {
		if _, err := os.Stat(last); err == nil {
			root = last
		}
	}
	u := &ui{win: w, root: root}
	w.SetContent(u.build())
	// Finder used to pass -psn_… to apps it launched.
	arg := len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-psn")
	if arg {
		u.root = os.Args[1]
	}
	u.showPath()
	if arg || hasCache(u.root) {
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
	copyPath := widget.NewButtonWithIcon("", theme.ContentCopyIcon(), func() { u.copyPath(u.pathLabel.Text) })
	disk := widget.NewButtonWithIcon("Disco", theme.StorageIcon(), func() { u.scanDir("/") })
	pick := widget.NewButtonWithIcon("Carpeta", theme.FolderOpenIcon(), u.pickFolder)
	run := widget.NewButtonWithIcon("Escanear", theme.ViewRefreshIcon(), u.startScan)
	run.Importance = widget.HighImportance

	u.list = widget.NewList(u.rowCount, func() fyne.CanvasObject { return newRow(u.open, u.showMenu) }, u.updateRow)

	u.tree = NewTreemap()
	u.tree.OnOpen = u.navigate
	u.tree.OnHover = u.hover
	u.tree.OnMenu = u.showMenu

	split := container.NewHSplit(u.list, u.tree)
	split.Offset = 0.35

	top := container.NewBorder(nil, nil, u.back, container.NewHBox(copyPath, disk, pick, run), u.pathLabel)
	u.activity = widget.NewProgressBarInfinite()
	u.activity.Stop()
	u.activity.Hide()
	bottom := container.NewBorder(nil, nil, nil, container.NewCenter(container.NewGridWrap(fyne.NewSize(120, 6), u.activity)), u.status)
	return container.NewBorder(top, bottom, nil, nil, split)
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
		u.scanDir(uri.Path())
	}, u.win)
}

func (u *ui) scanDir(dir string) {
	u.root = dir
	u.startScan()
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
	fyne.CurrentApp().Preferences().SetString(lastRootKey, root)

	// A tree is owned by the run updating it; a cancelled run may still be
	// touching its tree, so only a finished one is reused.
	tree, stamp := u.rootNode, u.stamp
	if tree == nil || tree.Root.Name != root || u.scanning {
		tree = nil
		u.clearView()
	}
	u.rootNode = nil
	u.setScanning(true)
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
				cached := tree.Root
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
			u.setScanning(false)
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
				tree.Root.Files(), humanSize(tree.Root.Size()), how, p.Errors.Load())
			u.status.SetText(u.summary)
			if u.current == nil {
				u.navigate(tree.Root)
			} else {
				u.refreshView() // keep wherever the user browsed to
			}
		})
	}()
}

func (u *ui) setScanning(on bool) {
	u.scanning = on
	if on {
		u.activity.Show()
		u.activity.Start()
	} else {
		u.activity.Stop()
		u.activity.Hide()
	}
}

func (u *ui) clearView() {
	u.current, u.items, u.total = nil, nil, 0
	u.back.Disable()
	u.list.UnselectAll()
	u.list.Refresh()
	u.tree.SetNode(nil)
	u.showPath()
}

func (u *ui) open(n *scan.Node) {
	if !n.IsDir || (!u.scanning && len(n.Children()) == 0) {
		return
	}
	u.navigate(n)
}

// goUp shows the parent folder. Above the scanned folder, it scans the parent.
func (u *ui) goUp() {
	switch {
	case u.current == nil:
	case u.current.Parent != nil:
		u.navigate(u.current.Parent)
	case u.current.Name != "/":
		u.scanDir(filepath.Dir(u.current.Name))
	}
}

func (u *ui) navigate(n *scan.Node) {
	u.current = n
	if n.Parent != nil || n.Name != "/" {
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

// showMenu offers what can be done with n, at pos on the window's canvas.
func (u *ui) showMenu(n *scan.Node, pos fyne.Position) {
	path := n.Path()
	report := func(err error) {
		if err != nil {
			dialog.ShowError(err, u.win)
		}
	}
	reveal := fyne.NewMenuItem("Mostrar en Finder", func() { report(finder.Reveal(path)) })
	reveal.Icon = theme.SearchIcon()
	open := fyne.NewMenuItem("Abrir", func() { report(finder.Open(path)) })
	open.Icon = theme.FileApplicationIcon()
	copyPath := fyne.NewMenuItem("Copiar ruta", func() { u.copyPath(path) })
	copyPath.Icon = theme.ContentCopyIcon()
	trash := fyne.NewMenuItem("Mover a la Papelera…", func() { u.confirmTrash(n) })
	trash.Icon = theme.DeleteIcon()
	// While a scan runs the tree belongs to it; and the root has no parent to
	// be removed from.
	trash.Disabled = u.scanning || u.rootNode == nil || n.Parent == nil
	menu := fyne.NewMenu("", reveal, open, copyPath, fyne.NewMenuItemSeparator(), trash)
	widget.ShowPopUpMenuAtPosition(menu, u.win.Canvas(), pos)
}

func (u *ui) copyPath(path string) {
	fyne.CurrentApp().Clipboard().SetContent(path)
	u.status.SetText("Ruta copiada: " + path)
}

func (u *ui) confirmTrash(n *scan.Node) {
	kind := "el archivo"
	if n.IsDir {
		kind = fmt.Sprintf("la carpeta (%d archivos)", n.Files())
	}
	msg := fmt.Sprintf("Se moverá %s «%s» a la Papelera.\nOcupa %s.", kind, n.Name, humanSize(n.Size()))
	dialog.ShowConfirm("¿Mover a la Papelera?", msg, func(ok bool) {
		if !ok {
			return
		}
		tree := u.rootNode
		if u.scanning || tree == nil || !inTree(n, tree) {
			dialog.ShowInformation("No se movió nada", "El escaneo cambió mientras confirmabas. Inténtalo de nuevo cuando termine.", u.win)
			return
		}
		if err := finder.Trash(n.Path()); err != nil {
			dialog.ShowError(err, u.win)
			return
		}
		size := n.Size()
		tree.Remove(n)
		u.refreshView()
		u.summary = fmt.Sprintf("«%s» movido a la Papelera (%s) · total %s",
			n.Name, humanSize(size), humanSize(tree.Root.Size()))
		u.status.SetText(u.summary)
	}, u.win)
}

// inTree reports whether n is still part of t.
func inTree(n *scan.Node, t *scan.Tree) bool {
	for n.Parent != nil {
		if !slices.Contains(n.Parent.Children(), n) {
			return false
		}
		n = n.Parent
	}
	return n == t.Root
}

func (u *ui) rowCount() int {
	return len(u.items)
}

// row is a list entry that opens its node on click and offers a context menu
// on right click. Being tappable itself, it receives both kinds of click, so
// the list's own selection never sees them.
type row struct {
	widget.BaseWidget
	content *fyne.Container
	node    *scan.Node
	onOpen  func(*scan.Node)
	onMenu  func(*scan.Node, fyne.Position)
}

func (r *row) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(r.content)
}

func (r *row) Tapped(*fyne.PointEvent) {
	if r.node != nil {
		r.onOpen(r.node)
	}
}

func (r *row) TappedSecondary(ev *fyne.PointEvent) {
	if r.node != nil {
		r.onMenu(r.node, ev.AbsolutePosition)
	}
}

func newRow(onOpen func(*scan.Node), onMenu func(*scan.Node, fyne.Position)) fyne.CanvasObject {
	bar := widget.NewProgressBar()
	bar.TextFormatter = func() string { return "" }
	size := widget.NewLabel("")
	size.Alignment = fyne.TextAlignTrailing
	name := widget.NewLabel("")
	name.Truncation = fyne.TextTruncateEllipsis
	right := container.NewHBox(container.NewGridWrap(fyne.NewSize(90, 30), size), container.NewGridWrap(fyne.NewSize(140, 30), bar))
	r := &row{content: container.NewBorder(nil, nil, widget.NewIcon(nil), right, name), onOpen: onOpen, onMenu: onMenu}
	r.ExtendBaseWidget(r)
	return r
}

func (u *ui) updateRow(id widget.ListItemID, o fyne.CanvasObject) {
	n := u.items[id]
	r := o.(*row)
	r.node = n
	name := r.content.Objects[0].(*widget.Label)
	icon := r.content.Objects[1].(*widget.Icon)
	right := r.content.Objects[2].(*fyne.Container)
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
