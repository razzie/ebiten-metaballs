package softbody

import (
	"math"
	"slices"
	"testing"
)

func TestRadialImpulseDirectionAndGroups(t *testing.T) {
	for _, strength := range []float32{-2, 2} {
		for _, groups := range [][]Group{nil, {}, {0}, {17, 1000, 17}} {
			cfg := DefaultConfig()
			cfg.LinearDamping = -1
			s := New(cfg)
			for i, group := range []Group{0, 17, 1000} {
				if _, err := s.AddCircle(CircleSpec{X: .5, Y: .4 + .1*float32(i), InnerRadius: .01, OuterRadius: .02, Mass: 2, Group: group}); err != nil {
					t.Fatal(err)
				}
			}
			before := s.Snapshot(nil)
			selected := slices.Clone(groups)
			s.QueueRadialImpulse(RadialImpulse{X: .35, Y: .3, Radius: .5, Strength: strength, Groups: selected})
			// The caller may reuse its selection slice immediately after queueing.
			for i := range selected {
				selected[i] = 9999
			}
			if !slices.Equal(before, s.Snapshot(nil)) {
				t.Fatal("queueing changed velocities before Step")
			}
			s.Step(.001)
			for _, c := range s.Snapshot(nil) {
				original := before[c.ID-1]
				var vx, vy float32
				if len(groups) == 0 || slices.Contains(groups, c.Group) {
					dx, dy := original.X-.35, original.Y-.3
					d := float32(math.Hypot(float64(dx), float64(dy)))
					impulse := strength * (1 - d/.5) * (1 - d/.5) / 2
					vx, vy = dx/d*impulse, dy/d*impulse
				}
				if math.Abs(float64(c.VX-vx)) > 1e-6 || math.Abs(float64(c.VY-vy)) > 1e-6 {
					t.Fatalf("strength %g, groups %v: %+v, want velocity (%g, %g)", strength, groups, c, vx, vy)
				}
			}
		}
	}
}

func TestRadialImpulseRadiusAndFalloff(t *testing.T) {
	cfg := DefaultConfig()
	cfg.LinearDamping = -1
	s := New(cfg)
	for i, x := range []float32{.5, .5625, .625, .75, .875} {
		if _, err := s.AddCircle(CircleSpec{X: x, Y: .5, InnerRadius: .01, OuterRadius: .025, Group: Group(i)}); err != nil {
			t.Fatal(err)
		}
	}
	s.QueueRadialImpulse(RadialImpulse{X: .5, Y: .5, Radius: .25, Strength: 1})
	s.Step(.001)
	for _, c := range s.Snapshot(nil) {
		want := []float32{0, .5625, .25, 0, 0}[c.ID-1]
		if math.Abs(float64(c.VX-want)) > 1e-7 || c.VY != 0 || math.IsNaN(float64(c.VX)) {
			t.Fatalf("circle %d: velocity (%g, %g), want (%g, 0)", c.ID, c.VX, c.VY, want)
		}
	}
}

func TestRadialImpulseInvalidInputsIgnored(t *testing.T) {
	valid := RadialImpulse{X: .5, Y: .5, Radius: 1, Strength: 1}
	for _, change := range []func(*RadialImpulse){
		func(p *RadialImpulse) { p.Radius = 0 },
		func(p *RadialImpulse) { p.Radius = -1 },
		func(p *RadialImpulse) { p.Radius = float32(math.NaN()) },
		func(p *RadialImpulse) { p.Strength = 0 },
		func(p *RadialImpulse) { p.Strength = float32(math.NaN()) },
		func(p *RadialImpulse) { p.Strength = float32(math.Inf(1)) },
		func(p *RadialImpulse) { p.X = float32(math.Inf(-1)) },
		func(p *RadialImpulse) { p.Y = float32(math.NaN()) },
	} {
		s := New(DefaultConfig())
		impulse := valid
		change(&impulse)
		s.QueueRadialImpulse(impulse)
		if len(s.consumeImpulses()) != 0 {
			t.Fatalf("invalid impulse queued: %+v", impulse)
		}
	}
}

func TestRadialImpulsesHaveIndependentSettings(t *testing.T) {
	cfg := DefaultConfig()
	cfg.LinearDamping = -1
	s := New(cfg)
	if _, err := s.AddCircle(CircleSpec{X: .5, Y: .5, InnerRadius: .01, OuterRadius: .02}); err != nil {
		t.Fatal(err)
	}
	s.QueueRadialImpulse(RadialImpulse{X: .25, Y: .5, Radius: .5, Strength: 2})
	s.QueueRadialImpulse(RadialImpulse{X: .5, Y: .75, Radius: 1, Strength: -1})
	s.Step(.001)
	if c := s.Snapshot(nil)[0]; c.VX != .5 || c.VY != .5625 {
		t.Fatalf("impulses did not add independently: %+v", c)
	}
}
