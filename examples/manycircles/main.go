package main

import (
	"fmt"
	"log"
	"math"
	"math/rand"
	"time"

	metaballs "github.com/razzie/ebiten-metaballs"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
)

const (
	screenWidth  = 900
	screenHeight = 900

	circlesPerGroup = 170 // ~500 circles total across 3 groups
	minRadius       = 0.01
	maxRadius       = 0.02
	minSpeed        = 0.05
	maxSpeed        = 0.15

	smoothK       = 0.015
	lightDirX     = 1
	lightDirY     = -1
	edgeThickness = 0.03
)

// vec2 is a plain 2D vector used for per-circle velocity.
type vec2 struct{ X, Y float32 }

// movingGroup pairs a rendered Group with the velocities driving its circles.
type movingGroup struct {
	group      metaballs.Group
	velocities []vec2
}

type Game struct {
	renderer *metaballs.Renderer
	groups   []movingGroup

	lastStats metaballs.Stats
}

func randRange(min, max float32) float32 {
	return min + rand.Float32()*(max-min)
}

func newMovingGroup(color ebiten.ColorScale, count int) movingGroup {
	circles := make([]metaballs.Circle, count)
	velocities := make([]vec2, count)

	for i := range circles {
		circles[i] = metaballs.Circle{
			X:      randRange(0, 1),
			Y:      randRange(0, 1),
			Radius: randRange(minRadius, maxRadius),
		}

		speed := randRange(minSpeed, maxSpeed)
		angle := rand.Float32() * 2 * math.Pi
		velocities[i] = vec2{
			X: speed * float32(math.Cos(float64(angle))),
			Y: speed * float32(math.Sin(float64(angle))),
		}
	}

	return movingGroup{
		group: metaballs.Group{
			Circles: circles,
			Color:   color,
		},
		velocities: velocities,
	}
}

func NewGame() (*Game, error) {
	groups := []movingGroup{
		newMovingGroup(metaballs.NewColorScale(1, 0.2, 0.2, 1), circlesPerGroup),
		newMovingGroup(metaballs.NewColorScale(0.2, 0.4, 1, 1), circlesPerGroup),
		newMovingGroup(metaballs.NewColorScale(0.2, 1, 0.4, 1), circlesPerGroup),
	}

	// Capacity tiers: shader pool from small (cheap, common case for sparse
	// tiles) up to large (rare, dense tiles after subdivision).
	tiers := []metaballs.ShaderConfig{
		{MainCircles: 16, OtherCircles: 64, SmoothK: smoothK, LightDirX: lightDirX, LightDirY: lightDirY, EdgeThickness: edgeThickness},
		{MainCircles: 64, OtherCircles: 256, SmoothK: smoothK, LightDirX: lightDirX, LightDirY: lightDirY, EdgeThickness: edgeThickness},
		{MainCircles: 256, OtherCircles: 768, SmoothK: smoothK, LightDirX: lightDirX, LightDirY: lightDirY, EdgeThickness: edgeThickness},
	}

	renderer, err := metaballs.NewRenderer(metaballs.RendererConfig{
		Tiers:       tiers,
		RootCols:    8,
		RootRows:    8,
		MaxDepth:    3,
		MinTileSize: 0.01,
		Padding:     2 * smoothK,
	})
	if err != nil {
		return nil, err
	}

	return &Game{renderer: renderer, groups: groups}, nil
}

func (g *Game) Update() error {
	const dt = 1.0 / 60.0

	for gi := range g.groups {
		mg := &g.groups[gi]

		for i := range mg.group.Circles {
			c := &mg.group.Circles[i]
			v := &mg.velocities[i]

			c.X += v.X * dt
			c.Y += v.Y * dt

			if c.X-c.Radius < 0 {
				c.X = c.Radius
				v.X = -v.X
			} else if c.X+c.Radius > 1 {
				c.X = 1 - c.Radius
				v.X = -v.X
			}

			if c.Y-c.Radius < 0 {
				c.Y = c.Radius
				v.Y = -v.Y
			} else if c.Y+c.Radius > 1 {
				c.Y = 1 - c.Radius
				v.Y = -v.Y
			}
		}
	}

	return nil
}

func (g *Game) Draw(screen *ebiten.Image) {
	groups := make([]metaballs.Group, len(g.groups))
	for i, mg := range g.groups {
		groups[i] = mg.group
	}

	stats, err := g.renderer.Draw(screen, groups)
	if err != nil {
		panic(err)
	}
	g.lastStats = stats

	totalCircles := 0
	for _, mg := range g.groups {
		totalCircles += len(mg.group.Circles)
	}

	ebitenutil.DebugPrint(screen, fmt.Sprintf(
		"FPS: %0.1f  TPS: %0.1f\ncircles: %d\ntiles drawn: %d  skipped: %d\ncircles clipped: %d",
		ebiten.ActualFPS(), ebiten.ActualTPS(),
		totalCircles,
		g.lastStats.TilesDrawn, g.lastStats.TilesSkipped,
		g.lastStats.CirclesClipped,
	))
}

func (g *Game) Layout(outsideWidth, outsideHeight int) (int, int) {
	return screenWidth, screenHeight
}

func main() {
	rand.Seed(time.Now().UnixNano())

	game, err := NewGame()
	if err != nil {
		log.Fatal(err)
	}

	ebiten.SetWindowSize(screenWidth, screenHeight)
	ebiten.SetWindowTitle("Metaballs - Many Circles")

	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}
