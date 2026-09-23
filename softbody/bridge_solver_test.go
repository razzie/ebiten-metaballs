package softbody

import (
	"fmt"
	"math"
	"slices"
	"testing"
)

func bridgeCircles(s *State) []CircleSnapshot {
	circles := s.Snapshot(nil)
	slices.SortFunc(circles, func(a, b CircleSnapshot) int { return int(a.ID) - int(b.ID) })
	return circles
}

func nearBridge(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.IsNaN(got) || math.Abs(got-want) > 2e-6 {
		t.Fatalf("%s = %g, want %g", name, got, want)
	}
}

func TestBridgeConstraintMassWeightedLimits(t *testing.T) {
	for _, distance := range []float32{.125, .25, .375, .5, .625} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("distance=%g/reverse=%t", distance, reverse), func(t *testing.T) {
				s, left, right := bridgeWorld(t, distance, 1)
				a, b := left, right
				if reverse {
					a, b = b, a
				}
				addTestBridge(t, s, BridgeSpec{A: a, B: b, MinDistance: .25, MaxDistance: .5, ConstrainDistance: true,
					AttractForce: 1e6, RepelForce: 1e6}) // Spring fields are ignored.
				const dt = .1
				s.Step(dt)
				c := bridgeCircles(s) // Right (mass 2) has the lower ID.
				correction := float64(distance-min(max(distance, .25), .5)) / 3
				nearBridge(t, "right position", float64(c[0].X), .125+float64(distance)-correction)
				nearBridge(t, "left position", float64(c[1].X), .125+2*correction)
				nearBridge(t, "right velocity", float64(c[0].VX), -correction/dt)
				nearBridge(t, "left velocity", float64(c[1].VX), 2*correction/dt)
				nearBridge(t, "momentum", float64(2*c[0].VX+c[1].VX), 0)
			})
		}
	}
}

func TestBridgeConstraintFixedLengthAndCoincidentEndpoints(t *testing.T) {
	for _, coincident := range []bool{false, true} {
		for _, reverse := range []bool{false, true} {
			s, left, right := bridgeWorld(t, .5, 1)
			// A vertical pair also exercises corrections on Y. Coincident cores
			// exercise the ordinary collision solver's deterministic normal.
			s.p.x[0], s.p.x[1], s.p.y[0], s.p.y[1] = .5, .5, .75, .25
			if coincident {
				s.p.y[0] = .25
			}
			if reverse {
				left, right = right, left
			}
			addTestBridge(t, s, BridgeSpec{A: left, B: right, MinDistance: .25, MaxDistance: .25, ConstrainDistance: true})
			s.Step(.1)
			c := bridgeCircles(s)
			nearBridge(t, "length", math.Hypot(float64(c[1].X-c[0].X), float64(c[1].Y-c[0].Y)), .25)
			if coincident && c[0].X >= c[1].X {
				t.Fatal("coincident endpoints did not separate in stable ID order")
			}
		}
	}
}

func TestBridgeDampingMassMomentumAndIterationIndependence(t *testing.T) {
	for _, iterations := range []int{1, 8, 32} {
		for _, damping := range []float32{0, 2, 1e8} {
			for _, speed := range []float32{-1, 0, 1} {
				t.Run(fmt.Sprintf("iterations=%d/damping=%g/speed=%g", iterations, damping, speed), func(t *testing.T) {
					s, left, right := bridgeWorld(t, .5, 1)
					s.cfg.BridgeIterations = iterations
					s.p.vx[0], s.p.vx[1] = speed, -speed
					s.p.vy[0], s.p.vy[1] = .2, .2
					addTestBridge(t, s, BridgeSpec{A: left, B: right, MaxDistance: 1, Damping: damping})
					s.Step(.05)
					c := bridgeCircles(s)
					// The isolated implicit damper has an analytic solution.
					impulse := .05 * float64(damping) * 2 * float64(speed) / (1 + .05*float64(damping)*1.5)
					nearBridge(t, "right velocity", float64(c[0].VX), float64(speed)-impulse/2)
					nearBridge(t, "left velocity", float64(c[1].VX), -float64(speed)+impulse)
					nearBridge(t, "momentum", float64(2*c[0].VX+c[1].VX), float64(speed))
					for _, circle := range c {
						nearBridge(t, "shared transverse velocity", float64(circle.VY), .2)
					}
				})
			}
		}
	}
}

func TestBridgeDampingPreservesTangentialMotion(t *testing.T) {
	s, left, right := bridgeWorld(t, .5, 1)
	addTestBridge(t, s, BridgeSpec{A: left, B: right, MaxDistance: 1, Damping: 100})
	s.p.vx[0], s.p.vx[1] = .3, .3
	s.p.vy[0], s.p.vy[1] = .5, -1
	before := bridgeCircles(s)
	s.indexBridges()
	s.dampBridges(.1) // Isolate damping at the current, horizontal geometry.
	if !slices.Equal(before, bridgeCircles(s)) {
		t.Fatal("axial damping changed shared translation or tangential motion")
	}
}

func TestBridgeConstraintBreaksBeforeCorrection(t *testing.T) {
	s, left, right := bridgeWorld(t, .5, 1)
	id := addTestBridge(t, s, BridgeSpec{A: left, B: right, MaxDistance: .5, BreakDistance: .55, ConstrainDistance: true, Damping: 1e6})
	s.p.vx[0], s.p.vx[1] = 1, -1
	s.Step(.05)
	if len(s.BridgeSnapshot(nil)) != 0 || s.RemoveBridge(id) {
		t.Fatal("constraint hid a break caused by predicted movement")
	}
	c := bridgeCircles(s)
	nearBridge(t, "right velocity", float64(c[0].VX), 1)
	nearBridge(t, "left velocity", float64(c[1].VX), -1)
}

func constraintChain(t *testing.T, iterations, workers int, damping float32) *State {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Substeps, cfg.BridgeIterations, cfg.Workers, cfg.LinearDamping = 1, iterations, workers, -1
	s := New(cfg)
	var previous uint64
	for i := range 8 {
		c := CircleSpec{X: .15 + float32(i)*.08, Y: .5, InnerRadius: .01, OuterRadius: .02, Mass: float32(1 + i%3)}
		if i == 7 {
			c.VX = .4
		}
		id, err := s.AddCircle(c)
		if err != nil {
			t.Fatal(err)
		}
		if previous != 0 {
			addTestBridge(t, s, BridgeSpec{A: previous, B: id, MinDistance: .08, MaxDistance: .08, ConstrainDistance: true, Damping: damping})
		}
		previous = id
	}
	return s
}

func chainLengthError(s *State) float64 {
	c := bridgeCircles(s)
	var result float64
	for i := 1; i < len(c); i++ {
		result += math.Abs(math.Hypot(float64(c[i].X-c[i-1].X), float64(c[i].Y-c[i-1].Y)) - .08)
	}
	return result
}

func TestBridgeConstraintIterationsPropagateChainLoad(t *testing.T) {
	one := constraintChain(t, 1, 1, 0)
	many := constraintChain(t, 32, 1, 0)
	one.Step(1.0 / 60)
	many.Step(1.0 / 60)
	if low, high := chainLengthError(one), chainLengthError(many); high >= low/4 {
		t.Fatalf("iterations did not improve chain lengths: one=%g many=%g", low, high)
	}
	if c := bridgeCircles(many); c[0].VX <= .001 {
		t.Fatal("load did not reach the far end of the chain within a step")
	}
	var momentum float64
	for i, c := range bridgeCircles(many) {
		momentum += float64(c.VX) * float64(1+i%3)
	}
	nearBridge(t, "chain momentum", momentum, .8)
}

func TestBridgeConstraintChainStaysStableAndDeterministic(t *testing.T) {
	a := constraintChain(t, 16, 1, 3)
	b := constraintChain(t, 16, 4, 3)
	for range 120 {
		a.Step(1.0 / 60)
		b.Step(1.0 / 60)
		if !slices.Equal(bridgeCircles(a), bridgeCircles(b)) {
			t.Fatal("worker count changed chain behavior")
		}
		if err := chainLengthError(a); math.IsNaN(err) || err > .003 {
			t.Fatalf("unstable chain: total length error %g", err)
		}
	}
}

func TestBridgeConstraintRespectsWallsAndOtherCores(t *testing.T) {
	for _, wall := range []bool{false, true} {
		s, left, right := bridgeWorld(t, .5, 1)
		s.cfg.BridgeIterations = 32
		minDistance, maxDistance := float32(.2), float32(.2)
		if wall {
			minDistance, maxDistance = .8, .8
		} else {
			// The right endpoint would be projected into this initially
			// non-overlapping third circle without interleaved contacts.
			if _, err := s.AddCircle(CircleSpec{X: .53, Y: .5, InnerRadius: .025, OuterRadius: .025}); err != nil {
				t.Fatal(err)
			}
		}
		addTestBridge(t, s, BridgeSpec{A: left, B: right, MinDistance: minDistance, MaxDistance: maxDistance, ConstrainDistance: true, Damping: 2})
		s.Step(.1)
		c := bridgeCircles(s)
		nearBridge(t, "bridge length with contacts", math.Hypot(float64(c[0].X-c[1].X), float64(c[0].Y-c[1].Y)), float64(maxDistance))
		for i, a := range c {
			if a.X < a.InnerRadius || a.X > 1-a.InnerRadius || a.Y < a.InnerRadius || a.Y > 1-a.InnerRadius {
				t.Fatalf("circle escaped walls: %+v", a)
			}
			for _, b := range c[i+1:] {
				d := math.Hypot(float64(a.X-b.X), float64(a.Y-b.Y))
				if d < float64(a.InnerRadius+b.InnerRadius)-2e-6 {
					t.Fatalf("constraint left cores overlapping: distance=%g", d)
				}
			}
		}
	}
}
