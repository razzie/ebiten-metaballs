package softbody

import (
	"fmt"
	"math"
	"slices"
)

// AddBridge adds a bridge and returns its stable ID. Multiple bridges may connect
// the same pair; spring forces add, while constraints are solved together.
// Like Step, call it on the simulation goroutine.
func (s *State) AddBridge(b BridgeSpec) (uint64, error) {
	if b.A == b.B || b.A == 0 || b.B == 0 || b.A >= s.nextID || b.B >= s.nextID {
		return 0, fmt.Errorf("bridge endpoints must be distinct existing circle IDs")
	}
	for _, v := range [...]float32{b.MinDistance, b.MaxDistance, b.BreakDistance, b.AttractForce, b.RepelForce, b.Damping} {
		if v < 0 || math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return 0, fmt.Errorf("bridge distances, forces, and damping must be finite and nonnegative")
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
	s.indexBridges()
	s.pruneBrokenBridges()

	for _, b := range s.bridges {
		if b.ConstrainDistance {
			continue
		}
		i, j := s.bridgeIndex[b.A], s.bridgeIndex[b.B]
		d, nx, ny := s.bridgeDirection(i, j)

		var force float32
		switch {
		case d < float64(b.MinDistance):
			force = -float32(float64(b.RepelForce) * (float64(b.MinDistance) - d))
		case d > float64(b.MaxDistance):
			force = float32(float64(b.AttractForce) * (d - float64(b.MaxDistance)))
		default:
			continue
		}
		fx, fy := float32(nx)*force, float32(ny)*force
		s.ax[i] += fx * s.p.invMass[i]
		s.ay[i] += fy * s.p.invMass[i]
		s.ax[j] -= fx * s.p.invMass[j]
		s.ay[j] -= fy * s.p.invMass[j]
	}
}

func (s *State) indexBridges() {
	// Circle IDs are dense and never removed. Refresh after each grid sort so
	// bridges remain attached to the same circles as storage order changes.
	s.bridgeIndex = resize(s.bridgeIndex, s.p.len()+1)
	for i, id := range s.p.id {
		s.bridgeIndex[id] = i
	}
}

// Test all breaks before corrections so transient solver positions do not make
// breaking depend on bridge insertion order.
func (s *State) pruneBrokenBridges() {
	active := s.bridges[:0]
	for _, b := range s.bridges {
		i, j := s.bridgeIndex[b.A], s.bridgeIndex[b.B]
		d, _, _ := s.bridgeDirection(i, j)
		if b.BreakDistance > 0 && d > float64(b.BreakDistance) {
			continue
		}
		active = append(active, b)
	}
	clear(s.bridges[len(active):])
	s.bridges = active
}

func (s *State) bridgeDirection(i, j int) (distance, nx, ny float64) {
	dx := float64(s.p.x[j]) - float64(s.p.x[i])
	dy := float64(s.p.y[j]) - float64(s.p.y[i])
	distance = math.Hypot(dx, dy)
	if distance > 0 {
		return distance, dx / distance, dy / distance
	}
	// Stable anti-symmetry for coincident centers, even after grid sorting.
	if s.p.id[i] < s.p.id[j] {
		return 0, 1, 0
	}
	return 0, -1, 0
}
