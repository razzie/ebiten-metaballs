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
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	metaballs "github.com/razzie/ebiten-metaballs"
	"github.com/razzie/ebiten-metaballs/softbody"
)

const (
	screenSize     = 900
	ticksPerSecond = 60

	numGroups            = 3
	numClustersPerGroup  = 3
	minCirclesPerCluster = 3
	maxCirclesPerCluster = 6
	minRadius            = 0.01
	maxRadius            = 0.025
	bridgeRadius         = minRadius / 4

	cursorRadius    = 0.35
	cursorImpulse   = 3.0 / ticksPerSecond
	smoothK         = 0.015
	edgeThickness   = 0.03
	borderThickness = 0.004
)

type circleLocation struct {
	group softbody.Group
	index int
}

type Game struct {
	world          *softbody.State
	renderers      [2]*metaballs.Renderer
	activeRenderer int
	groups         []metaballs.Group
	snapshot       []softbody.CircleSnapshot
	bridges        []softbody.BridgeSnapshot
	circleIndices  map[uint64]circleLocation
	xform          metaballs.UVTransform
	bounds         metaballs.UVBounds
}

// Seed nine separated chains. The physics bridges keep each chain together,
// while every circle remains free to collide and respond to the cursor.
func populateWorld(world *softbody.State) error {
	for group := range numGroups {
		for cluster := range numClustersPerGroup {
			cx := (float32(cluster) + 0.5) / numClustersPerGroup
			cy := (float32(group) + 0.5) / numGroups
			count := minCirclesPerCluster + rand.IntN(maxCirclesPerCluster-minCirclesPerCluster+1)
			radius := 0.07 + rand.Float32()*0.04
			phase := rand.Float64() * 2 * math.Pi
			var previous uint64
			for i := range count {
				angle := phase + float64(i)*2*math.Pi/float64(count)
				outer := minRadius + rand.Float32()*(maxRadius-minRadius)
				size := outer / 0.02
				id, err := world.AddCircle(softbody.CircleSpec{
					X:           cx + radius*float32(math.Cos(angle)),
					Y:           cy + radius*float32(math.Sin(angle)),
					VX:          (rand.Float32() - 0.5) * 0.06,
					VY:          (rand.Float32() - 0.5) * 0.06,
					InnerRadius: outer * 0.75,
					OuterRadius: outer,
					Mass:        size * size,
					Group:       softbody.Group(group),
				})
				if err != nil {
					return fmt.Errorf("add circle: %w", err)
				}
				if previous != 0 {
					// Leave some slack around the starting separation. Zero
					// BreakDistance keeps chains connected during interaction.
					distance := 2 * radius * float32(math.Sin(math.Pi/float64(count)))
					_, err := world.AddBridge(softbody.BridgeSpec{
						A: previous, B: id,
						MinDistance:  distance * 0.8,
						MaxDistance:  distance * 1.2,
						AttractForce: 0.6,
						RepelForce:   0.6,
					})
					if err != nil {
						return fmt.Errorf("add bridge: %w", err)
					}
				}
				previous = id
			}
		}
	}
	return nil
}

func NewGame() (*Game, error) {
	cfg := softbody.DefaultConfig()
	cfg.Workers = 4
	cfg.Substeps = 4
	world := softbody.New(cfg)
	if err := populateWorld(world); err != nil {
		return nil, err
	}

	xform, bounds := metaballs.NewCenteredUVTransform(screenSize, screenSize)
	g := &Game{
		world:         world,
		xform:         xform,
		bounds:        bounds,
		circleIndices: make(map[uint64]circleLocation, world.Len()),
		groups: []metaballs.Group{
			{Color: metaballs.NewColorScale(1, 0.2, 0.2, 1)},
			{Color: metaballs.NewColorScale(0.2, 0.4, 1, 1)},
			{Color: metaballs.NewColorScale(0.2, 1, 0.4, 1)},
		},
	}
	g.syncGeometry()
	for i, common := range [...]metaballs.ShaderCommonConfig{
		{SmoothK: smoothK, LightDirX: 1, LightDirY: -1, EdgeThickness: edgeThickness, FxaaEnabled: true},
		{SmoothK: smoothK, BorderThickness: borderThickness, FxaaEnabled: true},
	} {
		renderer, err := metaballs.NewRenderer(metaballs.RendererConfig{
			Common: common,
			Tiers: []metaballs.ShaderCapacity{
				{Groups: numGroups, Circles: 16, Bridges: 16},
				// Fit every circle and bridge even when the cursor gathers
				// the entire world into a single tile.
				metaballs.CapacityForGroups(g.groups),
			},
			RootCols: 1, RootRows: 1,
			MaxDepth: 3, MinTileSize: 0.01,
			Workers: 4,
		})
		if err != nil {
			return nil, fmt.Errorf("create renderer %d: %w", i, err)
		}
		g.renderers[i] = renderer
	}
	return g, nil
}

func (g *Game) syncGeometry() {
	g.snapshot = g.world.Snapshot(g.snapshot)
	g.bridges = g.world.BridgeSnapshot(g.bridges)
	// Physics sorts by grid cell. Keep blending order stable and resolve
	// bridge IDs to the current group-local rendering indices.
	slices.SortFunc(g.snapshot, func(a, b softbody.CircleSnapshot) int {
		return cmp.Compare(a.ID, b.ID)
	})
	clear(g.circleIndices)
	for i := range g.groups {
		g.groups[i].Circles = g.groups[i].Circles[:0]
		g.groups[i].Bridges = g.groups[i].Bridges[:0]
	}
	for _, c := range g.snapshot {
		group := &g.groups[c.Group]
		g.circleIndices[c.ID] = circleLocation{group: c.Group, index: len(group.Circles)}
		group.Circles = append(group.Circles, metaballs.Circle{X: c.X, Y: c.Y, Radius: c.OuterRadius})
	}
	for _, b := range g.bridges {
		a, z := g.circleIndices[b.A], g.circleIndices[b.B]
		// populateWorld only connects circles of the same color.
		group := &g.groups[a.group]
		group.Bridges = append(group.Bridges, metaballs.Bridge{A: a.index, B: z.index, MiddleRadius: bridgeRadius})
	}
}

func (g *Game) Update() error {
	if inpututil.IsKeyJustPressed(ebiten.KeySpace) {
		g.activeRenderer = (g.activeRenderer + 1) % len(g.renderers)
	}
	mx, my := ebiten.CursorPosition()
	x, y := g.xform.ScreenToUV(mx, my)
	if x >= g.bounds.MinX && x < g.bounds.MaxX && y >= g.bounds.MinY && y < g.bounds.MaxY {
		var strength float32
		if ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) {
			strength -= cursorImpulse
		}
		if ebiten.IsMouseButtonPressed(ebiten.MouseButtonRight) {
			strength += cursorImpulse
		}
		if strength != 0 {
			g.world.QueueRadialImpulse(softbody.RadialImpulse{
				X: x, Y: y, Radius: cursorRadius, Strength: strength,
			})
		}
	}
	g.world.Step(1.0 / ticksPerSecond)
	g.syncGeometry()
	return nil
}

func (g *Game) Draw(screen *ebiten.Image) {
	screen.Fill(color.RGBA{R: 16, G: 18, B: 24, A: 255})
	stats, err := g.renderers[g.activeRenderer].Draw(screen, g.groups, g.xform)
	if err != nil {
		ebitenutil.DebugPrint(screen, err.Error())
		return
	}
	mode := "normal lighting"
	if g.activeRenderer == 1 {
		mode = "border thickness"
	}
	ebitenutil.DebugPrint(screen, fmt.Sprintf(
		"Hold left click: attract nearby circles | Hold right click: repel\nSpace: switch renderer | Current: %s\n%d circles | %d bridges | FPS: %.1f | TPS: %.1f\nTiles: %d | Skipped: %d | Circles clipped: %d",
		mode, g.world.Len(), len(g.bridges), ebiten.ActualFPS(), ebiten.ActualTPS(),
		stats.TilesDrawn.Load(), stats.TilesSkipped.Load(), stats.CirclesClipped.Load(),
	))
}

func (g *Game) Layout(outsideWidth, outsideHeight int) (int, int) {
	return screenSize, screenSize
}

func main() {
	game, err := NewGame()
	if err != nil {
		log.Fatal(err)
	}
	ebiten.SetWindowSize(screenSize, screenSize)
	ebiten.SetWindowTitle("Metaballs - Softbody Bridges")
	ebiten.SetTPS(ticksPerSecond)
	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}
