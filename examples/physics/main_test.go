package main

import (
	"slices"
	"testing"

	metaballs "github.com/razzie/ebiten-metaballs"
	"github.com/razzie/ebiten-metaballs/softbody"
)

func TestSyncCirclesPreservesBlendOrderAcrossGridRebuild(t *testing.T) {
	cfg := softbody.DefaultConfig()
	cfg.Workers = 1
	world := softbody.New(cfg)
	// Stationary, separated cores in a different order from their grid cells.
	// With SmoothK=0.06, these same-color circles still blend visually.
	for _, x := range []float32{0.56, 0.48, 0.52} {
		for group := red; group <= blue; group++ {
			_, err := world.AddCircle(softbody.CircleSpec{
				X: x, Y: 0.25 + 0.25*float32(group),
				InnerRadius: 0.01, OuterRadius: 0.01, Group: group,
			})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	g := &Game{world: world, groups: make([]metaballs.Group, 3)}
	g.syncCircles()
	want := make([][]metaballs.Circle, len(g.groups))
	for i := range g.groups {
		want[i] = slices.Clone(g.groups[i].Circles)
	}
	before := world.Snapshot(nil)
	world.Step(1.0 / ticksPerSecond)
	after := world.Snapshot(nil)
	if slices.Equal(before, after) {
		t.Fatal("fixture did not exercise grid reordering")
	}
	for _, c := range after {
		if c != before[c.ID-1] {
			t.Fatalf("stationary circle %d changed: got %+v, want %+v", c.ID, c, before[c.ID-1])
		}
	}
	g.syncCircles()
	for i := range g.groups {
		if !slices.Equal(g.groups[i].Circles, want[i]) {
			t.Fatalf("group %d changed blend order after grid rebuild: got %v, want %v", i, g.groups[i].Circles, want[i])
		}
	}
}
