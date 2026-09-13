package softbody

import (
	"runtime"
	"sync"
)

type Group uint8

const (
	Red Group = iota
	Green
	Blue
)

type MouseButton uint8

const (
	MouseLeft MouseButton = iota
	MouseRight
	MouseMiddle
)

type CircleSpec struct {
	X, Y        float32
	VX, VY      float32
	InnerRadius float32
	OuterRadius float32
	Mass        float32
	Group       Group
}

type CircleSnapshot struct {
	ID          uint64
	Group       Group
	X, Y        float32
	VX, VY      float32
	InnerRadius float32
	OuterRadius float32
}

// Bounds defines the rectangular simulation world in circle coordinates.
type Bounds struct {
	MinX, MinY, MaxX, MaxY float32
}

type Config struct {
	// GridSpacing is the distance between neighboring hex-cell centers.
	// Zero chooses 2*max(OuterRadius), which is a good starting point.
	GridSpacing float32

	Workers  int
	Substeps int

	ShellStiffness float32
	ShellDamping   float32
	Restitution    float32
	CoreCorrection float32
	LinearDamping  float32

	ClickRadius  float32
	ClickImpulse float32
}

func DefaultConfig() Config {
	return Config{
		Workers:        runtime.GOMAXPROCS(0),
		Substeps:       2,
		ShellStiffness: 80,
		ShellDamping:   2.5,
		Restitution:    0.08,
		CoreCorrection: 0.8,
		LinearDamping:  0.25,
		ClickRadius:    0.18,
		ClickImpulse:   0.35,
	}
}

type click struct {
	x, y  float32
	group Group
}

type particleData struct {
	id      []uint64
	group   []Group
	x, y    []float32
	vx, vy  []float32
	inner   []float32
	outer   []float32
	invMass []float32
}

func (p *particleData) len() int { return len(p.x) }

func (p *particleData) resize(n int) {
	p.id = resize(p.id, n)
	p.group = resize(p.group, n)
	p.x = resize(p.x, n)
	p.y = resize(p.y, n)
	p.vx = resize(p.vx, n)
	p.vy = resize(p.vy, n)
	p.inner = resize(p.inner, n)
	p.outer = resize(p.outer, n)
	p.invMass = resize(p.invMass, n)
}

func resize[T any](s []T, n int) []T {
	if cap(s) >= n {
		return s[:n]
	}
	return append(s[:cap(s)], make([]T, n-cap(s))...)
}

type State struct {
	cfg    Config
	bounds Bounds

	p       particleData
	scratch particleData
	nextID  uint64

	maxOuter      float32
	geometryDirty bool
	grid          hexGrid

	cellID []int
	qf     []float32
	rf     []float32

	ax, ay       []float32
	dvx, dvy     []float32
	corrX, corrY []float32

	clickMu sync.Mutex
	clicks  []click
}
