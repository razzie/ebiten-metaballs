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

	numClustersPerGroup  = 3 // per color group; few clusters -> large empty gaps between them
	minCirclesPerCluster = 3
	maxCirclesPerCluster = 6

	baseClusterRadius = 0.15 // cluster occupied-area range is independent of circle count
	maxClusterRadius  = 0.5

	minRadius = 0.01
	maxRadius = 0.025
	minSpeed  = 0.015 // cluster center roaming speed
	maxSpeed  = 0.05

	minLocalSpeed = 0.02 // circle jitter speed within its cluster's disc
	maxLocalSpeed = 0.05

	clusterBridgeRadius = minRadius / 4

	smoothK       = 0.015
	lightDirX     = 1
	lightDirY     = -1
	edgeThickness = 0.03
)

// clusterCircle is a circle's position/velocity relative to its cluster's center.
type clusterCircle struct {
	localX, localY float32
	velX, velY     float32
	radius         float32
}

// cluster is a compact, roaming blob: circles jitter within radius of the
// moving center but never drift outside it, so bridges between them stay short.
type cluster struct {
	centerX, centerY float32
	velX, velY       float32
	radius           float32
	circles          []clusterCircle
}

// movingGroup pairs a rendered Group with the cluster physics state driving
// it. group.Circles is the flattened, reused-each-frame position buffer;
// group.Bridges is precomputed once since cluster membership never changes.
type movingGroup struct {
	group         metaballs.Group
	clusters      []cluster
	clusterOffset []int // start index of each cluster's circles in group.Circles
}

type Game struct {
	renderer *metaballs.Renderer
	groups   []movingGroup

	lastStats *metaballs.Stats
}

func randRange(min, max float32) float32 {
	return min + rand.Float32()*(max-min)
}

// randInDisc returns a point uniformly distributed within a disc of the given radius.
func randInDisc(radius float32) (x, y float32) {
	r := radius * float32(math.Sqrt(float64(rand.Float32())))
	theta := rand.Float32() * 2 * math.Pi
	return r * float32(math.Cos(float64(theta))), r * float32(math.Sin(float64(theta)))
}

func randVelocity(minSpeed, maxSpeed float32) (x, y float32) {
	speed := randRange(minSpeed, maxSpeed)
	angle := rand.Float32() * 2 * math.Pi
	return speed * float32(math.Cos(float64(angle))), speed * float32(math.Sin(float64(angle)))
}

func newMovingGroup(color ebiten.ColorScale, numClusters int) movingGroup {
	mg := movingGroup{
		clusters:      make([]cluster, numClusters),
		clusterOffset: make([]int, numClusters),
	}
	mg.group.Color = color

	for ci := range mg.clusters {
		// Cube the random sample so most clusters occupy a small area and a few are large;
		// radius is independent of circle count so fewer/larger circles don't shrink the area.
		radius := baseClusterRadius + float32(math.Pow(rand.Float64(), 3))*(maxClusterRadius-baseClusterRadius)
		count := minCirclesPerCluster + rand.Intn(maxCirclesPerCluster-minCirclesPerCluster+1)

		velX, velY := randVelocity(minSpeed, maxSpeed)
		c := cluster{
			centerX: randRange(radius, 1-radius),
			centerY: randRange(radius, 1-radius),
			velX:    velX,
			velY:    velY,
			radius:  radius,
			circles: make([]clusterCircle, count),
		}

		for i := range c.circles {
			localX, localY := randInDisc(radius)
			localVelX, localVelY := randVelocity(minLocalSpeed, maxLocalSpeed)

			c.circles[i] = clusterCircle{
				localX: localX,
				localY: localY,
				velX:   localVelX,
				velY:   localVelY,
				radius: randRange(minRadius, maxRadius),
			}
		}

		mg.clusterOffset[ci] = len(mg.group.Circles)
		for _, cc := range c.circles {
			mg.group.Circles = append(mg.group.Circles, metaballs.Circle{
				X:      c.centerX + cc.localX,
				Y:      c.centerY + cc.localY,
				Radius: cc.radius,
			})
		}
		for i := 0; i < len(c.circles)-1; i++ {
			mg.group.Bridges = append(mg.group.Bridges, metaballs.Bridge{
				A:            mg.clusterOffset[ci] + i,
				B:            mg.clusterOffset[ci] + i + 1,
				MiddleRadius: clusterBridgeRadius,
			})
		}

		mg.clusters[ci] = c
	}

	return mg
}

func NewGame() (*Game, error) {
	groups := []movingGroup{
		newMovingGroup(metaballs.NewColorScale(1, 0.2, 0.2, 1), numClustersPerGroup),
		newMovingGroup(metaballs.NewColorScale(0.2, 0.4, 1, 1), numClustersPerGroup),
		newMovingGroup(metaballs.NewColorScale(0.2, 1, 0.4, 1), numClustersPerGroup),
	}

	// Capacity tiers: shader pool from small (cheap, common case for sparse
	// tiles) up to large (rare, dense tiles after subdivision). Bridge
	// capacities scale with circle capacities since each cluster chain has
	// one bridge fewer than its circle count.
	tiers := []metaballs.ShaderCapacity{
		{MainCircles: 16, MainBridges: 8, OtherCircles: 16, OtherBridges: 8},
		{MainCircles: 32, MainBridges: 16, OtherCircles: 32, OtherBridges: 16},
	}

	renderer, err := metaballs.NewRenderer(metaballs.RendererConfig{
		Common:      metaballs.ShaderCommonConfig{SmoothK: smoothK, LightDirX: lightDirX, LightDirY: lightDirY, EdgeThickness: edgeThickness},
		Tiers:       tiers,
		RootCols:    1,
		RootRows:    1,
		MaxDepth:    3,
		MinTileSize: 0.01,
		Debug:       true,
		Workers:     4,
	})
	if err != nil {
		return nil, err
	}

	return &Game{
		renderer:  renderer,
		groups:    groups,
		lastStats: new(metaballs.Stats),
	}, nil
}

func (g *Game) Update() error {
	const dt = 1.0 / 60.0

	for gi := range g.groups {
		mg := &g.groups[gi]

		for ci := range mg.clusters {
			c := &mg.clusters[ci]

			c.centerX += c.velX * dt
			c.centerY += c.velY * dt

			effRadius := c.radius + maxRadius
			if c.centerX-effRadius < 0 {
				c.centerX = effRadius
				c.velX = -c.velX
			} else if c.centerX+effRadius > 1 {
				c.centerX = 1 - effRadius
				c.velX = -c.velX
			}

			if c.centerY-effRadius < 0 {
				c.centerY = effRadius
				c.velY = -c.velY
			} else if c.centerY+effRadius > 1 {
				c.centerY = 1 - effRadius
				c.velY = -c.velY
			}

			for i := range c.circles {
				cc := &c.circles[i]

				cc.localX += cc.velX * dt
				cc.localY += cc.velY * dt

				// Circular container bounce: clamp to the disc edge and
				// reflect the radial velocity component so circles never
				// drift outside their cluster.
				if distSq := cc.localX*cc.localX + cc.localY*cc.localY; distSq > c.radius*c.radius {
					dist := float32(math.Sqrt(float64(distSq)))
					nx, ny := cc.localX/dist, cc.localY/dist

					cc.localX, cc.localY = nx*c.radius, ny*c.radius

					vDotN := cc.velX*nx + cc.velY*ny
					cc.velX -= 2 * vDotN * nx
					cc.velY -= 2 * vDotN * ny
				}

				offset := mg.clusterOffset[ci] + i
				mg.group.Circles[offset].X = c.centerX + cc.localX
				mg.group.Circles[offset].Y = c.centerY + cc.localY
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
		g.lastStats.TilesDrawn.Load(), g.lastStats.TilesSkipped.Load(),
		g.lastStats.CirclesClipped.Load(),
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
	ebiten.SetWindowTitle("Metaballs - Bridges")

	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}
