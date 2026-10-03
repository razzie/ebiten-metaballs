package main

import (
	"bytes"
	"os"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
)

func TestWaterShaderCompiles(t *testing.T) {
	s, err := ebiten.NewShader(waterSource)
	if err != nil {
		t.Fatal(err)
	}
	s.Deallocate()
}

func TestWaterDraw(t *testing.T) {
	if os.Getenv("METABALLS_DRAW_TESTS") != "1" {
		t.Skip("METABALLS_DRAW_TESTS not enabled")
	}
	g, err := NewGame()
	if err != nil {
		t.Fatal(err)
	}
	if err := ebiten.RunGame(&waterTestGame{Game: g, t: t}); err != nil {
		t.Fatal(err)
	}
}

type waterTestGame struct {
	*Game
	t *testing.T
}

func (*waterTestGame) Draw(*ebiten.Image) {}

func (g *waterTestGame) Update() error {
	dst := ebiten.NewImage(width, height)
	defer dst.Deallocate()
	pixels := make([]byte, width*height*4)
	var water []byte
	for view := 0; view < 4; view++ {
		g.view = view
		g.Game.Draw(dst)
		dst.ReadPixels(pixels)
		for i := 3; i < len(pixels); i += 4 {
			if pixels[i] != 255 {
				g.t.Fatalf("view %d: water color pass must be opaque; pixel %d has alpha %d", view, i/4, pixels[i])
			}
		}
		if view == 0 {
			water = bytes.Clone(pixels)
		} else if bytes.Equal(pixels, water) {
			g.t.Fatalf("geometry view %d did not change output", view)
		}
	}
	geometry := make([]byte, len(pixels))
	g.geometry.ReadPixels(geometry)
	interior := 0
	for i := 3; i < len(geometry); i += 4 {
		if geometry[i] > 0 && geometry[i] < 255 {
			interior++
		}
	}
	if interior < 10000 || interior > width*height/2 {
		g.t.Fatalf("unexpected geometry coverage: %d pixels", interior)
	}
	// Animation must update the geometry rather than leaving a cached color texture.
	g.ticks = 120
	g.animate()
	g.view = 0
	g.Game.Draw(dst)
	dst.ReadPixels(pixels)
	if bytes.Equal(water, pixels) {
		g.t.Fatal("water animation did not change output")
	}
	return ebiten.Termination
}
