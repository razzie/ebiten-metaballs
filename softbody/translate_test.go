package softbody

import (
	"fmt"
	"math"
	"reflect"
	"slices"
	"sync"
	"testing"
)

func TestTranslateCirclesPreservesMotionAndDrag(t *testing.T) {
	s := dragWorld()
	a := addDragCircle(t, s, .25, .5, 2, 9)
	b := addDragCircle(t, s, .5, .5, 4, 9)
	addTestBridge(t, s, BridgeSpec{A: a, B: b, MaxDistance: .5, Damping: 1})
	s.p.vx[s.bridgeIndex[b]] = .125
	s.Drag(DragSpec{X: .234375, Y: .5, MaxCircles: 1})
	s.Carry(.359375, .5)
	s.QueueRadialImpulse(RadialImpulse{X: .75, Y: .25, Radius: .1, Strength: .1})
	before, bridges, bounds := bridgeCircles(s), s.BridgeSnapshot(nil), s.Bounds()
	if err := s.TranslateCircles(.125, -.125); err != nil {
		t.Fatal(err)
	}
	after := bridgeCircles(s)
	for i, c := range before {
		c.X += .125
		c.Y -= .125
		if c != after[i] {
			t.Fatalf("translation changed circle properties: %+v, want %+v", after[i], c)
		}
	}
	if s.Bounds() != bounds || !slices.Equal(bridges, s.BridgeSnapshot(nil)) || s.impulses[0].X != .75 || s.impulses[0].Y != .25 {
		t.Fatal("circle translation moved bounds, bridges, or impulse source")
	}
	s.Step(.1)
	nearBridge(t, "translated pending drag X", float64(bridgeCircles(s)[0].X), .5)
	nearBridge(t, "translated pending drag Y", float64(bridgeCircles(s)[0].Y), .375)
	s.Drop()
	nearBridge(t, "original mass", float64(s.p.invMass[s.bridgeIndex[a]]), .5)
}

func TestTranslateCirclesRemoveMode(t *testing.T) {
	s := removalWorld()
	a := addBoundaryCircle(t, s, CircleSpec{X: .25, Y: .5})
	b := addBoundaryCircle(t, s, CircleSpec{X: .75, Y: .5})
	addTestBridge(t, s, BridgeSpec{A: a, B: b, MaxDistance: 1})
	s.Drag(DragSpec{X: .75, Y: .5})
	if err := s.TranslateCircles(.5, 0); err != nil {
		t.Fatal(err)
	}
	if c := s.Snapshot(nil); len(c) != 1 || c[0].ID != a || c[0].X != .75 || len(s.bridges) != 0 || len(s.dragged) != 0 {
		t.Fatalf("translation did not apply removal: %+v", c)
	}
	s.Step(.01)
}

func TestTranslatePolygonsUpdatesCollisionGeometry(t *testing.T) {
	for _, mode := range []BoundaryMode{BoundaryWalls, BoundaryRemove} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			s := polygonWorld()
			if err := s.SetBoundaryMode(mode); err != nil {
				t.Fatal(err)
			}
			poly := addTestPolygon(t, s, rectangle(.375, .25, .5, .75))
			circle := addBoundaryCircle(t, s, CircleSpec{X: .75, Y: .5, VX: -1})
			s.rebuildGrid() // Materialize old candidates before moving terrain.
			old := s.PolygonSnapshot(nil)
			if err := s.TranslatePolygons(.3125, 0); err != nil {
				t.Fatal(err)
			}
			got := s.PolygonSnapshot(nil)
			if got[0].ID != poly || got[0].Points[0].X != old[0].Points[0].X+.3125 || !s.geometryDirty {
				t.Fatal("polygon translation lost ID or cached old geometry")
			}
			assertPolygonClear(t, s)
			s.Step(.001)
			assertPolygonClear(t, s)
			if s.p.id[0] != circle {
				t.Fatal("polygon recovery changed circle identity")
			}
			if err := s.TranslatePolygons(1, 0); err != nil {
				t.Fatal(err)
			}
			if mode == BoundaryRemove && len(s.polygons) != 0 || mode == BoundaryWalls && len(s.polygons) != 1 {
				t.Fatal("polygon translation ignored boundary mode")
			}
			s.Step(.001)
		})
	}
}

func TestShiftOriginEquivalentSimulation(t *testing.T) {
	for _, mode := range []BoundaryMode{BoundaryWalls, BoundaryRemove} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			makeWorld := func() *State {
				cfg := DefaultConfig()
				cfg.Workers, cfg.Substeps, cfg.LinearDamping = 4, 2, -1
				s := New(cfg)
				if err := s.SetBoundaryMode(mode); err != nil {
					t.Fatal(err)
				}
				addTestPolygon(t, s, rectangle(.125, .75, .875, .875))
				a := addDragCircle(t, s, .25, .5, 2, 7)
				b := addDragCircle(t, s, .375, .5, 4, 7)
				held := addDragCircle(t, s, .75, .5, 3, 7)
				addTestBridge(t, s, BridgeSpec{A: a, B: b, MinDistance: .125, MaxDistance: .125, ConstrainDistance: true, Damping: 2})
				addTestBridge(t, s, BridgeSpec{A: b, B: held, MaxDistance: .5, AttractForce: 1, Damping: 1})
				s.Drag(DragSpec{X: .734375, Y: .5, MaxCircles: 1})
				s.Carry(.796875, .5625)
				s.QueueRadialImpulse(RadialImpulse{X: .5, Y: .25, Radius: .5, Strength: .03, Groups: []Group{7}})
				return s
			}
			plain, shifted := makeWorld(), makeWorld()
			const dx, dy = float32(4.25), float32(-2.125)
			before, polys, bridges := bridgeCircles(shifted), shifted.PolygonSnapshot(nil), shifted.BridgeSnapshot(nil)
			if err := shifted.ShiftOrigin(dx, dy); err != nil {
				t.Fatal(err)
			}
			b := plain.Bounds()
			b.MinX -= dx
			b.MaxX -= dx
			b.MinY -= dy
			b.MaxY -= dy
			if shifted.Bounds() != b || !slices.Equal(bridges, shifted.BridgeSnapshot(nil)) {
				t.Fatal("origin shift changed bridge data or failed to shift bounds")
			}
			for i, c := range bridgeCircles(shifted) {
				c.X += dx
				c.Y += dy
				if c != before[i] {
					t.Fatal("origin shift changed initial circle data")
				}
			}
			for k, p := range shifted.PolygonSnapshot(nil) {
				if p.ID != polys[k].ID {
					t.Fatal("polygon ID changed")
				}
				for i, a := range p.Points {
					if (Point{a.X + dx, a.Y + dy}) != polys[k].Points[i] {
						t.Fatal("polygon not shifted with world")
					}
				}
			}
			if shifted.impulses[0].X != plain.impulses[0].X-dx || shifted.impulses[0].Y != plain.impulses[0].Y-dy {
				t.Fatal("pending impulse not shifted")
			}
			for range 12 {
				plain.Step(.125)
				shifted.Step(.125)
				got, want := bridgeCircles(shifted), bridgeCircles(plain)
				if len(got) != len(want) {
					t.Fatal("origin shift caused removal")
				}
				for i, c := range got {
					for j, value := range []float32{c.X + dx, c.Y + dy, c.VX, c.VY} {
						expected := []float32{want[i].X, want[i].Y, want[i].VX, want[i].VY}[j]
						// Constraint displacement is fed back into velocity each
						// substep, amplifying float32 rounding at the offset origin.
						tolerance := float64(1e-5)
						if j >= 2 {
							tolerance = 1e-3
						}
						if math.Abs(float64(value-expected)) > tolerance {
							t.Fatalf("rebased simulation differs for ID %d component %d: %g vs %g", c.ID, j, value, expected)
						}
					}
				}
			}
			plain.Drop()
			shifted.Drop()
			if shifted.p.invMass[shifted.bridgeIndex[3]] != plain.p.invMass[plain.bridgeIndex[3]] {
				t.Fatal("origin shift lost original held mass")
			}
		})
	}
}

func TestTranslationErrorsAreAtomic(t *testing.T) {
	for _, name := range []string{"circles", "polygons", "origin"} {
		t.Run(name, func(t *testing.T) {
			s := dragWorld()
			addDragCircle(t, s, .5, .5, 2, 0)
			addTestPolygon(t, s, rectangle(.75, .25, .875, .75))
			s.Drag(DragSpec{X: .5, Y: .5})
			s.Carry(.6, .5)
			s.QueueRadialImpulse(RadialImpulse{X: .25, Y: .5, Radius: 1, Strength: 1})
			s.rebuildGrid()
			operation := s.TranslateCircles
			if name == "polygons" {
				operation = s.TranslatePolygons
			} else if name == "origin" {
				operation = s.ShiftOrigin
			}
			before, polys, bounds := s.Snapshot(nil), s.PolygonSnapshot(nil), s.Bounds()
			dragX, dragY := s.dragX, s.dragY
			impulses := slices.Clone(s.impulses)
			for _, dx := range []float32{float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1)), math.MaxFloat32} {
				if err := operation(dx, 0); err == nil {
					t.Fatal("invalid translation accepted")
				}
				if !slices.Equal(before, s.Snapshot(nil)) || !reflect.DeepEqual(polys, s.PolygonSnapshot(nil)) || s.Bounds() != bounds || s.dragX != dragX || s.dragY != dragY || !reflect.DeepEqual(impulses, s.impulses) || s.geometryDirty {
					t.Fatal("failed translation mutated state")
				}
			}
			if err := operation(0, 0); err != nil || s.geometryDirty {
				t.Fatal("zero translation was not a no-op")
			}
		})
	}
	// Reject circle movement into fixed terrain or through a closed wall.
	s := polygonWorld()
	addTestPolygon(t, s, rectangle(.75, .25, .875, .75))
	addBoundaryCircle(t, s, CircleSpec{X: .5, Y: .5})
	before := s.Snapshot(nil)
	for _, dx := range []float32{.3, .5} {
		if err := s.TranslateCircles(dx, 0); err == nil || !slices.Equal(before, s.Snapshot(nil)) {
			t.Fatal("invalid circle destination accepted or mutated state")
		}
	}
}

func TestShiftOriginImpulseOverflowIsAtomic(t *testing.T) {
	s := removalWorld()
	if err := s.SetBounds(Bounds{MinX: -1e38, MinY: -1e38, MaxX: 1e38, MaxY: 1e38}); err != nil {
		t.Fatal(err)
	}
	addBoundaryCircle(t, s, CircleSpec{})
	s.QueueRadialImpulse(RadialImpulse{X: 2.8e38, Y: 0, Radius: 1, Strength: 1})
	before, bounds := s.Snapshot(nil), s.Bounds()
	if err := s.ShiftOrigin(-1e38, 0); err == nil {
		t.Fatal("overflowing impulse shift accepted")
	}
	if !slices.Equal(before, s.Snapshot(nil)) || s.Bounds() != bounds || s.impulses[0].X != float32(2.8e38) {
		t.Fatal("impulse overflow partially shifted world")
	}
}

func TestShiftOriginConcurrentImpulseQueue(t *testing.T) {
	s := polygonWorld()
	addBoundaryCircle(t, s, CircleSpec{X: .5, Y: .5})
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 100 {
				s.QueueRadialImpulse(RadialImpulse{X: .25, Y: .5, Radius: 1, Strength: .001})
			}
		})
	}
	for range 100 {
		if err := s.ShiftOrigin(.125, -.125); err != nil {
			t.Fatal(err)
		}
		if err := s.ShiftOrigin(-.125, .125); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	// Producers must coordinate frames for physical meaning; this checks queue
	// synchronization and losslessness, not the source frame of concurrent input.
	if got := len(s.consumeImpulses()); got != 400 {
		t.Fatalf("origin changes lost queued impulses: %d", got)
	}
}
