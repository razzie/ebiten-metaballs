package main

import (
	"image"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"log"
	"math"
	"math/rand"
	"os"
	"time"

	metaballs "github.com/razzie/ebiten-metaballs"

	"github.com/hajimehoshi/ebiten/v2"
)

const (
	canvasSize      = 384
	fps             = 20
	durationSeconds = 5
	frameCount      = fps * durationSeconds
	gifDelay        = 100 / fps // 100ths of a second per frame
	outputPath      = "manycircles.gif"

	circlesPerGroup = 64
	minRadius       = 0.02
	maxRadius       = 0.04
	minSpeed        = 0.025
	maxSpeed        = 0.1

	smoothK       = 0.03
	lightDirX     = 1
	lightDirY     = -1
	edgeThickness = 0.015
)

// vec2 is a plain 2D vector used for per-circle velocity.
type vec2 struct{ X, Y float32 }

// movingGroup pairs a rendered Group with the velocities driving its circles.
type movingGroup struct {
	group      metaballs.Group
	velocities []vec2
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

// step advances mg's circles by dt, bouncing off the [0,1] unit square.
func (mg *movingGroup) step(dt float32) {
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

// Game drives a one-shot capture: the whole animation is rendered and
// written to disk during the first Draw call, then the process exits.
type Game struct {
	shader    *metaballs.MetaballShader
	groups    []movingGroup
	offscreen *ebiten.Image
	captured  bool
}

func NewGame() (*Game, error) {
	groups := []movingGroup{
		newMovingGroup(metaballs.NewColorScale(1, 0.2, 0.2, 1), circlesPerGroup),
		newMovingGroup(metaballs.NewColorScale(0.2, 0.4, 1, 1), circlesPerGroup),
		newMovingGroup(metaballs.NewColorScale(0.2, 1, 0.4, 1), circlesPerGroup),
	}

	shaderGroups := make([]metaballs.Group, len(groups))
	for i, mg := range groups {
		shaderGroups[i] = mg.group
	}

	shader, err := metaballs.NewMetaballShader(metaballs.ShaderConfig{
		ShaderCapacity: metaballs.CapacityForGroups(shaderGroups),
		ShaderCommonConfig: metaballs.ShaderCommonConfig{
			SmoothK:       smoothK,
			LightDirX:     lightDirX,
			LightDirY:     lightDirY,
			EdgeThickness: edgeThickness,
		},
	})
	if err != nil {
		return nil, err
	}

	return &Game{
		shader:    shader,
		groups:    groups,
		offscreen: ebiten.NewImage(canvasSize, canvasSize),
	}, nil
}

// capture renders frameCount frames, encodes them as an animated GIF at
// outputPath, and exits the process.
func (g *Game) capture() {
	anim := &gif.GIF{}
	pix := make([]byte, 4*canvasSize*canvasSize)
	rect := image.Rect(0, 0, canvasSize, canvasSize)

	shaderGroups := make([]metaballs.Group, len(g.groups))

	for frame := 0; frame < frameCount; frame++ {
		for i := range g.groups {
			g.groups[i].step(1.0 / fps)
			shaderGroups[i] = g.groups[i].group
		}

		g.offscreen.Clear()
		if err := g.shader.Draw(g.offscreen, shaderGroups); err != nil {
			log.Fatal(err)
		}

		g.offscreen.ReadPixels(pix)
		rgba := &image.RGBA{Pix: pix, Stride: 4 * canvasSize, Rect: rect}

		paletted := image.NewPaletted(rect, palette.Plan9)
		draw.Draw(paletted, rect, rgba, image.Point{}, draw.Src)

		anim.Image = append(anim.Image, paletted)
		anim.Delay = append(anim.Delay, gifDelay)
	}

	f, err := os.Create(outputPath)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	if err := gif.EncodeAll(f, anim); err != nil {
		log.Fatal(err)
	}

	log.Printf("wrote %s (%d frames)", outputPath, frameCount)
	os.Exit(0)
}

func (g *Game) Update() error {
	return nil
}

func (g *Game) Draw(screen *ebiten.Image) {
	if !g.captured {
		g.captured = true
		g.capture()
		return
	}

	screen.DrawImage(g.offscreen, nil)
}

func (g *Game) Layout(outsideWidth, outsideHeight int) (int, int) {
	return canvasSize, canvasSize
}

func main() {
	rand.Seed(time.Now().UnixNano())

	game, err := NewGame()
	if err != nil {
		log.Fatal(err)
	}

	ebiten.SetWindowSize(canvasSize, canvasSize)
	ebiten.SetWindowTitle("Metaballs - GIF Export")

	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}
