package softbody

import (
	"cmp"
	"math"
	"slices"
)

// CirclesAt returns stable IDs of circles whose outer disks contain (x, y),
// including the boundary, in world coordinates. Hits are ordered by distance
// to their centers, then by stable ID. Anchored and dragged circles are included.
// A miss or nonfinite coordinates return nil. The result belongs to the caller.
// This does not change the state. Call on the simulation goroutine.
func (s *State) CirclesAt(x, y float32) []uint64 {
	if !finitePointer(x, y) {
		return nil
	}
	distance := func(i int) float64 {
		return math.Hypot(float64(s.p.x[i])-float64(x), float64(s.p.y[i])-float64(y))
	}
	var ids []uint64
	for i, id := range s.p.id {
		if distance(i) <= float64(s.p.outer[i]) {
			ids = append(ids, id)
		}
	}
	slices.SortFunc(ids, func(a, b uint64) int {
		if order := cmp.Compare(distance(s.bridgeIndex[a]), distance(s.bridgeIndex[b])); order != 0 {
			return order
		}
		return cmp.Compare(a, b)
	})
	return ids
}

// PolygonsAt returns stable IDs of solid polygons containing (x, y), including
// edges and vertices, in world coordinates. Convex and concave polygons in either
// winding order are supported. Hits are ordered by stable ID, independent of
// circle IDs. A miss or nonfinite coordinates return nil. The result belongs to
// the caller. This does not change the state. Call on the simulation goroutine.
func (s *State) PolygonsAt(x, y float32) []uint64 {
	if !finitePointer(x, y) {
		return nil
	}
	var ids []uint64
	for i := range s.polygons {
		p := &s.polygons[i]
		if p.bounds.contains(x, y) && p.contains(x, y) {
			ids = append(ids, p.ID)
		}
	}
	// Polygon storage retains insertion order, which is also stable-ID order.
	return ids
}

func (p *polygon) contains(x, y float32) bool {
	point := Point{x, y}
	inside := false
	for i, a := range p.Points {
		b := p.Points[(i+1)%len(p.Points)]
		if orient(a, b, point) == 0 && x >= min(a.X, b.X) && x <= max(a.X, b.X) && y >= min(a.Y, b.Y) && y <= max(a.Y, b.Y) {
			return true
		}
		ax, ay, bx, by := float64(a.X), float64(a.Y), float64(b.X), float64(b.Y)
		if (ay > float64(y)) != (by > float64(y)) && float64(x) < ax+(float64(y)-ay)*(bx-ax)/(by-ay) {
			inside = !inside
		}
	}
	return inside
}
