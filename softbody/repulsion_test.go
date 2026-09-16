package softbody

import (
	"math"
	"slices"
	"testing"
)

func TestRepelFromPointerAllGroups(t *testing.T) {
	for group := Red; group <= Blue; group++ {
		cfg := DefaultConfig()
		cfg.ClickRadius, cfg.ClickImpulse = .5, 2
		s := New(cfg)
		// Include a single circle: repulsion must come from the pointer, not peers.
		if _, err := s.AddCircle(CircleSpec{
			X: .5, Y: .5, InnerRadius: .01, OuterRadius: .025, Mass: 2, Group: group,
		}); err != nil {
			t.Fatal(err)
		}
		// Distance .25, unit direction (.6, .8), half-radius falloff, mass 2.
		s.Repel(.35, .3)
		got := s.Snapshot(nil)[0]
		if math.Abs(float64(got.VX-.15)) > 1e-6 || math.Abs(float64(got.VY-.2)) > 1e-6 {
			t.Fatalf("group %d: expected outward velocity (.15, .2), got %+v", group, got)
		}
		// Moving the pointer to the opposite side reverses the impulse.
		s.Repel(.65, .7)
		got = s.Snapshot(nil)[0]
		if math.Abs(float64(got.VX)) > 1e-6 || math.Abs(float64(got.VY)) > 1e-6 {
			t.Fatalf("repulsion did not follow the pointer: %+v", got)
		}
	}
}

func TestRepelRadiusAndFalloff(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ClickRadius, cfg.ClickImpulse = .25, 1
	s := New(cfg)
	for i, x := range []float32{.5, .5625, .625, .75, .875} {
		if _, err := s.AddCircle(CircleSpec{
			X: x, Y: .5, InnerRadius: .01, OuterRadius: .025, Group: Group(i % 3),
		}); err != nil {
			t.Fatal(err)
		}
	}
	s.Repel(.5, .5)
	for i, c := range s.Snapshot(nil) {
		want := []float32{0, .5625, .25, 0, 0}[i]
		if math.Abs(float64(c.VX-want)) > 1e-7 || c.VY != 0 || math.IsNaN(float64(c.VX)) {
			t.Fatalf("circle %d: velocity (%g, %g), want (%g, 0)", i, c.VX, c.VY, want)
		}
	}
}

func TestRepelDisabled(t *testing.T) {
	for _, cfg := range []Config{
		{ClickRadius: -1, ClickImpulse: 1},
		{ClickRadius: .5, ClickImpulse: -1},
	} {
		s := New(cfg)
		if _, err := s.AddCircle(CircleSpec{X: .6, Y: .5, VX: .1, InnerRadius: .01, OuterRadius: .025}); err != nil {
			t.Fatal(err)
		}
		before := s.Snapshot(nil)
		s.Repel(.5, .5)
		if !slices.Equal(before, s.Snapshot(nil)) {
			t.Fatal("disabled repulsion changed state")
		}
	}
}
