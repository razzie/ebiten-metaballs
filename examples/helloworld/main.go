package main

import (
	"log"
	"math"
	"time"

	metaballs "github.com/razzie/ebiten-metaballs"

	"github.com/hajimehoshi/ebiten/v2"
)

const (
	screenWidth  = 800
	screenHeight = 800
)

const (
	groupRed = iota
	groupBlue
	groupGreen
)

type Game struct {
	shader *metaballs.MetaballShader

	groups []metaballs.Group

	startTime time.Time
}

func NewGame() (*Game, error) {
	groups := []metaballs.Group{
		groupRed: {
			Circles: []metaballs.Circle{
				{X: 0.5, Y: 0.55, Radius: 0.1},
				{X: 0.5, Y: 0.40, Radius: 0.075},
			},
			Color: metaballs.NewColorScale(1, 0, 0, 1),
		},
		groupBlue: {
			Circles: []metaballs.Circle{
				{X: 0.5, Y: 0.475, Radius: 0.075},
				{X: 0.5, Y: 0.475, Radius: 0.05},
			},
			Color: metaballs.NewColorScale(0, 0, 1, 1),
		},
		groupGreen: {
			Circles: []metaballs.Circle{
				{X: 0.5, Y: 0.5, Radius: 0.05},
			},
			Color: metaballs.NewColorScale(0, 1, 0, 1),
		},
	}

	shader, err := metaballs.NewMetaballShader(metaballs.ShaderConfig{
		ShaderCapacity:     metaballs.CapacityForGroups(groups),
		ShaderCommonConfig: metaballs.ShaderCommonConfig{SmoothK: 0.1, LightDirX: 1, LightDirY: 0, EdgeThickness: 0.04},
	})
	if err != nil {
		return nil, err
	}

	return &Game{
		shader:    shader,
		groups:    groups,
		startTime: time.Now(),
	}, nil
}

func (g *Game) Update() error {
	t := time.Since(g.startTime).Seconds()

	// Visible red metaballs.
	s3 := float32(math.Sin(t * 3))
	c2 := float32(math.Cos(t * 2))

	red := &g.groups[groupRed]
	red.Circles[0].X = 0.5 + s3*0.01
	red.Circles[0].Y = 0.55 + s3*0.01

	red.Circles[1].X = 0.5 + c2*0.01
	red.Circles[1].Y = 0.40 - c2*0.01

	// Mouse-controlled blue metaball.
	mx, my := ebiten.CursorPosition()

	mouseX := float32(mx) / screenWidth
	mouseY := float32(my) / screenHeight

	// Similar default position to the Shadertoy version.
	if mx == 0 && my == 0 {
		mouseX = 0.5
		mouseY = 0.475
	}

	blue := &g.groups[groupBlue]
	blue.Circles[0].X = mouseX
	blue.Circles[0].Y = mouseY

	// Second blue metaball follows / wiggles around mouse.
	s4 := float32(math.Sin(t * 4))

	blue.Circles[1].X = mouseX + s4*0.05
	blue.Circles[1].Y = mouseY + s4*0.05

	// Green metaball.
	green := &g.groups[groupGreen]
	green.Circles[0].X = 0.5 + float32(math.Sin(t))*0.1
	green.Circles[0].Y = 0.5 + float32(math.Cos(t))*0.15

	return nil
}

func (g *Game) Draw(screen *ebiten.Image) {
	// Anything drawn before the metaballs remains visible wherever
	// the Kage shader executes discard().
	//
	// For example:
	//
	// screen.Fill(color.RGBA{30, 30, 30, 255})

	if err := g.shader.Draw(screen, g.groups); err != nil {
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
