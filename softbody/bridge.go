package softbody

import (
	"fmt"
	"math"
	"slices"
)

// AddBridge adds a bridge and returns its stable ID. Multiple bridges may connect
// the same pair; their forces add. Like Step, call it on the simulation goroutine.
func (s *State) AddBridge(b BridgeSpec) (uint64, error) {
	if b.A == b.B || b.A == 0 || b.B == 0 || b.A >= s.nextID || b.B >= s.nextID {
		return 0, fmt.Errorf("bridge endpoints must be distinct existing circle IDs")
	}
	for _, v := range [...]float32{b.MinDistance, b.MaxDistance, b.BreakDistance, b.AttractForce, b.RepelForce} {
		if v < 0 || math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return 0, fmt.Errorf("bridge distances and forces must be finite and nonnegative")
		}
	}
	if b.MinDistance > b.MaxDistance {
		return 0, fmt.Errorf("bridge min distance must be <= max distance")
	}
	if b.BreakDistance != 0 && b.BreakDistance < b.MaxDistance {
		return 0, fmt.Errorf("bridge break distance must be zero or >= max distance")
	}
	s.nextBridgeID++
	id := s.nextBridgeID
	s.bridges = append(s.bridges, BridgeSnapshot{ID: id, BridgeSpec: b})
	return id, nil
}

// RemoveBridge removes an active bridge by ID and reports whether it existed.
// Call it on the simulation goroutine.
func (s *State) RemoveBridge(id uint64) bool {
	for i, b := range s.bridges {
		if b.ID == id {
			s.bridges = slices.Delete(s.bridges, i, i+1)
			return true
		}
	}
	return false
}

// BridgeSnapshot copies active bridges in insertion order, reusing dst's capacity
// when possible. Like Snapshot, it must not run concurrently with mutations.
func (s *State) BridgeSnapshot(dst []BridgeSnapshot) []BridgeSnapshot {
	return append(dst[:0], s.bridges...)
}

func (s *State) solveBridges() {
	if len(s.bridges) == 0 {
		return
	}
	// Circle IDs are dense and never removed. Refresh after each grid sort so
	// bridges remain attached to the same circles as storage order changes.
	s.bridgeIndex = resize(s.bridgeIndex, s.p.len()+1)
	for i, id := range s.p.id {
		s.bridgeIndex[id] = i
	}

	active := s.bridges[:0]
	for _, b := range s.bridges {
		i, j := s.bridgeIndex[b.A], s.bridgeIndex[b.B]
		dx := float64(s.p.x[j]) - float64(s.p.x[i])
		dy := float64(s.p.y[j]) - float64(s.p.y[i])
		d := math.Hypot(dx, dy)
		if b.BreakDistance > 0 && d > float64(b.BreakDistance) {
			continue
		}
		active = append(active, b)

		var force float32
		switch {
		case d < float64(b.MinDistance):
			force = -b.RepelForce
		case d > float64(b.MaxDistance):
			force = b.AttractForce
		default:
			continue
		}
		var nx, ny float32
		if d == 0 {
			// Match the stable direction used by core collisions, independent
			// of the order in which bridge endpoints were supplied.
			nx = 1
			if b.A > b.B {
				nx = -1
			}
		} else {
			nx, ny = float32(dx/d), float32(dy/d)
		}
		fx, fy := nx*force, ny*force
		s.ax[i] += fx * s.p.invMass[i]
		s.ay[i] += fy * s.p.invMass[i]
		s.ax[j] -= fx * s.p.invMass[j]
		s.ay[j] -= fy * s.p.invMass[j]
	}
	clear(s.bridges[len(active):])
	s.bridges = active
}
