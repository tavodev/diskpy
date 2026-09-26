// Package treemap computes squarified treemap layouts (Bruls, Huizing, van Wijk).
package treemap

// Rect is an axis-aligned rectangle.
type Rect struct{ X, Y, W, H float64 }

// Contains reports whether the point lies inside r.
func (r Rect) Contains(x, y float64) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// Squarify splits r into one rectangle per value, with areas proportional to
// the values. Values must be sorted in descending order; the result has the
// same length and order. Zero or negative values get an empty rectangle.
func Squarify(values []float64, r Rect) []Rect {
	out := make([]Rect, len(values))
	var total float64
	for _, v := range values {
		if v > 0 {
			total += v
		}
	}
	if total == 0 || r.W <= 0 || r.H <= 0 {
		return out
	}

	scale := r.W * r.H / total
	var row []int
	var rowSum float64
	for i, v := range values {
		if v <= 0 {
			continue
		}
		a := v * scale
		if len(row) == 0 || worst(row, rowSum+a, a, values, scale, r) <= worst(row, rowSum, 0, values, scale, r) {
			row = append(row, i)
			rowSum += a
			continue
		}
		r = layoutRow(row, rowSum, values, scale, r, out)
		row, rowSum = []int{i}, a
	}
	if len(row) > 0 {
		layoutRow(row, rowSum, values, scale, r, out)
	}
	return out
}

// worst returns the highest aspect ratio in row (plus an optional extra area)
// when laid out along the shorter side of r.
func worst(row []int, sum, extra float64, values []float64, scale float64, r Rect) float64 {
	side := min(r.W, r.H)
	maxA, minA := extra, extra
	if extra == 0 {
		maxA, minA = 0, values[row[0]]*scale
	}
	for _, i := range row {
		a := values[i] * scale
		maxA = max(maxA, a)
		minA = min(minA, a)
	}
	s2, side2 := sum*sum, side*side
	return max(side2*maxA/s2, s2/(side2*minA))
}

// layoutRow places row along the shorter side of r and returns the remaining space.
func layoutRow(row []int, sum float64, values []float64, scale float64, r Rect, out []Rect) Rect {
	if r.W >= r.H {
		w := sum / r.H
		y := r.Y
		for _, i := range row {
			h := values[i] * scale / w
			out[i] = Rect{r.X, y, w, h}
			y += h
		}
		return Rect{r.X + w, r.Y, r.W - w, r.H}
	}
	h := sum / r.W
	x := r.X
	for _, i := range row {
		w := values[i] * scale / h
		out[i] = Rect{x, r.Y, w, h}
		x += w
	}
	return Rect{r.X, r.Y + h, r.W, r.H - h}
}
