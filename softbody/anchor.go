package softbody

import (
	"fmt"
	"slices"
)

// AnchorCircle places a circle at (x, y) and fixes it there until ReleaseCircle.
// It clears velocity and removes the circle from any drag selection. The target
// must be finite, within the world (with its core inside walls), and outside
// polygon cores. Movement is a teleport. Repeating the same anchor is harmless;
// changing its position requires releasing it first. Errors leave state unchanged.
// Call on the simulation goroutine.
func (s *State) AnchorCircle(id uint64, x, y float32) error {
	i, exists := s.bridgeIndex[id]
	if !exists {
		return fmt.Errorf("circle %d does not exist", id)
	}
	if !finitePointer(x, y) {
		return fmt.Errorf("anchor coordinates must be finite")
	}
	if s.isAnchored(i) {
		if s.p.x[i] == x && s.p.y[i] == y {
			return nil
		}
		return fmt.Errorf("circle %d must be released before moving its anchor", id)
	}
	r := float64(s.p.inner[i])
	if s.cfg.BoundaryMode == BoundaryWalls {
		if !s.coreWithinBounds(float64(x), float64(y), r) {
			return fmt.Errorf("anchored circle core must fit the world walls")
		}
	} else if !s.bounds.contains(x, y) {
		return fmt.Errorf("anchor must be within the world bounds")
	}
	for k := range s.polygons {
		if d, _, _ := s.polygons[k].contact(float64(x), float64(y)); d < r {
			return fmt.Errorf("anchored circle core overlaps a polygon")
		}
	}

	invMass := s.p.invMass[i]
	for _, c := range s.dragged {
		if c.id == id {
			invMass = c.invMass
			break
		}
	}
	s.dragged = slices.DeleteFunc(s.dragged, func(c draggedCircle) bool { return c.id == id })
	if s.anchored == nil {
		s.anchored = make(map[uint64]float32)
	}
	s.anchored[id] = invMass
	s.p.invMass[i] = 0
	s.p.x[i], s.p.y[i] = x, y
	s.p.vx[i], s.p.vy[i] = 0, 0
	return nil
}

// ReleaseCircle releases an anchored circle at rest and restores its original
// mass. It reports whether an anchor was released. Call on the simulation
// goroutine; ordinary physics resumes on the next Step.
func (s *State) ReleaseCircle(id uint64) bool {
	invMass, exists := s.anchored[id]
	if !exists {
		return false
	}
	i := s.bridgeIndex[id]
	s.p.invMass[i] = invMass
	s.p.vx[i], s.p.vy[i] = 0, 0
	delete(s.anchored, id)
	return true
}

func (s *State) isAnchored(i int) bool {
	if len(s.anchored) == 0 {
		return false
	}
	_, exists := s.anchored[s.p.id[i]]
	return exists
}
