// Water renders a metaball geometry texture, then shades it in a custom pass.
package main

import (
	_ "embed"
	"fmt"
	"log"
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	metaballs "github.com/razzie/ebiten-metaballs"
)

const (
	width, height = 1080, 720
	smoothK       = 28
	circleCount   = 16
)

//go:embed water.kage
var waterSource []byte

type Game struct {
	geometry *ebiten.Image
	metaball *metaballs.MetaballShader
	water    *ebiten.Shader
	groups   []metaballs.Group
	xform    metaballs.UVTransform
	ticks    int
	paused   bool
	view     int
	dragging int
	uniforms map[string]any
}

func NewGame() (*Game, error) {
	groups := []metaballs.Group{{Circles: make([]metaballs.Circle, circleCount)}}
	geometryShader, err := metaballs.NewMetaballShader(metaballs.ShaderConfig{
		ShaderCapacity: metaballs.CapacityForGroups(groups),
		ShaderCommonConfig: metaballs.ShaderCommonConfig{
			SmoothK: smoothK, GeometryBuffer: true,
		},
	})
	if err != nil {
		return nil, err
	}
	waterShader, err := ebiten.NewShader(waterSource)
	if err != nil {
		return nil, fmt.Errorf("compile water shader: %w", err)
	}
	g := &Game{
		geometry: ebiten.NewImage(width, height), metaball: geometryShader,
		water: waterShader, groups: groups, dragging: -1,
		uniforms: map[string]any{"GeometryK": float32(smoothK)},
	}
	// Pixel-sized UV units let the water shader express refraction and foam in pixels.
	g.xform.SetScale(1, 1)
	g.animate()
	return g, nil
}

func (g *Game) animate() {
	t := float64(g.ticks) / 60
	for i := range g.groups[0].Circles {
		a := float64(i) * 2 * math.Pi / 10
		if i < 10 {
			g.groups[0].Circles[i] = metaballs.Circle{
				X:      float32(540 + 225*math.Cos(a+t*0.24) + 36*math.Sin(a*3-t*0.7)),
				Y:      float32(365 + 90*math.Sin(a*2+t*0.45) + 45*math.Sin(a-t*0.3)),
				Radius: float32(66 + 15*math.Sin(a*2+t*0.8)),
			}
		} else {
			a = float64(i-10)*math.Pi/3 + t*0.18
			g.groups[0].Circles[i] = metaballs.Circle{
				X:      float32(540 + (275+35*math.Sin(t*0.7+a))*math.Cos(a)),
				Y:      float32(365 + (190+35*math.Cos(t*0.6+a))*math.Sin(a)),
				Radius: float32(22 + 10*(0.5+0.5*math.Sin(a*3-t))),
			}
		}
	}
}

func (g *Game) Update() error {
	if inpututil.IsKeyJustPressed(ebiten.KeySpace) {
		g.paused = !g.paused
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyG) {
		g.view = (g.view + 1) % 4
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyR) {
		g.ticks, g.dragging, g.paused = 0, -1, false
		g.animate()
	}
	mx, my := ebiten.CursorPosition()
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		for i, c := range g.groups[0].Circles {
			dx, dy := float32(mx)-c.X, float32(my)-c.Y
			if dx*dx+dy*dy < c.Radius*c.Radius {
				g.dragging, g.paused = i, true
				break
			}
		}
	}
	if !ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) {
		g.dragging = -1
	}
	if g.dragging >= 0 {
		c := &g.groups[0].Circles[g.dragging]
		c.X, c.Y = float32(mx), float32(my)
	} else if !g.paused {
		g.ticks++
		g.animate()
	}
	return nil
}

func (g *Game) Draw(screen *ebiten.Image) {
	// Discarded pixels leave the target untouched, so clear stale geometry every frame.
	g.geometry.Clear()
	if err := g.metaball.Draw(g.geometry, g.groups, g.xform); err != nil {
		ebitenutil.DebugPrint(screen, err.Error())
		return
	}
	g.uniforms["Time"] = float32(g.ticks) / 60
	g.uniforms["View"] = g.view
	screen.DrawRectShader(width, height, g.water, &ebiten.DrawRectShaderOptions{
		Images: [4]*ebiten.Image{g.geometry}, Uniforms: g.uniforms,
	})
	views := [...]string{"WATER", "GRADIENT", "DISTANCE", "BLENDED RADIUS"}
	ebitenutil.DebugPrintAt(screen, "LIQUID / "+views[g.view]+"\nDrag a droplet | Space: pause / resume | G: inspect geometry | R: reset", 28, 26)
	ebitenutil.DebugPrintAt(screen, "Metaball geometry -> refraction / ripples / reflections / shoreline foam", 28, height-40)
}

func (*Game) Layout(int, int) (int, int) { return width, height }

func main() {
	g, err := NewGame()
	if err != nil {
		log.Fatal(err)
	}
	ebiten.SetWindowSize(width, height)
	ebiten.SetWindowTitle("Metaballs - Water")
	if err := ebiten.RunGame(g); err != nil {
		log.Fatal(err)
	}
}
