package softbody

import (
	"fmt"
	"math"
)

func New(cfg Config) *State {
	d := DefaultConfig()
	if cfg.Workers == 0 {
		cfg.Workers = d.Workers
	}
	if cfg.Substeps == 0 {
		cfg.Substeps = d.Substeps
	}
	if cfg.BridgeIterations == 0 {
		cfg.BridgeIterations = d.BridgeIterations
	}
	cfg.BridgeIterations = max(cfg.BridgeIterations, 1)
	if cfg.ShellStiffness == 0 {
		cfg.ShellStiffness = d.ShellStiffness
	}
	if cfg.ShellDamping == 0 {
		cfg.ShellDamping = d.ShellDamping
	}
	if cfg.CoreCorrection == 0 {
		cfg.CoreCorrection = d.CoreCorrection
	}
	if cfg.LinearDamping == 0 {
		cfg.LinearDamping = d.LinearDamping
	}
	if cfg.LinearDampingMassFactor < 0 {
		cfg.LinearDampingMassFactor = 0
	}
	if cfg.Workers < 1 {
		cfg.Workers = 1
	}
	if cfg.Substeps < 1 {
		cfg.Substeps = 1
	}
	if cfg.Restitution < 0 {
		cfg.Restitution = 0
	}
	if cfg.Restitution > 1 {
		cfg.Restitution = 1
	}
	return &State{cfg: cfg, bounds: Bounds{MaxX: 1, MaxY: 1}, nextID: 1, geometryDirty: true}
}

func (s *State) Config() Config { return s.cfg }

func (s *State) Len() int { return s.p.len() }

// SetBounds resizes the world and immediately confines existing cores to it.
// Like Step, it must be called from the simulation goroutine.
func (s *State) SetBounds(b Bounds) error {
	for _, v := range [...]float32{b.MinX, b.MinY, b.MaxX, b.MaxY} {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return fmt.Errorf("bounds must be finite")
		}
	}
	if b.MaxX <= b.MinX || b.MaxY <= b.MinY || min(b.MaxX-b.MinX, b.MaxY-b.MinY) < 2*s.maxOuter {
		return fmt.Errorf("bounds must have positive dimensions and fit the largest circle")
	}
	if b == s.bounds {
		return nil
	}
	s.bounds = b
	s.geometryDirty = true
	for i, radius := range s.p.inner {
		s.p.x[i], s.p.vx[i] = confine(s.p.x[i], s.p.vx[i], b.MinX+radius, b.MaxX-radius, s.cfg.Restitution)
		s.p.y[i], s.p.vy[i] = confine(s.p.y[i], s.p.vy[i], b.MinY+radius, b.MaxY-radius, s.cfg.Restitution)
		s.projectPolygonCore(i)
	}
	return nil
}

func (s *State) AddCircle(c CircleSpec) (uint64, error) {
	if c.InnerRadius <= 0 {
		return 0, fmt.Errorf("inner radius must be > 0")
	}
	if c.OuterRadius < c.InnerRadius {
		return 0, fmt.Errorf("outer radius must be >= inner radius")
	}
	if 2*c.OuterRadius > min(s.bounds.MaxX-s.bounds.MinX, s.bounds.MaxY-s.bounds.MinY) {
		return 0, fmt.Errorf("outer diameter must fit the world bounds")
	}
	if c.Mass <= 0 {
		c.Mass = 1
	}

	// Keep the hard core in the world. The outer shell may overlap a wall.
	c.X = clamp(c.X, s.bounds.MinX+c.InnerRadius, s.bounds.MaxX-c.InnerRadius)
	c.Y = clamp(c.Y, s.bounds.MinY+c.InnerRadius, s.bounds.MaxY-c.InnerRadius)

	i := s.p.len()
	s.p.resize(i + 1)
	id := s.nextID
	s.nextID++

	s.p.id[i] = id
	s.p.group[i] = c.Group
	s.p.x[i], s.p.y[i] = c.X, c.Y
	s.p.vx[i], s.p.vy[i] = c.VX, c.VY
	s.p.inner[i], s.p.outer[i] = c.InnerRadius, c.OuterRadius
	s.p.invMass[i] = 1 / c.Mass

	if c.OuterRadius > s.maxOuter {
		s.maxOuter = c.OuterRadius
		s.geometryDirty = true
	}
	s.projectPolygonCore(i)
	return id, nil
}

// QueueRadialImpulse queues an impulse for the next Step with a positive dt and
// at least one circle. It is applied once, regardless of the number of substeps.
// Sources are not clamped to world bounds. Nonpositive or NaN radii, nonfinite
// coordinates or strengths, and zero strengths are ignored.
// It is safe to call concurrently with Step. Groups is copied before returning.
func (s *State) QueueRadialImpulse(impulse RadialImpulse) {
	if !(impulse.Radius > 0) || impulse.Strength == 0 {
		return
	}
	for _, v := range [...]float32{impulse.X, impulse.Y, impulse.Strength} {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return
		}
	}
	impulse.Groups = append([]Group(nil), impulse.Groups...)
	s.impulseMu.Lock()
	s.impulses = append(s.impulses, impulse)
	s.impulseMu.Unlock()
}

func (s *State) consumeImpulses() []RadialImpulse {
	s.impulseMu.Lock()
	defer s.impulseMu.Unlock()
	out := s.impulses
	s.impulses = nil
	return out
}

// Snapshot returns circles in stable-ID form. Internal storage is reordered every
// substep for cache locality, so callers should never rely on slice positions.
func (s *State) Snapshot(dst []CircleSnapshot) []CircleSnapshot {
	n := s.p.len()
	if cap(dst) < n {
		dst = make([]CircleSnapshot, n)
	} else {
		dst = dst[:n]
	}
	for i := range n {
		dst[i] = CircleSnapshot{
			ID: s.p.id[i], Group: s.p.group[i],
			X: s.p.x[i], Y: s.p.y[i], VX: s.p.vx[i], VY: s.p.vy[i],
			InnerRadius: s.p.inner[i], OuterRadius: s.p.outer[i],
		}
	}
	return dst
}

func (s *State) Step(dt float32) {
	if dt <= 0 || s.p.len() == 0 {
		return
	}

	impulses := s.consumeImpulses()
	h := dt / float32(s.cfg.Substeps)
	for sub := 0; sub < s.cfg.Substeps; sub++ {
		s.carrySubstep(h, s.cfg.Substeps-sub)
		s.rebuildGrid()
		s.ensureWorkBuffers()

		var subImpulses []RadialImpulse
		if sub == 0 {
			subImpulses = impulses
		}
		s.solveCells(subImpulses)
		s.solveBridges()
		s.integrate(h)
		s.solveBridgeConstraints(h)
	}
}

func (s *State) ensureWorkBuffers() {
	n := s.p.len()
	s.ax = resize(s.ax, n)
	s.ay = resize(s.ay, n)
	s.dvx = resize(s.dvx, n)
	s.dvy = resize(s.dvy, n)
	s.corrX = resize(s.corrX, n)
	s.corrY = resize(s.corrY, n)
	clear(s.ax)
	clear(s.ay)
	clear(s.dvx)
	clear(s.dvy)
	clear(s.corrX)
	clear(s.corrY)
}

func clamp(v, lo, hi float32) float32 {
	return float32(math.Max(float64(lo), math.Min(float64(hi), float64(v))))
}

func confine(position, velocity, lo, hi, restitution float32) (float32, float32) {
	if position < lo {
		position = lo
		if velocity < 0 {
			velocity = -velocity * restitution
		}
	} else if position > hi {
		position = hi
		if velocity > 0 {
			velocity = -velocity * restitution
		}
	}
	return position, velocity
}
