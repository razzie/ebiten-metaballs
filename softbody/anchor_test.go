package softbody

import (
	"fmt"
	"math"
	"slices"
	"testing"
)

func TestAnchorIgnoresPhysicsAndRestoresMass(t *testing.T) {
	for _, workers := range []int{1, 4} {
		t.Run(fmt.Sprint(workers), func(t *testing.T) {
			s := dragWorld()
			s.cfg.Workers = workers
			s.cfg.LinearDamping, s.cfg.LinearDampingMassFactor = 2, 5
			// Enough particles for full SIMD vectors and a scalar tail. Reverse
			// spatial order so the anchors must follow IDs through sorting.
			for i := range 65 {
				x, y := .1+float32(8-i%9)*.08, .1+float32(i/9)*.08
				id := addDragCircle(t, s, x, y, float32(i+1), 7)
				s.p.vx[s.bridgeIndex[id]], s.p.vy[s.bridgeIndex[id]] = 3, -2
				if err := s.AnchorCircle(id, x, y); err != nil {
					t.Fatal(err)
				}
			}
			before := bridgeCircles(s)
			for range 3 {
				s.QueueRadialImpulse(RadialImpulse{X: .5, Y: .5, Radius: 1, Strength: 100})
				s.Step(.1)
				if !slices.Equal(before, bridgeCircles(s)) {
					t.Fatal("physics moved an anchored circle")
				}
			}
			for _, c := range before {
				if !c.Anchored || c.VX != 0 || c.VY != 0 || !s.ReleaseCircle(c.ID) || s.ReleaseCircle(c.ID) {
					t.Fatalf("incorrect anchor lifecycle: %+v", c)
				}
				nearBridge(t, "restored mass", float64(s.p.invMass[s.bridgeIndex[c.ID]]), 1/float64(c.ID))
			}
			s.Step(.01)
			for i, c := range bridgeCircles(s) {
				nearBridge(t, "released X", float64(c.X), float64(before[i].X))
				nearBridge(t, "released Y", float64(c.Y), float64(before[i].Y))
				nearBridge(t, "released VX", float64(c.VX), 0)
				nearBridge(t, "released VY", float64(c.VY), 0)
				if c.Anchored {
					t.Fatal("released circle still marked anchored")
				}
			}
			s.QueueRadialImpulse(RadialImpulse{X: 0, Y: 0, Radius: 2, Strength: 1})
			s.Step(.01)
			if bridgeCircles(s)[0].VX <= 0 {
				t.Fatal("released circle did not resume physics")
			}
		})
	}
}

func TestAnchorContactsAndBridges(t *testing.T) {
	for _, constrain := range []bool{false, true} {
		for _, both := range []bool{false, true} {
			t.Run(fmt.Sprintf("constrain=%t/both=%t", constrain, both), func(t *testing.T) {
				s := dragWorld()
				a := addDragCircle(t, s, .75, .5, 4, 0)
				b := addDragCircle(t, s, .4, .5, 2, 0)
				addTestBridge(t, s, BridgeSpec{A: a, B: b, MinDistance: .2, MaxDistance: .2,
					ConstrainDistance: constrain, AttractForce: 20, RepelForce: 20, Damping: 10})
				if err := s.AnchorCircle(a, .75, .5); err != nil {
					t.Fatal(err)
				}
				if both {
					// Incompatible bridge length and overlapping cores must not
					// move either anchor or produce nonfinite values.
					if err := s.AnchorCircle(b, .75, .5); err != nil {
						t.Fatal(err)
					}
				} else {
					addDragCircle(t, s, .76, .5, 1, 0)
				}
				s.Step(.1)
				c := bridgeCircles(s)
				if c[0].X != .75 || c[0].Y != .5 || c[0].VX != 0 || c[0].VY != 0 {
					t.Fatalf("solver moved anchor: %+v", c[0])
				}
				if both {
					if c[1].X != .75 || c[1].VX != 0 || c[1].VY != 0 {
						t.Fatalf("solver moved second anchor: %+v", c[1])
					}
				} else {
					if c[1].X <= .4 || c[2].X <= .76 {
						t.Fatalf("free circles did not respond to anchor: %+v", c)
					}
					if constrain {
						nearBridge(t, "bridge length", float64(c[0].X-c[1].X), .2)
					}
				}
			})
		}
	}
}

func TestAnchorTakesCircleFromDrag(t *testing.T) {
	s := dragWorld()
	a := addDragCircle(t, s, .48, .5, 4, 0)
	b := addDragCircle(t, s, .52, .5, 2, 0)
	s.Drag(DragSpec{X: .5, Y: .5})
	s.Carry(.8, .5)
	if err := s.AnchorCircle(a, .25, .25); err != nil {
		t.Fatal(err)
	}
	s.Step(.1)
	s.Drop()
	c := bridgeCircles(s)
	if c[0].X != .25 || c[0].Y != .25 || !c[0].Anchored || c[1].X <= .8 {
		t.Fatalf("anchoring did not preserve remaining drag: %+v", c)
	}
	// Put a free circle under the anchored one: anchored hits must not consume
	// MaxCircles or prevent selecting free hits.
	free := addDragCircle(t, s, .25, .25, 1, 0)
	if ids := s.Drag(DragSpec{X: .25, Y: .25, MaxCircles: 1}); !slices.Equal(ids, []uint64{free}) {
		t.Fatalf("drag selected anchored circle: %v", ids)
	}
	s.Drop()
	if !s.ReleaseCircle(a) {
		t.Fatal("failed to release former drag selection")
	}
	nearBridge(t, "original dragged mass", float64(s.p.invMass[s.bridgeIndex[a]]), .25)
	nearBridge(t, "remaining dragged mass", float64(s.p.invMass[s.bridgeIndex[b]]), .5)
}

func TestAnchorValidationIsAtomic(t *testing.T) {
	s := dragWorld()
	a := addDragCircle(t, s, .25, .25, 4, 0)
	addTestPolygon(t, s, rectangle(.4, .4, .6, .6))
	s.Drag(DragSpec{X: .25, Y: .25})
	s.Carry(.3, .3)
	before := s.Snapshot(nil)
	for _, target := range []Point{{float32(math.NaN()), .25}, {.25, float32(math.Inf(1))}, {0, .25}, {2, .25}, {.5, .5}, {.39, .5}} {
		if err := s.AnchorCircle(a, target.X, target.Y); err == nil {
			t.Fatalf("accepted invalid anchor: %+v", target)
		}
		if !slices.Equal(before, s.Snapshot(nil)) || len(s.dragged) != 1 || len(s.anchored) != 0 {
			t.Fatal("invalid anchor mutated state")
		}
	}
	if err := s.AnchorCircle(999, .25, .25); err == nil || s.ReleaseCircle(999) {
		t.Fatal("unknown circle accepted")
	}
	if err := s.AnchorCircle(a, .25, .25); err != nil {
		t.Fatal(err)
	}
	if err := s.AnchorCircle(a, .25, .25); err != nil {
		t.Fatal(err)
	}
	if err := s.AnchorCircle(a, .3, .3); err == nil {
		t.Fatal("moved an existing anchor without releasing it")
	}
}

func TestAnchorWorldChangesAndRemoval(t *testing.T) {
	s := dragWorld()
	a := addDragCircle(t, s, .75, .5, 4, 0)
	b := addDragCircle(t, s, .25, .5, 1, 0)
	if err := s.AnchorCircle(a, .75, .5); err != nil {
		t.Fatal(err)
	}
	if err := s.TranslateCircles(.125, 0); err != nil {
		t.Fatal(err)
	}
	c := bridgeCircles(s)
	if c[0].X != .75 || c[1].X != .375 {
		t.Fatalf("circle translation moved anchor or missed free circle: %+v", c)
	}
	addTestPolygon(t, s, rectangle(.7, .4, .8, .6))
	if err := s.TranslatePolygons(-.03125, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.SetBounds(Bounds{MaxX: .5, MaxY: 1}); err != nil {
		t.Fatal(err)
	}
	s.Step(.01)
	if c := bridgeCircles(s)[0]; c.X != .75 || c.Y != .5 || c.VX != 0 || c.VY != 0 {
		t.Fatalf("geometry changes moved anchor: %+v", c)
	}
	if err := s.ShiftOrigin(.125, .25); err != nil {
		t.Fatal(err)
	}
	s.Step(.01)
	if c := bridgeCircles(s)[0]; c.X != .625 || c.Y != .25 || !c.Anchored {
		t.Fatalf("origin shift lost anchor: %+v", c)
	}
	addTestBridge(t, s, BridgeSpec{A: a, B: b, MaxDistance: 1})
	if err := s.SetBoundaryMode(BoundaryRemove); err != nil {
		t.Fatal(err)
	}
	if s.ReleaseCircle(a) || len(s.anchored) != 0 || s.Len() != 1 || len(s.bridges) != 0 {
		t.Fatal("boundary removal left stale anchor or bridge")
	}
	if err := s.AnchorCircle(b, .625, .25); err == nil {
		t.Fatal("remove mode accepted outside anchor")
	}
}
