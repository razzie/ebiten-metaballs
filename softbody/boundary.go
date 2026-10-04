package softbody

import (
	"fmt"
	"math"
	"slices"
)

// SetBoundaryMode changes boundary behavior and immediately applies the new
// policy. Invalid modes or bounds too small for wall confinement leave the state
// unchanged. Like SetBounds, call it on the simulation goroutine.
func (s *State) SetBoundaryMode(mode BoundaryMode) error {
	if mode != BoundaryWalls && mode != BoundaryRemove {
		return fmt.Errorf("unknown boundary mode %d", mode)
	}
	if err := validateBounds(s.bounds, mode, s.maxOuter); err != nil {
		return err
	}
	if mode == s.cfg.BoundaryMode {
		return nil
	}
	s.cfg.BoundaryMode = mode
	if mode == BoundaryRemove {
		s.removeOutsidePolygons()
		s.removeOutsideCircles()
	} else {
		s.confineBridgeParticles()
	}
	return nil
}

func finite(v float32) bool {
	return !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0)
}

func validateBounds(b Bounds, mode BoundaryMode, maxOuter float32) error {
	if !finitePointer(b.MinX, b.MinY) || !finitePointer(b.MaxX, b.MaxY) {
		return fmt.Errorf("bounds must be finite")
	}
	w, h := b.MaxX-b.MinX, b.MaxY-b.MinY
	if !(w > 0 && h > 0) || !finitePointer(w, h) {
		return fmt.Errorf("bounds must have finite positive dimensions")
	}
	if mode == BoundaryWalls && float64(min(w, h)) < 2*float64(maxOuter) {
		return fmt.Errorf("bounds must fit the largest circle in wall mode")
	}
	return nil
}

func (b Bounds) contains(x, y float32) bool {
	return x >= b.MinX && x <= b.MaxX && y >= b.MinY && y <= b.MaxY
}

func (b Bounds) intersects(other Bounds) bool {
	return b.MaxX >= other.MinX && b.MinX <= other.MaxX && b.MaxY >= other.MinY && b.MinY <= other.MaxY
}

// Call only at a solver checkpoint, after workers and pair iterations finish.
// Particle compaction invalidates grid spans; rebuild before querying contacts.
func (s *State) removeOutsideCircles() {
	if s.cfg.BoundaryMode != BoundaryRemove {
		return
	}
	n := 0
	var maxOuter float32
	for i := range s.p.x {
		if !s.bounds.contains(s.p.x[i], s.p.y[i]) {
			continue
		}
		maxOuter = max(maxOuter, s.p.outer[i])
		if n != i {
			copyParticle(&s.p, n, &s.p, i)
		}
		n++
	}
	if n == s.p.len() {
		return
	}
	s.p.resize(n)
	s.indexBridges()
	s.bridges = slices.DeleteFunc(s.bridges, func(b BridgeSnapshot) bool {
		_, a := s.bridgeIndex[b.A]
		_, bExists := s.bridgeIndex[b.B]
		return !a || !bExists
	})
	s.dragged = slices.DeleteFunc(s.dragged, func(c draggedCircle) bool {
		_, exists := s.bridgeIndex[c.id]
		return !exists
	})
	if maxOuter != s.maxOuter {
		s.maxOuter = maxOuter
		s.geometryDirty = true
	}
	// An empty world must not retain active spans pointing into removed storage.
	s.grid.active = s.grid.active[:0]
}

func (s *State) removeOutsidePolygons() {
	if s.cfg.BoundaryMode != BoundaryRemove {
		return
	}
	n := len(s.polygons)
	s.polygons = slices.DeleteFunc(s.polygons, func(p polygon) bool {
		return !s.bounds.intersects(p.bounds)
	})
	if len(s.polygons) != n {
		s.geometryDirty = true
	}
}
