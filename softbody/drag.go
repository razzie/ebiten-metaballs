package softbody

import (
	"cmp"
	"math"
	"slices"
)

type draggedCircle struct {
	id               uint64
	offsetX, offsetY float64
	invMass          float32
}

// Drag replaces the current selection and returns the selected stable circle IDs.
// Selection is tested only here, never during Carry or Step. Grab offsets are
// preserved so circles do not snap to the pointer. Nonfinite coordinates are
// ignored (including leaving an existing selection intact).
// Like Carry and Drop, call Drag on the simulation goroutine.
func (s *State) Drag(spec DragSpec) []uint64 {
	if !finitePointer(spec.X, spec.Y) {
		return nil
	}
	s.Drop()
	s.dragX, s.dragY = spec.X, spec.Y
	for i, id := range s.p.id {
		if len(spec.Groups) > 0 && !slices.Contains(spec.Groups, s.p.group[i]) {
			continue
		}
		dx, dy := float64(s.p.x[i])-float64(spec.X), float64(s.p.y[i])-float64(spec.Y)
		if math.Hypot(dx, dy) <= float64(s.p.outer[i]) {
			s.dragged = append(s.dragged, draggedCircle{id: id, offsetX: dx, offsetY: dy, invMass: s.p.invMass[i]})
		}
	}
	slices.SortFunc(s.dragged, func(a, b draggedCircle) int {
		if order := cmp.Compare(math.Hypot(a.offsetX, a.offsetY), math.Hypot(b.offsetX, b.offsetY)); order != 0 {
			return order
		}
		return cmp.Compare(a.id, b.id)
	})
	if spec.MaxCircles > 0 && len(s.dragged) > spec.MaxCircles {
		s.dragged = s.dragged[:spec.MaxCircles]
	}
	if len(s.dragged) == 0 {
		return nil
	}
	s.indexBridges()
	ids := make([]uint64, len(s.dragged))
	for k, c := range s.dragged {
		i := s.bridgeIndex[c.id]
		ids[k] = c.id
		// Zero inverse mass makes held circles kinematic in all solvers.
		s.p.invMass[i] = 0
		s.p.vx[i], s.p.vy[i] = 0, 0
	}
	return ids
}

// Carry registers the latest pointer position in world coordinates. The next
// positive Step moves the selection there over its substeps, preserving grab
// offsets and clamping each core to the world bounds and polygon obstacles.
// Held circles push other circles and pull bridge neighbors but cannot be moved
// by physics themselves.
// It is harmless without a selection; nonfinite coordinates are ignored.
func (s *State) Carry(x, y float32) {
	if len(s.dragged) > 0 && finitePointer(x, y) {
		s.dragX, s.dragY = x, y
	}
}

// Drop releases the selection at rest, restoring its original masses. Any Carry
// position pending since the last Step is applied before releasing. Repeated
// calls are harmless. Bridge forces and constraints resume on the next Step.
func (s *State) Drop() {
	if len(s.dragged) == 0 {
		return
	}
	s.indexBridges()
	for _, c := range s.dragged {
		i := s.bridgeIndex[c.id]
		x, y := s.dragTarget(c, i)
		s.movePolygonCore(i, x, y)
		s.p.vx[i], s.p.vy[i] = 0, 0
		s.p.invMass[i] = c.invMass
	}
	s.dragged = s.dragged[:0]
}

func finitePointer(x, y float32) bool {
	return !math.IsNaN(float64(x)) && !math.IsNaN(float64(y)) && !math.IsInf(float64(x), 0) && !math.IsInf(float64(y), 0)
}

func (s *State) dragTarget(c draggedCircle, i int) (float32, float32) {
	r := s.p.inner[i]
	return float32(min(max(float64(s.dragX)+c.offsetX, float64(s.bounds.MinX+r)), float64(s.bounds.MaxX-r))),
		float32(min(max(float64(s.dragY)+c.offsetY, float64(s.bounds.MinY+r)), float64(s.bounds.MaxY-r)))
}

func (s *State) carrySubstep(dt float32, remaining int) {
	if len(s.dragged) == 0 {
		return
	}
	// The grid may have reordered circles during the previous substep or
	// constraint pass. Resolve stable IDs again before touching the selection.
	s.indexBridges()
	for _, c := range s.dragged {
		i := s.bridgeIndex[c.id]
		x, y := s.dragTarget(c, i)
		x = s.p.x[i] + (x-s.p.x[i])/float32(remaining)
		y = s.p.y[i] + (y-s.p.y[i])/float32(remaining)
		oldX, oldY := s.p.x[i], s.p.y[i]
		s.movePolygonCore(i, x, y)
		s.p.vx[i], s.p.vy[i] = (s.p.x[i]-oldX)/dt, (s.p.y[i]-oldY)/dt
	}
}
