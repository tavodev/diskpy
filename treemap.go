package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/tavodev/diskpy/internal/scan"
	"github.com/tavodev/diskpy/internal/treemap"
)

const (
	maxObjects   = 4000
	labelSize    = 11
	labelPad     = 3
	headerHeight = labelSize + 2*labelPad + 2 // must fit a label, see fitLabel
	nestMinSide  = 48
)

var palette = []color.NRGBA{
	{0x4e, 0x79, 0xa7, 0xff}, {0xf2, 0x8e, 0x2b, 0xff}, {0xe1, 0x57, 0x59, 0xff},
	{0x76, 0xb7, 0xb2, 0xff}, {0x59, 0xa1, 0x4f, 0xff}, {0xed, 0xc9, 0x48, 0xff},
	{0xb0, 0x7a, 0xa1, 0xff}, {0xff, 0x9d, 0xa7, 0xff}, {0x9c, 0x75, 0x5f, 0xff},
	{0x86, 0xbc, 0xb6, 0xff},
}

var fileColor = color.NRGBA{0x8a, 0x8f, 0x98, 0xff}

type hit struct {
	rect treemap.Rect
	node *scan.Node
}

// Treemap draws the children of a node as nested, size-proportional rectangles.
type Treemap struct {
	widget.BaseWidget

	OnOpen  func(*scan.Node)                // a directory was clicked
	OnHover func(*scan.Node)                // nil when the pointer leaves
	OnMenu  func(*scan.Node, fyne.Position) // right click, at a canvas position

	node *scan.Node
	hits []hit // painted order: parents before their children
}

func NewTreemap() *Treemap {
	t := &Treemap{}
	t.ExtendBaseWidget(t)
	return t
}

func (t *Treemap) SetNode(n *scan.Node) {
	t.node = n
	t.Refresh()
}

func (t *Treemap) CreateRenderer() fyne.WidgetRenderer {
	return &treemapRenderer{t: t}
}

func (t *Treemap) Tapped(ev *fyne.PointEvent) {
	n := t.nodeAt(ev.Position)
	for n != nil && n != t.node && !n.IsDir {
		n = n.Parent
	}
	if n != nil && n != t.node && t.OnOpen != nil {
		t.OnOpen(n)
	}
}

func (t *Treemap) TappedSecondary(ev *fyne.PointEvent) {
	if n := t.nodeAt(ev.Position); n != nil && t.OnMenu != nil {
		t.OnMenu(n, ev.AbsolutePosition)
	}
}

func (t *Treemap) MouseIn(ev *desktop.MouseEvent) { t.MouseMoved(ev) }

func (t *Treemap) MouseMoved(ev *desktop.MouseEvent) {
	if t.OnHover != nil {
		t.OnHover(t.nodeAt(ev.Position))
	}
}

func (t *Treemap) MouseOut() {
	if t.OnHover != nil {
		t.OnHover(nil)
	}
}

// nodeAt returns the deepest node painted under p.
func (t *Treemap) nodeAt(p fyne.Position) *scan.Node {
	for i := len(t.hits) - 1; i >= 0; i-- {
		if t.hits[i].rect.Contains(float64(p.X), float64(p.Y)) {
			return t.hits[i].node
		}
	}
	return nil
}

func (t *Treemap) build(size fyne.Size) []fyne.CanvasObject {
	bg := canvas.NewRectangle(theme.Color(theme.ColorNameBackground))
	bg.Resize(size)
	objs := []fyne.CanvasObject{bg}
	t.hits = t.hits[:0]
	if t.node == nil {
		return objs
	}

	border := theme.Color(theme.ColorNameBackground)
	addRect := func(r treemap.Rect, fill color.Color, n *scan.Node) {
		rect := canvas.NewRectangle(fill)
		rect.StrokeColor = border
		rect.StrokeWidth = 1
		rect.Move(fyne.NewPos(float32(r.X), float32(r.Y)))
		rect.Resize(fyne.NewSize(float32(r.W), float32(r.H)))
		objs = append(objs, rect)
		t.hits = append(t.hits, hit{r, n})
	}
	addLabel := func(r treemap.Rect, text string, style fyne.TextStyle) {
		if lbl := fitLabel(text, r, style); lbl != nil {
			objs = append(objs, lbl)
		}
	}
	bold := fyne.TextStyle{Bold: true}

	bounds := treemap.Rect{W: float64(size.Width), H: float64(size.Height)}
	children := t.node.Sorted()
	for i, r := range squarify(children, bounds) {
		if len(objs) >= maxObjects {
			break
		}
		if r.W < 1 || r.H < 1 {
			continue
		}
		c := children[i]
		base := fileColor
		if c.IsDir {
			base = palette[i%len(palette)]
		}
		addRect(r, base, c)

		label := c.Name + "  " + humanSize(c.Size())
		if !c.IsDir || r.W < nestMinSide || r.H < nestMinSide {
			addLabel(r, label, bold)
			continue
		}
		grand := c.Sorted()
		if len(grand) == 0 {
			addLabel(r, label, bold)
			continue
		}
		addLabel(treemap.Rect{X: r.X, Y: r.Y, W: r.W, H: headerHeight}, label, bold)
		inner := treemap.Rect{X: r.X + 2, Y: r.Y + headerHeight, W: r.W - 4, H: r.H - headerHeight - 2}
		for j, cr := range squarify(grand, inner) {
			if len(objs) >= maxObjects {
				break
			}
			if cr.W < 2 || cr.H < 2 {
				continue
			}
			gc := grand[j]
			fill := lighten(base, 0.25+0.15*float64(j%3))
			if !gc.IsDir {
				fill = lighten(base, 0.6)
			}
			addRect(cr, fill, gc)
			addLabel(cr, gc.Name, fyne.TextStyle{})
		}
	}
	return objs
}

func squarify(nodes []*scan.Node, r treemap.Rect) []treemap.Rect {
	values := make([]float64, len(nodes))
	for i, n := range nodes {
		values[i] = float64(n.Size())
	}
	return treemap.Squarify(values, r)
}

// fitLabel returns a text object that fits inside r, or nil if none does.
// The text is cut with an ellipsis, finding the longest prefix by binary search.
func fitLabel(text string, r treemap.Rect, style fyne.TextStyle) *canvas.Text {
	const pad = labelPad
	if r.H < labelSize+2*pad || r.W < 30 {
		return nil
	}
	avail := float32(r.W) - 2*pad
	fits := func(s string) bool {
		return fyne.MeasureText(s, labelSize, style).Width <= avail
	}
	if !fits(text) {
		runes := []rune(text)
		lo, hi := 0, len(runes) // longest fitting prefix is in [lo, hi)
		for lo+1 < hi {
			mid := (lo + hi) / 2
			if fits(string(runes[:mid]) + "…") {
				lo = mid
			} else {
				hi = mid
			}
		}
		if lo == 0 {
			return nil
		}
		text = string(runes[:lo]) + "…"
	}
	lbl := canvas.NewText(text, color.NRGBA{0x10, 0x10, 0x10, 0xff})
	lbl.TextSize = labelSize
	lbl.TextStyle = style
	lbl.Move(fyne.NewPos(float32(r.X)+pad, float32(r.Y)+1))
	return lbl
}

func lighten(c color.NRGBA, f float64) color.NRGBA {
	mix := func(v uint8) uint8 { return uint8(float64(v) + (255-float64(v))*f) }
	return color.NRGBA{mix(c.R), mix(c.G), mix(c.B), c.A}
}

type treemapRenderer struct {
	t    *Treemap
	objs []fyne.CanvasObject

	// what objs was built for; Layout is called often with no change
	builtNode *scan.Node
	builtSize fyne.Size
}

func (r *treemapRenderer) rebuild(size fyne.Size, force bool) {
	if !force && r.objs != nil && r.builtNode == r.t.node && r.builtSize == size {
		return
	}
	r.objs = r.t.build(size)
	r.builtNode, r.builtSize = r.t.node, size
}

func (r *treemapRenderer) Layout(size fyne.Size) { r.rebuild(size, false) }
func (r *treemapRenderer) MinSize() fyne.Size    { return fyne.NewSize(200, 200) }
func (r *treemapRenderer) Objects() []fyne.CanvasObject {
	return r.objs
}
func (r *treemapRenderer) Destroy() {}

// Refresh always rebuilds, so theme changes are picked up.
func (r *treemapRenderer) Refresh() {
	r.rebuild(r.t.Size(), true)
	canvas.Refresh(r.t)
}
