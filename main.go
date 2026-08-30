package main

import (
	"log"
	"math"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
)

const (
	screenWidth  = 800
	screenHeight = 800
)

type Game struct {
	shader *MetaballShader

	main  Group
	other Group

	startTime time.Time
}

func NewGame() (*Game, error) {
	// Visible red group.
	mainGroup := Group{
		Circles: []Circle{
			{X: 0.5, Y: 0.55, Radius: 0.1},
			{X: 0.5, Y: 0.40, Radius: 0.075},
		},
	}

	// Everything else gets merged into one invisible group.
	//
	// Original Shadertoy indices:
	//   2 -> other[0]
	//   3 -> other[1]
	//   4 -> other[2]
	//   5 -> other[3]
	//   6 -> other[4]
	otherGroup := Group{
		Circles: []Circle{
			{X: 0.5, Y: 0.475, Radius: 0.075},
			{X: 0.5, Y: 0.475, Radius: 0.05},
			{X: 0.3, Y: 0.35, Radius: 0.025},
			{X: 0.2, Y: 0.8, Radius: 0.03},
			{X: 0.5, Y: 0.5, Radius: 0.05},
		},
		Bridges: []Bridge{
			// Original bridge: 2 -> 4
			{A: 0, B: 2, MiddleRadius: 0.005},

			// Original bridge: 4 -> 5
			{A: 2, B: 3, MiddleRadius: 0.005},
		},
	}

	shader, err := NewMetaballShader(mainGroup, otherGroup)
	if err != nil {
		return nil, err
	}

	return &Game{
		shader:    shader,
		main:      mainGroup,
		other:     otherGroup,
		startTime: time.Now(),
	}, nil
}

func (g *Game) Update() error {
	t := time.Since(g.startTime).Seconds()

	// Visible red metaballs.
	s3 := float32(math.Sin(t * 3))
	c2 := float32(math.Cos(t * 2))

	g.main.Circles[0].X = 0.5 + s3*0.01
	g.main.Circles[0].Y = 0.55 + s3*0.01

	g.main.Circles[1].X = 0.5 + c2*0.01
	g.main.Circles[1].Y = 0.40 - c2*0.01

	// Mouse-controlled invisible metaball.
	mx, my := ebiten.CursorPosition()

	mouseX := float32(mx) / screenWidth
	mouseY := float32(my) / screenHeight

	// Similar default position to the Shadertoy version.
	if mx == 0 && my == 0 {
		mouseX = 0.5
		mouseY = 0.475
	}

	g.other.Circles[0].X = mouseX
	g.other.Circles[0].Y = mouseY

	// Second blue metaball follows / wiggles around mouse.
	s4 := float32(math.Sin(t * 4))

	g.other.Circles[1].X = mouseX + s4*0.05
	g.other.Circles[1].Y = mouseY + s4*0.05

	// Green metaball.
	g.other.Circles[4].X =
		0.5 + float32(math.Sin(t))*0.1

	g.other.Circles[4].Y =
		0.5 + float32(math.Cos(t))*0.15

	return nil
}

func (g *Game) Draw(screen *ebiten.Image) {
	// Anything drawn before the metaballs remains visible wherever
	// the Kage shader executes discard().
	//
	// For example:
	//
	// screen.Fill(color.RGBA{30, 30, 30, 255})

	err := g.shader.Draw(
		screen,
		g.main,
		g.other,

		// Main metaball color.
		[3]float32{1, 0, 0},

		// Smooth-min radius.
		0.1,
	)
	if err != nil {
		panic(err)
	}
}

func (g *Game) Layout(outsideWidth, outsideHeight int) (int, int) {
	return screenWidth, screenHeight
}

func main() {
	game, err := NewGame()
	if err != nil {
		log.Fatal(err)
	}

	ebiten.SetWindowSize(screenWidth, screenHeight)
	ebiten.SetWindowTitle("Metaballs")

	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}
