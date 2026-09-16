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
	if cfg.ClickRadius == 0 {
		cfg.ClickRadius = d.ClickRadius
	}
	if cfg.ClickImpulse == 0 {
		cfg.ClickImpulse = d.ClickImpulse
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
	s.clickMu.Lock()
	s.bounds = b
	s.clickMu.Unlock()
	s.geometryDirty = true
	for i, radius := range s.p.inner {
		s.p.x[i], s.p.vx[i] = confine(s.p.x[i], s.p.vx[i], b.MinX+radius, b.MaxX-radius, s.cfg.Restitution)
		s.p.y[i], s.p.vy[i] = confine(s.p.y[i], s.p.vy[i], b.MinY+radius, b.MaxY-radius, s.cfg.Restitution)
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
	if c.Group > Blue {
		return 0, fmt.Errorf("invalid group %d", c.Group)
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
	return id, nil
}

// RegisterClick is safe to call concurrently with Step. Coordinates use world bounds.
// The click is consumed by the next Step and acts as an attraction impulse.
func (s *State) RegisterClick(button MouseButton, x, y float32) {
	var group Group
	switch button {
	case MouseLeft:
		group = Red
	case MouseRight:
		group = Blue
	case MouseMiddle:
		group = Green
	default:
		return
	}

	s.clickMu.Lock()
	s.clicks = append(s.clicks, click{x: clamp(x, s.bounds.MinX, s.bounds.MaxX), y: clamp(y, s.bounds.MinY, s.bounds.MaxY), group: group})
	s.clickMu.Unlock()
}

func (s *State) consumeClicks() []click {
	s.clickMu.Lock()
	defer s.clickMu.Unlock()
	if len(s.clicks) == 0 {
		return nil
	}
	out := append([]click(nil), s.clicks...)
	for i := range out {
		out[i].x = clamp(out[i].x, s.bounds.MinX, s.bounds.MaxX)
		out[i].y = clamp(out[i].y, s.bounds.MinY, s.bounds.MaxY)
	}
	s.clicks = s.clicks[:0]
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

	clicks := s.consumeClicks()
	h := dt / float32(s.cfg.Substeps)
	for sub := 0; sub < s.cfg.Substeps; sub++ {
		s.rebuildGrid()
		s.ensureWorkBuffers()

		var subClicks []click
		if sub == 0 {
			subClicks = clicks
		}
		s.solveCells(subClicks)
		s.integrate(h)
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
