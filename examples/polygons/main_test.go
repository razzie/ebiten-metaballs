package main

import (
	"bytes"
	"os"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
	metaballs "github.com/razzie/ebiten-metaballs"
	"github.com/razzie/ebiten-metaballs/softbody"
)

func TestSceneFunnelAndReset(t *testing.T) {
	g, err := NewGame()
	if err != nil {
		t.Fatal(err)
	}
	capacity := metaballs.CapacityForGroups(g.groups)
	if capacity.Groups != 4 || capacity.Circles != 14 || capacity.Walls != 22 {
		t.Fatalf("incomplete scene: %+v", capacity)
	}
	// Drag a real scene circle through the throat. The core must fit even
	// though its rendered outer diameter is larger than the opening.
	c := g.snapshot[0]
	g.world.Drag(softbody.DragSpec{X: c.X, Y: c.Y, MaxCircles: 1})
	for _, target := range []softbody.Point{{550, 220}, {550, 409}, {550, 470}} {
		g.world.Carry(target.X, target.Y)
		g.world.Step(1.0 / ticksPerSecond)
		g.syncCircles()
		moved := g.snapshot[0]
		if moved.ID != c.ID || moved.X != target.X || moved.Y != target.Y {
			t.Fatalf("circle did not pass through throat at %+v: %+v", target, moved)
		}
	}
	// A fast drag into the concave obstacle must stop at its solid boundary.
	g.world.Carry(350, 495)
	g.world.Step(1.0 / ticksPerSecond)
	g.syncCircles()
	stopped := g.snapshot[0]
	if stopped.X < 430+stopped.InnerRadius-0.001 {
		t.Fatalf("drag crossed the polygon: %+v", stopped)
	}
	g.world.Drop()
	for range 300 {
		g.step()
	}
	if err := g.reset(); err != nil {
		t.Fatal(err)
	}
	if got := metaballs.CapacityForGroups(g.groups); got != capacity {
		t.Fatalf("reset changed scene capacity: %+v, want %+v", got, capacity)
	}
	if g.snapshot[0] != c {
		t.Fatalf("reset did not restore initial circle: %+v", g.snapshot[0])
	}
	// The unattended demonstration must pass at least one circle through the
	// funnel, rather than leaving the entire initial arrangement jammed.
	for range 600 {
		g.step()
	}
	passed := false
	for _, c := range g.snapshot {
		passed = passed || c.Y > 450
	}
	if !passed {
		t.Fatal("automatic pull did not carry any circle through the funnel")
	}
}

func TestDrawPolygonScene(t *testing.T) {
	if os.Getenv("METABALLS_DRAW_TESTS") != "1" {
		t.Skip("METABALLS_DRAW_TESTS not enabled")
	}
	g, err := NewGame()
	if err != nil {
		t.Fatal(err)
	}
	if err := ebiten.RunGame(&drawTestGame{Game: g, t: t}); err != nil {
		t.Fatal(err)
	}
}

type drawTestGame struct {
	*Game
	t *testing.T
}

func (g *drawTestGame) Draw(*ebiten.Image) {}
func (g *drawTestGame) Update() error {
	dst := ebiten.NewImage(width, height)
	defer dst.Deallocate()
	// Hold a circle against the left throat edge, compressing its shell.
	c := g.snapshot[0]
	g.world.Drag(softbody.DragSpec{X: c.X, Y: c.Y, MaxCircles: 1})
	g.world.Carry(550, 220)
	g.world.Step(1.0 / ticksPerSecond)
	g.world.Carry(537, 409)
	g.world.Step(1.0 / ticksPerSecond)
	g.syncCircles()
	withWalls := make([]byte, 4*width*height)
	if _, err := g.renderer.Draw(dst, g.groups, g.xform); err != nil {
		g.t.Fatal(err)
	}
	dst.ReadPixels(withWalls)
	walls := g.groups[wallGroup].Walls
	g.groups[wallGroup].Walls = nil
	dst.Clear()
	if _, err := g.renderer.Draw(dst, g.groups, g.xform); err != nil {
		g.t.Fatal(err)
	}
	withoutWalls := make([]byte, len(withWalls))
	dst.ReadPixels(withoutWalls)
	g.groups[wallGroup].Walls = walls
	// Compare visible free space, not the interior hidden by polygon fills.
	changed := false
	for y := 395; y < 425; y++ {
		for x := 526; x < 550; x++ {
			i := 4 * (y*width + x)
			changed = changed || !bytes.Equal(withWalls[i:i+4], withoutWalls[i:i+4])
		}
	}
	if !changed {
		g.t.Error("polygon walls did not squeeze the visible circle")
	}
	g.Game.Draw(dst)
	pixels := make([]byte, len(withWalls))
	dst.ReadPixels(pixels)
	pixel := func(x, y int) []byte { i := 4 * (y*width + x); return pixels[i : i+4] }
	// Solid fill in the L's arm, and empty space in its concave cutout.
	if p := pixel(250, 560); p[0] < 50 || p[1] < 65 || p[2] < 85 || p[3] != 255 {
		g.t.Errorf("polygon interior was not filled: %v", p)
	}
	if p := pixel(350, 560); !bytes.Equal(p, []byte{18, 23, 32, 255}) {
		g.t.Errorf("concave cutout was filled: %v", p)
	}
	return ebiten.Termination
}
