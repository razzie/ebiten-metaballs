package main

import (
	"bytes"
	"os"
	"slices"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
	metaballs "github.com/razzie/ebiten-metaballs"
	"github.com/razzie/ebiten-metaballs/softbody"
)

func TestGelShaderCompiles(t *testing.T) {
	shader, err := ebiten.NewShader(gelSource)
	if err != nil {
		t.Fatal(err)
	}
	shader.Deallocate()
}

func TestPhysicsShadingDraw(t *testing.T) {
	if os.Getenv("METABALLS_DRAW_TESTS") != "1" {
		t.Skip("METABALLS_DRAW_TESTS not enabled")
	}
	g, err := NewGame()
	if err != nil {
		t.Fatal(err)
	}
	if err := ebiten.RunGame(&shadingTestGame{Game: g, t: t}); err != nil {
		t.Fatal(err)
	}
}

type shadingTestGame struct {
	*Game
	t *testing.T
}

func (*shadingTestGame) Draw(*ebiten.Image) {}

func (g *shadingTestGame) Update() error {
	// Exercise merged same-color geometry and contacts between different colors.
	for _, size := range [][2]int{{640, 480}, {480, 640}} {
		w, h := g.Layout(size[0], size[1])
		g.groups[red].Circles = []metaballs.Circle{{X: 0.3, Y: 0.4, Radius: 0.09}, {X: 0.4, Y: 0.4, Radius: 0.08}}
		g.groups[green].Circles = []metaballs.Circle{{X: 0.53, Y: 0.4, Radius: 0.09}}
		g.groups[blue].Circles = []metaballs.Circle{{X: 0.7, Y: 0.65, Radius: 0.08}}
		dst := ebiten.NewImage(w, h)
		pixels := make([]byte, w*h*4)
		var previous []byte
		for mode := range shadingModeCount {
			g.shading = mode
			g.Game.Draw(dst)
			dst.ReadPixels(pixels)
			for i := 3; i < len(pixels); i += 4 {
				if pixels[i] != 255 {
					g.t.Fatalf("%dx%d %s: pixel %d has alpha %d", w, h, shadingNames[mode], i/4, pixels[i])
				}
			}
			// Ignore the HUD: the rendered surfaces themselves must change.
			surfaces := pixels[w*(h/4)*4:]
			if bytes.Equal(previous, surfaces) {
				g.t.Fatalf("%dx%d: %s did not change surface shading", w, h, shadingNames[mode])
			}
			previous = bytes.Clone(surfaces)
		}
		if g.geometry.Bounds() != dst.Bounds() || g.pigment.Bounds() != dst.Bounds() {
			g.t.Fatalf("gel buffers did not resize to %dx%d", w, h)
		}
		// Gel must retain the red/green/blue identity used by mouse controls.
		scaleX, scaleY := g.xform.Scale()
		offsetX, offsetY := g.xform.Offset()
		for group, circles := range g.groups {
			c := circles.Circles[0]
			x, y := int((c.X-offsetX)/scaleX), int((c.Y-offsetY)/scaleY)
			i := (y*w + x) * 4
			for channel := range 3 {
				if channel != group && pixels[i+group] <= pixels[i+channel] {
					g.t.Fatalf("gel lost group %d pigment at %dx%d: %v", group, w, h, pixels[i:i+4])
				}
			}
		}
		// Move every droplet away. Cleared geometry and color must leave no trails.
		for group := range g.groups {
			g.groups[group].Circles = nil
		}
		g.Game.Draw(dst)
		g.geometry.ReadPixels(pixels)
		for i, value := range pixels {
			if value != 0 {
				g.t.Fatalf("stale geometry at byte %d after clearing the scene", i)
			}
		}
		g.pigment.ReadPixels(pixels)
		for i, value := range pixels {
			if value != 0 {
				g.t.Fatalf("stale pigment at byte %d after clearing the scene", i)
			}
		}
		dst.Deallocate()
	}
	return ebiten.Termination
}

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
