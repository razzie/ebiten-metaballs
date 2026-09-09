package main

import (
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"log"
	"math"
	mrand "math/rand"
	"os"

	metaballs "github.com/razzie/ebiten-metaballs"

	"github.com/hajimehoshi/ebiten/v2"
)

const (
	seed = 12345

	canvasSize      = 384
	fps             = 16
	durationSeconds = 20
	frameCount      = fps * durationSeconds
	gifDelay        = 100 / fps // 100ths of a second per frame
	outputPath      = "demo.gif"

	blobsPerGroup   = 18
	orbitersPerBlob = 1

	minMainRadius = 0.03
	maxMainRadius = 0.06

	minOrbiterRadius = 0.02
	maxOrbiterRadius = 0.03

	minOrbitDistance = 0.025
	maxOrbitDistance = 0.055

	smoothK       = 0.06
	lightDirX     = 1
	lightDirY     = -1
	edgeThickness = 0.03
)

var rand = mrand.New(mrand.NewSource(seed))

var gifPalette = []color.Color{
	color.Transparent,
	// reds
	hexRGB(0x990000),
	hexRGB(0xAA0000),
	hexRGB(0xCC4444),
	hexRGB(0xDD4949),
	hexRGB(0xEE4F4F),
	// greens
	hexRGB(0x00994C),
	hexRGB(0x00AA55),
	hexRGB(0x44CC44),
	hexRGB(0x49DD49),
	hexRGB(0x4FEE4F),
	// blues
	hexRGB(0x004C99),
	hexRGB(0x0055AA),
	hexRGB(0x4444CC),
	hexRGB(0x4949DD),
	hexRGB(0x4F4FEE),
}

func hexRGB(clr uint64) color.RGBA {
	r := uint8(clr >> 16)
	g := uint8(clr >> 8)
	b := uint8(clr)
	return color.RGBA{R: r, G: g, B: b, A: 255}
}

func randRange(min, max float32) float32 {
	return min + rand.Float32()*(max-min)
}

type vec2 struct {
	X, Y float32
}

type pathMotion struct {
	points         []vec2
	segmentLengths []float32
	totalLength    float32
	phase          float32
}

type orbiterMotion struct {
	circleIndex int

	distance  float32
	phase     float32
	frequency int
}

type blobMotion struct {
	mainCircleIndex int
	path            pathMotion
	orbiters        []orbiterMotion
}

type movingGroup struct {
	group metaballs.Group
	blobs []blobMotion
}

func distance(a, b vec2) float32 {
	dx := b.X - a.X
	dy := b.Y - a.Y
	return float32(math.Sqrt(float64(dx*dx + dy*dy)))
}

func lerp(a, b vec2, t float32) vec2 {
	return vec2{
		X: a.X + (b.X-a.X)*t,
		Y: a.Y + (b.Y-a.Y)*t,
	}
}

func newPathMotion(points []vec2) pathMotion {
	if rand.Intn(2) != 0 {
		for i, j := 0, len(points)-1; i < j; i, j = i+1, j-1 {
			points[i], points[j] = points[j], points[i]
		}
	}

	lengths := make([]float32, len(points))
	var total float32

	for i := range points {
		a := points[i]
		b := points[(i+1)%len(points)]

		l := distance(a, b)
		lengths[i] = l
		total += l
	}

	return pathMotion{
		points:         points,
		segmentLengths: lengths,
		totalLength:    total,
		phase:          rand.Float32(),
	}
}

func (m *pathMotion) position(loopT float32) vec2 {
	loopT += m.phase
	loopT -= float32(math.Floor(float64(loopT)))

	d := loopT * m.totalLength

	for i, l := range m.segmentLengths {
		if d < l {
			return lerp(
				m.points[i],
				m.points[(i+1)%len(m.points)],
				d/l,
			)
		}

		d -= l
	}

	return m.points[0]
}

func edgePoint(edge int, margin float32) vec2 {
	switch edge {
	case 0:
		return vec2{
			X: randRange(margin, 1-margin),
			Y: margin,
		}

	case 1:
		return vec2{
			X: 1 - margin,
			Y: randRange(margin, 1-margin),
		}

	case 2:
		return vec2{
			X: randRange(margin, 1-margin),
			Y: 1 - margin,
		}

	default:
		return vec2{
			X: margin,
			Y: randRange(margin, 1-margin),
		}
	}
}

func trianglePath(margin float32) []vec2 {
	skip := rand.Intn(4)

	points := make([]vec2, 0, 3)

	for edge := 0; edge < 4; edge++ {
		if edge != skip {
			points = append(points, edgePoint(edge, margin))
		}
	}

	return points
}

func quadrilateralPath(margin float32) []vec2 {
	return []vec2{
		edgePoint(0, margin),
		edgePoint(1, margin),
		edgePoint(2, margin),
		edgePoint(3, margin),
	}
}

func newMovingGroup(color ebiten.ColorScale, blobCount int) movingGroup {
	circleCount := blobCount * (1 + orbitersPerBlob)

	circles := make([]metaballs.Circle, 0, circleCount)
	blobs := make([]blobMotion, 0, blobCount)

	for range blobCount {
		mainRadius := randRange(minMainRadius, maxMainRadius)

		mainIndex := len(circles)

		circles = append(circles, metaballs.Circle{
			Radius: mainRadius,
		})

		orbiters := make([]orbiterMotion, orbitersPerBlob)

		var maxExtent float32

		for j := range orbiters {
			radius := randRange(minOrbiterRadius, maxOrbiterRadius)
			orbitDistance := randRange(minOrbitDistance, maxOrbitDistance)

			circleIndex := len(circles)

			circles = append(circles, metaballs.Circle{
				Radius: radius,
			})

			orbiters[j] = orbiterMotion{
				circleIndex: circleIndex,
				distance:    orbitDistance,
				phase:       randRange(0, 2*math.Pi),

				// Integer cycles per GIF loop are important.
				frequency: 1 + rand.Intn(3),
			}

			extent := orbitDistance + radius
			if extent > maxExtent {
				maxExtent = extent
			}
		}

		// Ensure the whole metaball cluster remains on screen.
		margin := mainRadius
		if maxExtent > margin {
			margin = maxExtent
		}

		margin += 0.01

		var points []vec2

		if rand.Intn(2) == 0 {
			points = trianglePath(margin)
		} else {
			points = quadrilateralPath(margin)
		}

		blobs = append(blobs, blobMotion{
			mainCircleIndex: mainIndex,
			path:            newPathMotion(points),
			orbiters:        orbiters,
		})
	}

	mg := movingGroup{
		group: metaballs.Group{
			Circles: circles,
			Color:   color,
		},
		blobs: blobs,
	}

	mg.setTime(0)

	return mg
}

func (mg *movingGroup) setTime(t float32) {
	loopT := t / durationSeconds
	tau := float32(2 * math.Pi)

	for i := range mg.blobs {
		blob := &mg.blobs[i]

		center := blob.path.position(loopT)

		main := &mg.group.Circles[blob.mainCircleIndex]
		main.X = center.X
		main.Y = center.Y

		for j := range blob.orbiters {
			o := &blob.orbiters[j]

			angle :=
				o.phase +
					tau*float32(o.frequency)*loopT

			c := &mg.group.Circles[o.circleIndex]

			c.X = center.X +
				o.distance*float32(math.Cos(float64(angle)))

			c.Y = center.Y +
				o.distance*float32(math.Sin(float64(angle)))
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
		newMovingGroup(
			metaballs.NewColorScale(1, 0.2, 0.2, 1),
			blobsPerGroup,
		),
		newMovingGroup(
			metaballs.NewColorScale(0.2, 0.4, 1, 1),
			blobsPerGroup,
		),
		newMovingGroup(
			metaballs.NewColorScale(0.2, 1, 0.4, 1),
			blobsPerGroup,
		),
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
	anim := &gif.GIF{
		BackgroundIndex: 0,
	}

	pix := make([]byte, 4*canvasSize*canvasSize)
	rect := image.Rect(0, 0, canvasSize, canvasSize)

	shaderGroups := make([]metaballs.Group, len(g.groups))

	for frame := 0; frame < frameCount; frame++ {
		// Do not include t == durationSeconds.
		//
		// The last frame is immediately followed by frame 0 when the GIF
		// loops, while the mathematical animation itself satisfies:
		//
		//     position(durationSeconds) == position(0)
		//
		// This avoids duplicating the first frame at the loop boundary.
		t := float32(frame) / fps

		for i := range g.groups {
			g.groups[i].setTime(t)
			shaderGroups[i] = g.groups[i].group
		}

		g.offscreen.Clear()

		if err := g.shader.Draw(g.offscreen, shaderGroups); err != nil {
			log.Fatal(err)
		}

		g.offscreen.ReadPixels(pix)
		rgba := &image.RGBA{
			Pix:    pix,
			Stride: 4 * canvasSize,
			Rect:   rect,
		}

		paletted := image.NewPaletted(rect, gifPalette)
		draw.Draw(paletted, rect, rgba, image.Point{}, draw.Src)

		anim.Image = append(anim.Image, paletted)
		anim.Delay = append(anim.Delay, gifDelay)
		anim.Disposal = append(anim.Disposal, gif.DisposalBackground)
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
