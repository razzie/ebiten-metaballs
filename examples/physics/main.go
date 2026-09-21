package main

import (
	"cmp"
	"fmt"
	"image/color"
	"log"
	"math"
	"math/rand/v2"
	"slices"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	metaballs "github.com/razzie/ebiten-metaballs"
	"github.com/razzie/ebiten-metaballs/softbody"
)

const (
	screenSize      = 900
	ticksPerSecond  = 60
	circlesPerGroup = 36
	baseRadius      = 0.02
	cursorRadius    = 0.5
	cursorImpulse   = 1.5 / ticksPerSecond
)

const (
	red softbody.Group = iota
	green
	blue
)

type Game struct {
	world    *softbody.State
	renderer *metaballs.Renderer
	groups   []metaballs.Group
	snapshot []softbody.CircleSnapshot
	xform    metaballs.UVTransform
	bounds   metaballs.UVBounds
}

func NewGame() (*Game, error) {
	cfg := softbody.DefaultConfig()
	cfg.Workers = 4
	cfg.Substeps = 4
	// Same colors cohere only within a tiny gap between their outer shells.
	cfg.AttractionRange = 0.015
	cfg.AttractionStrength = 0.3
	world := softbody.New(cfg)

	// Scatter separated shells randomly and interleave colors in one shared
	// world, so all colors collide with each other through the same hex grid.
	const count = 3 * circlesPerGroup
	spawned := make([]metaballs.Circle, 0, count)
	for i := range count {
		size := 0.5 + rand.Float32()
		outer := baseRadius * size
		var x, y float32
		placed := false
		for range 1000 {
			x = outer + rand.Float32()*(1-2*outer)
			y = outer + rand.Float32()*(1-2*outer)
			placed = true
			for _, other := range spawned {
				dx, dy := x-other.X, y-other.Y
				separation := outer + other.Radius
				if dx*dx+dy*dy < separation*separation {
					placed = false
					break
				}
			}
			if placed {
				break
			}
		}
		if !placed {
			return nil, fmt.Errorf("place circle %d: no non-overlapping position found", i)
		}
		spawned = append(spawned, metaballs.Circle{X: x, Y: y, Radius: outer})
		angle := rand.Float64() * 2 * math.Pi
		speed := 0.025 + rand.Float32()*0.025
		_, err := world.AddCircle(softbody.CircleSpec{
			X:  x,
			Y:  y,
			VX: speed * float32(math.Cos(angle)),
			VY: speed * float32(math.Sin(angle)),
			// The solid collision core and rendered circle share the same radius.
			InnerRadius: outer,
			OuterRadius: outer,
			// Constant density: mass scales with area, with unit mass at baseRadius.
			Mass:  size * size,
			Group: softbody.Group(i % 3),
		})
		if err != nil {
			return nil, fmt.Errorf("add circle %d: %w", i, err)
		}
	}

	renderer, err := metaballs.NewRenderer(metaballs.RendererConfig{
		Common: metaballs.ShaderCommonConfig{
			// Keep contact rounding small relative to the physical circle radius.
			SmoothK:       baseRadius * 3,
			LightDirX:     1,
			LightDirY:     -1,
			EdgeThickness: 0.008,
			FxaaEnabled:   true,
		},
		Tiers: []metaballs.ShaderCapacity{
			{Groups: 3, Circles: 48},
			// Fit the entire world even when clicks bring all colors together.
			{Groups: 3, Circles: 3 * circlesPerGroup},
		},
		RootCols:    2,
		RootRows:    2,
		MaxDepth:    3,
		MinTileSize: 0.01,
		Workers:     4,
	})
	if err != nil {
		return nil, err
	}

	xform, bounds := metaballs.NewCenteredUVTransform(screenSize, screenSize)
	g := &Game{
		world:    world,
		renderer: renderer,
		xform:    xform,
		bounds:   bounds,
		groups: []metaballs.Group{
			red:   {Color: metaballs.NewColorScale(1, 0.15, 0.15, 1)},
			green: {Color: metaballs.NewColorScale(0.15, 1, 0.25, 1)},
			blue:  {Color: metaballs.NewColorScale(0.15, 0.35, 1, 1)},
		},
	}
	for i := range g.groups {
		g.groups[i].Circles = make([]metaballs.Circle, 0, circlesPerGroup)
	}
	g.syncCircles()
	return g, nil
}

func (g *Game) syncCircles() {
	g.snapshot = g.world.Snapshot(g.snapshot)
	// Grid rebuilds reorder snapshots. Smooth-min blending is not associative,
	// so preserve each circle's place in the blend as it moves between cells.
	slices.SortFunc(g.snapshot, func(a, b softbody.CircleSnapshot) int {
		return cmp.Compare(a.ID, b.ID)
	})
	for i := range g.groups {
		g.groups[i].Circles = g.groups[i].Circles[:0]
	}
	for _, c := range g.snapshot {
		group := &g.groups[c.Group]
		group.Circles = append(group.Circles, metaballs.Circle{
			X: c.X, Y: c.Y, Radius: c.OuterRadius,
		})
	}
}

func (g *Game) Update() error {
	mx, my := ebiten.CursorPosition()
	x, y := g.xform.ScreenToUV(mx, my)
	if x >= g.bounds.MinX && x < g.bounds.MaxX && y >= g.bounds.MinY && y < g.bounds.MaxY {
		if ebiten.IsKeyPressed(ebiten.KeySpace) {
			g.world.QueueRadialImpulse(softbody.RadialImpulse{
				X: x, Y: y, Radius: cursorRadius, Strength: cursorImpulse,
			})
		}
		for _, binding := range [...]struct {
			button ebiten.MouseButton
			group  softbody.Group
		}{
			{ebiten.MouseButtonLeft, red},
			{ebiten.MouseButtonMiddle, green},
			{ebiten.MouseButtonRight, blue},
		} {
			if ebiten.IsMouseButtonPressed(binding.button) {
				g.world.QueueRadialImpulse(softbody.RadialImpulse{
					X: x, Y: y, Radius: cursorRadius, Strength: -cursorImpulse,
					Groups: []softbody.Group{binding.group},
				})
			}
		}
	}
	g.world.Step(1.0 / ticksPerSecond)
	g.syncCircles()
	return nil
}

func (g *Game) Draw(screen *ebiten.Image) {
	screen.Fill(color.RGBA{R: 16, G: 18, B: 24, A: 255})
	if _, err := g.renderer.Draw(screen, g.groups, g.xform); err != nil {
		ebitenutil.DebugPrint(screen, err.Error())
		return
	}
	ebitenutil.DebugPrint(screen, fmt.Sprintf(
		"Hold to attract: left = red | middle = green | right = blue\nHold Space to repel all colors near the cursor\n%d circles | FPS: %.1f | TPS: %.1f",
		g.world.Len(), ebiten.ActualFPS(), ebiten.ActualTPS(),
	))
}

func (g *Game) Layout(outsideWidth, outsideHeight int) (int, int) {
	width, height := max(outsideWidth, 1), max(outsideHeight, 1)
	xform, bounds := metaballs.NewCenteredUVTransform(width, height)
	g.xform = xform
	if bounds != g.bounds {
		if err := g.world.SetBounds(softbody.Bounds{
			MinX: bounds.MinX, MinY: bounds.MinY,
			MaxX: bounds.MaxX, MaxY: bounds.MaxY,
		}); err != nil {
			panic(err)
		}
		g.bounds = bounds
		g.syncCircles()
	}
	return width, height
}

func main() {
	game, err := NewGame()
	if err != nil {
		log.Fatal(err)
	}
	ebiten.SetWindowSize(screenSize, screenSize)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	ebiten.SetWindowTitle("Metaballs - Softbody Physics")
	ebiten.SetTPS(ticksPerSecond)
	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}
