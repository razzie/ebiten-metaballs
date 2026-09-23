package main

import (
	"math/rand"
	"os"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
	metaballs "github.com/razzie/ebiten-metaballs"
)

func TestDrawAllCircleGroups(t *testing.T) {
	if os.Getenv("METABALLS_DRAW_TESTS") != "1" {
		t.Skip("METABALLS_DRAW_TESTS not enabled")
	}
	g, err := NewGame()
	if err != nil {
		t.Fatal(err)
	}
	// Keep all 510 circles, arranged into three visible color bands so the
	// regression is deterministic and no group can hide behind another.
	rng := rand.New(rand.NewSource(42))
	for gi := range g.groups {
		for i := range g.groups[gi].group.Circles {
			g.groups[gi].group.Circles[i] = metaballs.Circle{
				X:      (float32(gi*10+i%10) + 0.5) / 30,
				Y:      (float32(i/10) + 0.5) / 17,
				Radius: minRadius,
			}
			g.groups[gi].velocities[i] = vec2{X: minSpeed, Y: minSpeed}
		}
		// Match the example's randomized primitive order rather than imposing
		// a spatial sweep (which has much longer blend dependency chains).
		rng.Shuffle(len(g.groups[gi].group.Circles), func(a, b int) {
			circles := g.groups[gi].group.Circles
			circles[a], circles[b] = circles[b], circles[a]
		})
	}
	if err := ebiten.RunGame(&manyCirclesTestGame{Game: g, t: t}); err != nil {
		t.Fatal(err)
	}
}

type manyCirclesTestGame struct {
	*Game
	t *testing.T
}

func (*manyCirclesTestGame) Draw(*ebiten.Image) {}

func (g *manyCirclesTestGame) Update() error {
	const size = 128
	dst := ebiten.NewImage(size, size)
	defer dst.Deallocate()
	xform, _ := metaballs.NewCenteredUVTransform(size, size)
	groups := make([]metaballs.Group, len(g.groups))
	for i := range g.groups {
		groups[i] = g.groups[i].group
	}
	for frame := 0; frame < 3; frame++ {
		dst.Clear()
		stats, err := g.renderer.Draw(dst, groups, xform)
		if err != nil {
			g.t.Fatal(err)
		}
		if clipped := stats.CirclesClipped.Load(); clipped != 0 {
			g.t.Fatalf("frame %d: clipped %d circles", frame, clipped)
		}
		if stats.TilesDrawn.Load() <= 1 {
			g.t.Fatal("expected local culling and subdivision, not a full-scene capacity tier")
		}
		pixels := make([]byte, size*size*4)
		dst.ReadPixels(pixels)
		var colors [3]int
		for i := 0; i < len(pixels); i += 4 {
			r, green, b := int(pixels[i]), int(pixels[i+1]), int(pixels[i+2])
			if r > green*2 && r > b*2 {
				colors[0]++
			}
			if b > r*2 && b > green*2 {
				colors[1]++
			}
			if green > r*2 && green > b*2 {
				colors[2]++
			}
		}
		for group, count := range colors {
			if count == 0 {
				g.t.Errorf("frame %d: group %d has no visible pixels", frame, group)
			}
		}
		if err := g.Game.Update(); err != nil {
			g.t.Fatal(err)
		}
	}
	return ebiten.Termination
}
