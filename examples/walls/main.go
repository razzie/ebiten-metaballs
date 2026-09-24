// The wall demo deliberately moves circle geometry into fixed segments so
// the shader's deformation is visible. The faint outlines show input circles.
package main

import (
	"image/color"
	"log"
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/vector"
	metaballs "github.com/razzie/ebiten-metaballs"
)

const width, height = 1080, 640

var wallX = [...]float32{170, 530, 890}

type Game struct {
	renderer *metaballs.Renderer
	groups   []metaballs.Group
	xform    metaballs.UVTransform
	ticks    int
	paused   bool
	dragging int
}

func NewGame() (*Game, error) {
	g := &Game{dragging: -1, groups: []metaballs.Group{
		{Circles: make([]metaballs.Circle, 1), Color: metaballs.NewColorScale(0.2, 0.85, 0.7, 1)},
		{Circles: make([]metaballs.Circle, 2), Color: metaballs.NewColorScale(1, 0.35, 0.25, 1)},
	}}
	for i, x := range wallX {
		thickness := float32(24)
		if i == 2 {
			thickness = 0
		}
		g.groups[0].Walls = append(g.groups[0].Walls, metaballs.Wall{AX: x, AY: 190, BX: x, BY: 500, Thickness: thickness})
	}
	// Pixel-sized UV units keep example geometry and its guide outlines aligned.
	g.xform.SetScale(1, 1)
	g.animate()
	renderer, err := metaballs.NewRenderer(metaballs.RendererConfig{
		Common:   metaballs.ShaderCommonConfig{SmoothK: 28, LightDirX: -1, LightDirY: -1, EdgeThickness: 16, BorderThickness: 3, FxaaEnabled: true},
		Tiers:    []metaballs.ShaderCapacity{metaballs.CapacityForGroups(g.groups)},
		RootCols: 6, RootRows: 4, MinTileSize: 8,
	})
	if err != nil {
		return nil, err
	}
	g.renderer = renderer
	return g, nil
}

func (g *Game) circle(i int) *metaballs.Circle {
	if i == 0 {
		return &g.groups[0].Circles[0]
	}
	return &g.groups[1].Circles[i-1]
}

func (g *Game) animate() {
	phase := float64(g.ticks) / 60
	for i, x := range wallX {
		*g.circle(i) = metaballs.Circle{X: x + 65 + 55*float32(math.Cos(phase)), Y: 345 + 55*float32(math.Sin(phase*0.7)), Radius: 64}
	}
}

func (g *Game) Update() error {
	if inpututil.IsKeyJustPressed(ebiten.KeySpace) {
		g.paused = !g.paused
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyR) {
		g.ticks, g.dragging, g.paused = 0, -1, false
		g.animate()
	}
	mx, my := ebiten.CursorPosition()
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		for i := range wallX {
			c := g.circle(i)
			dx, dy := float32(mx)-c.X, float32(my)-c.Y
			if dx*dx+dy*dy <= c.Radius*c.Radius {
				g.dragging, g.paused = i, true
				break
			}
		}
	}
	if !ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) {
		g.dragging = -1
	}
	if g.dragging >= 0 {
		c := g.circle(g.dragging)
		c.X, c.Y = float32(mx), float32(my)
	} else if !g.paused {
		g.ticks++
		g.animate()
	}
	return nil
}

func (g *Game) Draw(screen *ebiten.Image) {
	screen.Fill(color.RGBA{R: 18, G: 23, B: 32, A: 255})
	guide := color.RGBA{R: 65, G: 76, B: 88, A: 255}
	for i := range wallX {
		c := g.circle(i)
		vector.StrokeCircle(screen, c.X, c.Y, c.Radius, 1, guide, true)
	}
	// The zero-width wall has no fill; a dashed guide makes its location clear.
	for y := float32(190); y < 500; y += 12 {
		vector.StrokeLine(screen, wallX[2], y, wallX[2], y+5, 1, guide, false)
	}
	if _, err := g.renderer.Draw(screen, g.groups, g.xform); err != nil {
		ebitenutil.DebugPrint(screen, err.Error())
		return
	}
	ebitenutil.DebugPrintAt(screen, "WALLS\nDrag a circle to explore | Space: pause / resume | R: reset\nFaint outlines show the original circles before squeezing.", 24, 24)
	ebitenutil.DebugPrintAt(screen, "SAME GROUP\nCircle merges into its wall", 48, 126)
	ebitenutil.DebugPrintAt(screen, "DIFFERENT GROUP\nCircle squeezes; wall stays rigid", 408, 126)
	ebitenutil.DebugPrintAt(screen, "ZERO THICKNESS\nTwo-sided line (dashed guide)", 768, 126)
	ebitenutil.DebugPrintAt(screen, "Walls use independent endpoints and optional full-width Thickness.\nThis example animates geometry directly to expose the rendering behavior.", 24, 578)
}

func (g *Game) Layout(int, int) (int, int) { return width, height }

func main() {
	game, err := NewGame()
	if err != nil {
		log.Fatal(err)
	}
	ebiten.SetWindowSize(width, height)
	ebiten.SetWindowTitle("Metaballs - Rigid Walls")
	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}
