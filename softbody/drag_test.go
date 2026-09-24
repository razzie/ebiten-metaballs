package softbody

import (
	"fmt"
	"math"
	"slices"
	"testing"
)

func dragWorld() *State {
	cfg := DefaultConfig()
	cfg.Workers, cfg.Substeps, cfg.BridgeIterations = 1, 4, 32
	cfg.LinearDamping = -1
	return New(cfg)
}

func addDragCircle(t *testing.T, s *State, x, y, mass float32, group Group) uint64 {
	t.Helper()
	id, err := s.AddCircle(CircleSpec{X: x, Y: y, InnerRadius: .02, OuterRadius: .04, Mass: mass, Group: group})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestDragSelection(t *testing.T) {
	s := dragWorld()
	a := addDragCircle(t, s, .515625, .5, 1, 1)
	b := addDragCircle(t, s, .484375, .5, 2, 2)
	c := addDragCircle(t, s, .5, .5, 3, 1)
	addDragCircle(t, s, .8, .5, 1, 1)
	s.rebuildGrid() // Spatial order differs from stable-ID order.
	for _, tc := range []struct {
		spec DragSpec
		want []uint64
	}{
		{DragSpec{X: .5, Y: .5, MaxCircles: 1}, []uint64{c}},
		{DragSpec{X: .5, Y: .5, MaxCircles: 2}, []uint64{c, a}},
		{DragSpec{X: .5, Y: .5}, []uint64{c, a, b}},
		{DragSpec{X: .5, Y: .5, MaxCircles: -1, Groups: []Group{2}}, []uint64{b}},
		{DragSpec{X: .5, Y: .5, Groups: []Group{1}}, []uint64{c, a}},
		{DragSpec{X: .1, Y: .1}, nil},
	} {
		if got := s.Drag(tc.spec); !slices.Equal(got, tc.want) {
			t.Fatalf("Drag(%+v) = %v, want %v", tc.spec, got, tc.want)
		}
	}
	s.indexBridges()
	for i, id := range []uint64{a, b, c} {
		nearBridge(t, "restored inverse mass", float64(s.p.invMass[s.bridgeIndex[id]]), 1/float64(i+1))
	}
}

func TestCarryPreservesSelectionAndOffset(t *testing.T) {
	s := dragWorld()
	held := addDragCircle(t, s, .8, .4, 3, 1)
	addDragCircle(t, s, .2, .7, 1, 1)
	if ids := s.Drag(DragSpec{X: .78, Y: .39, MaxCircles: 1}); !slices.Equal(ids, []uint64{held}) {
		t.Fatalf("selection = %v", ids)
	}
	s.Carry(.08, .19)
	s.Step(0)
	nearBridge(t, "deferred position", float64(bridgeCircles(s)[0].X), .8)
	s.Step(.1)
	c := bridgeCircles(s)
	nearBridge(t, "grab offset X", float64(c[0].X), .1)
	nearBridge(t, "grab offset Y", float64(c[0].Y), .2)
	nearBridge(t, "free circle", float64(c[1].X), .2)
	nearBridge(t, "carry velocity", float64(c[0].VX), -7)
	// Cross the other circle and several grid cells without reselecting.
	s.Carry(.18, .69)
	s.Step(.1)
	s.Step(.1) // A stationary pointer clears the previous movement velocity.
	c = bridgeCircles(s)
	nearBridge(t, "same held ID", float64(c[0].X), .2)
	nearBridge(t, "stationary velocity", float64(c[0].VX), 0)
	if c[1].X == .2 && c[1].Y == .7 {
		t.Fatal("free circle did not respond to held core")
	}
}

func TestDropRestoresMassAndAppliesPendingCarry(t *testing.T) {
	s := dragWorld()
	id := addDragCircle(t, s, .5, .5, 4, 0)
	s.Drag(DragSpec{X: .5, Y: .5})
	s.Carry(.7, .6)
	s.Drop() // No intervening Step.
	s.Drop()
	s.Carry(.1, .1)
	c := bridgeCircles(s)[0]
	nearBridge(t, "release X", float64(c.X), .7)
	nearBridge(t, "release Y", float64(c.Y), .6)
	nearBridge(t, "release velocity", float64(c.VX), 0)
	s.QueueRadialImpulse(RadialImpulse{X: .2, Y: .6, Radius: float32(math.Inf(1)), Strength: .4})
	s.Step(.1)
	c = bridgeCircles(s)[0]
	if c.ID != id {
		t.Fatal("release changed circle ID")
	}
	nearBridge(t, "mass after release", float64(c.VX), .1)
	nearBridge(t, "free movement after release", float64(c.X), .71)
}

func TestCarryBoundsResizeAndInvalidInput(t *testing.T) {
	s := dragWorld()
	id := addDragCircle(t, s, .5, .5, 1, 0)
	s.Drag(DragSpec{X: .5, Y: .5})
	for _, v := range []float32{float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))} {
		s.Carry(v, .9)
		s.Carry(.9, v)
		s.Drag(DragSpec{X: v, Y: .5})
		s.Drag(DragSpec{X: .5, Y: v})
	}
	s.Step(.1)
	nearBridge(t, "invalid pointer ignored", float64(bridgeCircles(s)[0].X), .5)
	s.Carry(-10, 10)
	s.Step(.1)
	c := bridgeCircles(s)[0]
	nearBridge(t, "left bound", float64(c.X), .02)
	nearBridge(t, "upper bound", float64(c.Y), .98)
	if err := s.SetBounds(Bounds{MinX: -.5, MinY: -.5, MaxX: .5, MaxY: .5}); err != nil {
		t.Fatal(err)
	}
	s.Step(.1)
	c = bridgeCircles(s)[0]
	nearBridge(t, "resized left bound", float64(c.X), -.48)
	nearBridge(t, "resized upper bound", float64(c.Y), .48)
	if c.ID != id {
		t.Fatal("invalid drag changed selection")
	}
}

func TestDragBridgeAnchor(t *testing.T) {
	for _, constrain := range []bool{false, true} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("constraint=%t/reverse=%t", constrain, reverse), func(t *testing.T) {
				s := dragWorld()
				a := addDragCircle(t, s, .7, .5, 4, 0)
				b := addDragCircle(t, s, .5, .5, 2, 0)
				left, right := a, b
				if reverse {
					left, right = right, left
				}
				addTestBridge(t, s, BridgeSpec{A: left, B: right, MinDistance: .2, MaxDistance: .2,
					ConstrainDistance: constrain, AttractForce: 50, RepelForce: 50, Damping: 3})
				s.Drag(DragSpec{X: .7, Y: .5, MaxCircles: 1})
				s.Carry(.85, .5)
				s.Step(.1)
				c := bridgeCircles(s)
				nearBridge(t, "anchor position", float64(c[0].X), .85)
				if c[1].X <= .5 || c[1].VX <= 0 {
					t.Fatalf("free endpoint did not follow: %+v", c[1])
				}
				if constrain {
					nearBridge(t, "constrained length", float64(c[0].X-c[1].X), .2)
				}
				s.Step(.1)
				nearBridge(t, "stationary anchor", float64(bridgeCircles(s)[0].X), .85)
				s.Drop()
				s.indexBridges()
				nearBridge(t, "restored bridged mass", float64(s.p.invMass[s.bridgeIndex[a]]), .25)
			})
		}
	}
}

func TestDragBreaksBridgeBeforeCorrection(t *testing.T) {
	for _, constrain := range []bool{false, true} {
		s := dragWorld()
		s.cfg.Substeps = 1
		a := addDragCircle(t, s, .7, .5, 1, 0)
		b := addDragCircle(t, s, .5, .5, 1, 0)
		addTestBridge(t, s, BridgeSpec{A: a, B: b, MaxDistance: .2, BreakDistance: .25,
			ConstrainDistance: constrain, AttractForce: 1e4, Damping: 1e4})
		s.Drag(DragSpec{X: .7, Y: .5})
		s.Carry(.9, .5)
		s.Step(.1)
		if len(s.BridgeSnapshot(nil)) != 0 {
			t.Fatal("dragged bridge did not break")
		}
		nearBridge(t, "broken bridge applied no force", float64(bridgeCircles(s)[1].VX), 0)
	}
}

func TestDragBothBridgeEndpointsAndOverlappingCores(t *testing.T) {
	s := dragWorld()
	a := addDragCircle(t, s, .48, .5, 1, 0)
	b := addDragCircle(t, s, .52, .5, 2, 0)
	// Incompatible length limits and wall clamping must not cause division by
	// zero or let the bridge/collision solvers displace either held endpoint.
	addTestBridge(t, s, BridgeSpec{A: a, B: b, MinDistance: .2, MaxDistance: .2, ConstrainDistance: true, Damping: 100})
	if ids := s.Drag(DragSpec{X: .5, Y: .5}); len(ids) != 2 {
		t.Fatalf("selection = %v", ids)
	}
	s.Carry(2, .5)
	for range 3 {
		s.Step(.1)
		for _, c := range bridgeCircles(s) {
			nearBridge(t, "held endpoint", float64(c.X), .98)
			nearBridge(t, "held Y", float64(c.Y), .5)
		}
	}
	s.Drop()
	s.Step(.01)
	for _, c := range bridgeCircles(s) {
		if math.IsNaN(float64(c.X)) || math.IsNaN(float64(c.VX)) {
			t.Fatal("nonfinite endpoint after release")
		}
	}
}

func TestDragChainSurvivesSortingAndWorkerCounts(t *testing.T) {
	a, b := constraintChain(t, 32, 1, 3), constraintChain(t, 32, 4, 3)
	for _, s := range []*State{a, b} {
		s.cfg.Substeps = 4
		s.Drag(DragSpec{X: .15, Y: .5, MaxCircles: 1})
	}
	for tick := range 60 {
		y := float32(.5 + .1*math.Sin(float64(tick)/10))
		for _, s := range []*State{a, b} {
			s.Carry(.15, y)
			s.Step(1.0 / 60)
			c := bridgeCircles(s)
			nearBridge(t, "chain anchor X", float64(c[0].X), .15)
			nearBridge(t, "chain anchor Y", float64(c[0].Y), float64(y))
			if err := chainLengthError(s); math.IsNaN(err) || err > .005 {
				t.Fatalf("dragging destabilized chain: error %g", err)
			}
		}
		if !slices.Equal(bridgeCircles(a), bridgeCircles(b)) {
			t.Fatal("drag depends on worker count")
		}
	}
}

func TestDragBridgeDampingUsesPointerVelocity(t *testing.T) {
	s := dragWorld()
	s.cfg.Substeps = 1
	a := addDragCircle(t, s, .7, .5, 1, 0)
	b := addDragCircle(t, s, .5, .5, 2, 0)
	addTestBridge(t, s, BridgeSpec{A: a, B: b, MaxDistance: 1, Damping: 10})
	s.Drag(DragSpec{X: .7, Y: .5})
	s.Carry(.8, .5)
	s.Step(.1)
	c := bridgeCircles(s)
	nearBridge(t, "anchor velocity", float64(c[0].VX), 1)
	// vFree = dt * damping * inverseMass * (vAnchor - vFree).
	nearBridge(t, "damped neighbor velocity", float64(c[1].VX), 1.0/3)
}

func TestHeldCircleIgnoresImpulsesAndMassDamping(t *testing.T) {
	s := dragWorld()
	s.cfg.LinearDamping, s.cfg.LinearDampingMassFactor = 2, 5
	addDragCircle(t, s, .5, .5, 4, 0)
	s.Drag(DragSpec{X: .5, Y: .5})
	s.Carry(.6, .7)
	s.QueueRadialImpulse(RadialImpulse{X: .3, Y: .3, Radius: 1, Strength: 100})
	s.Step(.1)
	c := bridgeCircles(s)[0]
	nearBridge(t, "held X", float64(c.X), .6)
	nearBridge(t, "held Y", float64(c.Y), .7)
	nearBridge(t, "prescribed VX", float64(c.VX), 1)
	nearBridge(t, "prescribed VY", float64(c.VY), 2)
	s.Drop()
	s.Step(.1)
	nearBridge(t, "impulse was consumed while held", float64(bridgeCircles(s)[0].VX), 0)
}
