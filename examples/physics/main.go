package main

import (
	"fmt"
	"image/color"
	"log"
	"math"
	"math/rand/v2"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	metaballs "github.com/razzie/ebiten-metaballs"
	"github.com/razzie/ebiten-metaballs/examples/physics/softbody"
)

const (
	screenSize      = 900
	ticksPerSecond  = 60
	circlesPerGroup = 48
	spawnColumns    = 12
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
	// Attract the whole group regardless of the window's aspect ratio.
	cfg.ClickRadius = float32(math.Inf(1))
	// Register an impulse every tick while held, for a steady attraction force.
	cfg.ClickImpulse = 1.5 / ticksPerSecond
	world := softbody.New(cfg)

	// Start with separated shells and interleave colors in one shared world,
	// so all colors collide with each other through the same hex grid.
	const count = 3 * circlesPerGroup
	const rows = (count + spawnColumns - 1) / spawnColumns
	for i := range count {
		outer := 0.022 + rand.Float32()*0.006
		angle := rand.Float64() * 2 * math.Pi
		speed := 0.025 + rand.Float32()*0.025
		_, err := world.AddCircle(softbody.CircleSpec{
			X:           (float32(i%spawnColumns) + 0.5) / spawnColumns,
			Y:           (float32(i/spawnColumns) + 0.5) / rows,
			VX:          speed * float32(math.Cos(angle)),
			VY:          speed * float32(math.Sin(angle)),
			InnerRadius: outer * 0.6,
			OuterRadius: outer,
			Mass:        1,
			Group:       softbody.Group((i + i/spawnColumns) % 3),
		})
		if err != nil {
			return nil, fmt.Errorf("add circle %d: %w", i, err)
		}
	}

	renderer, err := metaballs.NewRenderer(metaballs.RendererConfig{
		Common: metaballs.ShaderCommonConfig{
			SmoothK:       0.02,
			LightDirX:     1,
			LightDirY:     -1,
			EdgeThickness: 0.008,
			FxaaEnabled:   true,
		},
		Tiers: []metaballs.ShaderCapacity{
			{MainCircles: 16, OtherCircles: 32},
			// Fit the entire world even when clicks bring all colors together.
			{MainCircles: circlesPerGroup, OtherCircles: 2 * circlesPerGroup},
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
			softbody.Red:   {Color: metaballs.NewColorScale(1, 0.15, 0.15, 1)},
			softbody.Green: {Color: metaballs.NewColorScale(0.15, 1, 0.25, 1)},
			softbody.Blue:  {Color: metaballs.NewColorScale(0.15, 0.35, 1, 1)},
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
	for i := range g.groups {
		g.groups[i].Circles = g.groups[i].Circles[:0]
	}
	// Snapshots follow the grid's current ordering, not insertion order.
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
		for _, binding := range [...]struct {
			button ebiten.MouseButton
			target softbody.MouseButton
		}{
			{ebiten.MouseButtonLeft, softbody.MouseLeft},
			{ebiten.MouseButtonMiddle, softbody.MouseMiddle},
			{ebiten.MouseButtonRight, softbody.MouseRight},
		} {
			if ebiten.IsMouseButtonPressed(binding.button) {
				g.world.RegisterClick(binding.target, x, y)
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
		"Hold to attract: left = red | middle = green | right = blue\n%d circles | FPS: %.1f | TPS: %.1f",
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
