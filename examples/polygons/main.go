// Polygons share one boundary between ordinary Ebiten drawing, shader walls,
// and softbody collision geometry. World coordinates are logical pixels.
package main

import (
	"cmp"
	"fmt"
	"image/color"
	"log"
	"math"
	"slices"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/vector"
	metaballs "github.com/razzie/ebiten-metaballs"
	"github.com/razzie/ebiten-metaballs/softbody"
)

const (
	width, height  = 1100, 760
	ticksPerSecond = 60
	circleGroups   = 3
	wallGroup      = circleGroups
)

var sceneBounds = softbody.Bounds{MinX: 24, MinY: 110, MaxX: width - 24, MaxY: height - 56}

type Game struct {
	world    *softbody.State
	renderer *metaballs.Renderer
	xform    metaballs.UVTransform
	groups   []metaballs.Group
	snapshot []softbody.CircleSnapshot
	polygons []softbody.PolygonSnapshot
	paths    []vector.Path
	guides   bool
}

func NewGame() (*Game, error) {
	g := &Game{groups: []metaballs.Group{
		{Color: metaballs.NewColorScale(0.2, 0.85, 0.7, 1)},
		{Color: metaballs.NewColorScale(1, 0.4, 0.3, 1)},
		{Color: metaballs.NewColorScale(0.4, 0.6, 1, 1)},
		// Use a different group from every circle. Transparency also hides the
		// small smooth unions between zero-width walls at polygon corners.
		{Color: metaballs.NewColorScale(0, 0, 0, 0)},
	}}
	g.xform.SetScale(1, 1)
	if err := g.reset(); err != nil {
		return nil, err
	}
	renderer, err := metaballs.NewRenderer(metaballs.RendererConfig{
		Common: metaballs.ShaderCommonConfig{
			SmoothK: 24, LightDirX: -1, LightDirY: -1,
			EdgeThickness: 12, BorderThickness: 2, FxaaEnabled: true,
		},
		// Fit the entire scene, including every polygon edge, even in one tile.
		Tiers:    []metaballs.ShaderCapacity{metaballs.CapacityForGroups(g.groups)},
		RootCols: 4, RootRows: 3, MinTileSize: 8, Workers: 4,
	})
	if err != nil {
		return nil, err
	}
	g.renderer = renderer
	return g, nil
}

func (g *Game) reset() error {
	cfg := softbody.DefaultConfig()
	cfg.Workers, cfg.Substeps = 4, 4
	cfg.LinearDamping = 0.8
	g.world = softbody.New(cfg)
	if err := g.world.SetBounds(sceneBounds); err != nil {
		return err
	}
	for _, points := range [][]softbody.Point{
		// A 50-pixel throat fits 24-pixel cores, but squeezes 68-pixel shells.
		{{250, 280}, {525, 390}, {525, 430}, {250, 350}},
		{{850, 280}, {850, 350}, {575, 430}, {575, 390}},
		// A concave L and a diamond demonstrate corners and sloping contacts.
		{{230, 480}, {430, 480}, {430, 515}, {270, 515}, {270, 640}, {230, 640}},
		{{810, 470}, {910, 555}, {810, 640}, {710, 555}},
	} {
		if _, err := g.world.AddPolygon(points); err != nil {
			return fmt.Errorf("add polygon: %w", err)
		}
	}
	// Both render representations come from validated physics snapshots.
	g.polygons = g.world.PolygonSnapshot(g.polygons)
	g.paths = make([]vector.Path, len(g.polygons))
	walls := g.groups[wallGroup].Walls[:0]
	for i, polygon := range g.polygons {
		points := polygon.Points
		g.paths[i].MoveTo(points[0].X, points[0].Y)
		for j, a := range points {
			b := points[(j+1)%len(points)]
			walls = append(walls, metaballs.Wall{AX: a.X, AY: a.Y, BX: b.X, BY: b.Y})
			if j > 0 {
				g.paths[i].LineTo(a.X, a.Y)
			}
		}
		g.paths[i].Close()
	}
	// Match the enclosing softbody bounds as well as the polygon obstacles.
	b := sceneBounds
	frame := [...]softbody.Point{{b.MinX, b.MinY}, {b.MaxX, b.MinY}, {b.MaxX, b.MaxY}, {b.MinX, b.MaxY}}
	for i, a := range frame {
		b := frame[(i+1)%len(frame)]
		walls = append(walls, metaballs.Wall{AX: a.X, AY: a.Y, BX: b.X, BY: b.Y})
	}
	g.groups[wallGroup].Walls = walls
	for row := range 2 {
		for col := range 7 {
			if _, err := g.world.AddCircle(softbody.CircleSpec{
				X: 334 + float32(col)*72, Y: 153 + float32(row)*72,
				InnerRadius: 12, OuterRadius: 34, Mass: 1,
				Group: softbody.Group(col % circleGroups),
			}); err != nil {
				return err
			}
		}
	}
	g.syncCircles()
	return nil
}

func (g *Game) syncCircles() {
	g.snapshot = g.world.Snapshot(g.snapshot)
	// Grid sorting must not change the shader's blend order.
	slices.SortFunc(g.snapshot, func(a, b softbody.CircleSnapshot) int { return cmp.Compare(a.ID, b.ID) })
	for i := range circleGroups {
		g.groups[i].Circles = g.groups[i].Circles[:0]
	}
	for _, c := range g.snapshot {
		g.groups[c.Group].Circles = append(g.groups[c.Group].Circles, metaballs.Circle{X: c.X, Y: c.Y, Radius: c.OuterRadius})
	}
}

func (g *Game) step() {
	// A gentle attraction toward the bottom feeds circles through the funnel.
	g.world.QueueRadialImpulse(softbody.RadialImpulse{
		X: width / 2, Y: height, Radius: float32(math.Inf(1)), Strength: -160.0 / ticksPerSecond,
	})
	g.world.Step(1.0 / ticksPerSecond)
	g.syncCircles()
}

func (g *Game) Update() error {
	if inpututil.IsKeyJustPressed(ebiten.KeyR) {
		return g.reset()
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyD) {
		g.guides = !g.guides
	}
	mx, my := ebiten.CursorPosition()
	x, y := g.xform.ScreenToUV(mx, my)
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		g.world.Drag(softbody.DragSpec{X: x, Y: y, MaxCircles: 1})
	}
	g.world.Carry(x, y)
	if !ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) {
		g.world.Drop()
	}
	if ebiten.IsMouseButtonPressed(ebiten.MouseButtonRight) {
		g.world.QueueRadialImpulse(softbody.RadialImpulse{X: x, Y: y, Radius: 800, Strength: -1600.0 / ticksPerSecond})
	}
	if ebiten.IsKeyPressed(ebiten.KeySpace) {
		g.world.QueueRadialImpulse(softbody.RadialImpulse{X: x, Y: y, Radius: 500, Strength: 2400.0 / ticksPerSecond})
	}
	g.step()
	return nil
}

func (g *Game) Draw(screen *ebiten.Image) {
	screen.Fill(color.RGBA{R: 18, G: 23, B: 32, A: 255})
	if _, err := g.renderer.Draw(screen, g.groups, g.xform); err != nil {
		ebitenutil.DebugPrint(screen, err.Error())
		return
	}
	if g.guides {
		for _, c := range g.snapshot {
			vector.StrokeCircle(screen, c.X, c.Y, c.OuterRadius, 1, color.RGBA{90, 110, 130, 255}, true)
			vector.StrokeCircle(screen, c.X, c.Y, c.InnerRadius, 1, color.RGBA{220, 230, 240, 255}, true)
		}
	}
	// Shader walls are two-sided segments, not solid polygon fields. Opaque
	// polygon fills cover any part of a shell extending into a solid interior.
	for i := range g.paths {
		vector.FillPath(screen, &g.paths[i], nil, &vector.DrawPathOptions{
			ColorScale: metaballs.NewColorScale(0.22, 0.28, 0.36, 1), AntiAlias: true,
		})
		vector.StrokePath(screen, &g.paths[i], &vector.StrokeOptions{Width: 2}, &vector.DrawPathOptions{
			ColorScale: metaballs.NewColorScale(0.5, 0.62, 0.72, 1), AntiAlias: true,
		})
	}
	b := sceneBounds
	vector.StrokeRect(screen, b.MinX, b.MinY, b.MaxX-b.MinX, b.MaxY-b.MinY, 1, color.RGBA{65, 80, 100, 255}, true)
	ebitenutil.DebugPrintAt(screen, "POLYGON PLAYGROUND\nLeft drag: grab a circle | Right hold: pull | Space: push away\nD: show shells and solid cores | R: reset", 24, 24)
	ebitenutil.DebugPrintAt(screen, "50px throat", 510, 450)
	ebitenutil.DebugPrintAt(screen, fmt.Sprintf("%d circles | %d solid polygons | Gentle pull toward the bottom | FPS: %.0f", g.world.Len(), len(g.polygons), ebiten.ActualFPS()), 24, height-32)
}

func (g *Game) Layout(int, int) (int, int) { return width, height }

func main() {
	game, err := NewGame()
	if err != nil {
		log.Fatal(err)
	}
	ebiten.SetWindowSize(width, height)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	ebiten.SetWindowTitle("Metaballs - Polygon Playground")
	ebiten.SetTPS(ticksPerSecond)
	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}
