package treemap

import (
	"math"
	"testing"
)

func TestSquarify(t *testing.T) {
	values := []float64{6, 6, 4, 3, 2, 2, 1, 0}
	bounds := Rect{0, 0, 600, 400}
	rects := Squarify(values, bounds)

	if len(rects) != len(values) {
		t.Fatalf("got %d rects, want %d", len(rects), len(values))
	}
	const eps = 1e-6
	var area float64
	for i, r := range rects {
		want := values[i] / 24 * bounds.W * bounds.H
		if got := r.W * r.H; math.Abs(got-want) > eps {
			t.Errorf("rect %d area = %f, want %f", i, got, want)
		}
		if r.X < -eps || r.Y < -eps || r.X+r.W > bounds.W+eps || r.Y+r.H > bounds.H+eps {
			t.Errorf("rect %d %+v out of bounds", i, r)
		}
		area += r.W * r.H
	}
	if math.Abs(area-bounds.W*bounds.H) > eps {
		t.Errorf("total area = %f, want %f", area, bounds.W*bounds.H)
	}
	for i := range rects {
		for j := i + 1; j < len(rects); j++ {
			a, b := rects[i], rects[j]
			if a.X+eps < b.X+b.W && b.X+eps < a.X+a.W && a.Y+eps < b.Y+b.H && b.Y+eps < a.Y+a.H {
				t.Errorf("rects %d and %d overlap: %+v %+v", i, j, a, b)
			}
		}
	}
}

func TestSquarifyEmpty(t *testing.T) {
	for _, r := range Squarify([]float64{0, 0}, Rect{0, 0, 10, 10}) {
		if r.W != 0 || r.H != 0 {
			t.Errorf("expected empty rect, got %+v", r)
		}
	}
}
