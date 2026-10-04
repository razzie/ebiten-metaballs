package softbody

import (
	"fmt"
	"math"
	"reflect"
	"slices"
	"testing"
)

func removalWorld() *State {
	cfg := DefaultConfig()
	cfg.BoundaryMode = BoundaryRemove
	cfg.Workers, cfg.Substeps, cfg.LinearDamping = 4, 1, -1
	return New(cfg)
}

func addBoundaryCircle(t *testing.T, s *State, c CircleSpec) uint64 {
	t.Helper()
	if c.InnerRadius == 0 {
		c.InnerRadius, c.OuterRadius = .01, .04
	}
	id, err := s.AddCircle(c)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestBoundaryRemoveEdges(t *testing.T) {
	for _, polygons := range []bool{false, true} {
		for _, motion := range []CircleSpec{
			{X: .02, Y: .5, VX: -1}, {X: .98, Y: .5, VX: 1},
			{X: .5, Y: .02, VY: -1}, {X: .5, Y: .98, VY: 1},
		} {
			t.Run(fmt.Sprintf("polygons=%t/motion=%v", polygons, motion), func(t *testing.T) {
				s := removalWorld()
				if polygons {
					// A distant obstacle still selects the polygon integration path.
					addTestPolygon(t, s, rectangle(.1, .1, .2, .2))
				}
				id := addBoundaryCircle(t, s, motion)
				s.Step(.01)
				if s.Len() != 1 {
					t.Fatal("removed before center left bounds")
				}
				c := s.Snapshot(nil)[0]
				if c.VX != motion.VX || c.VY != motion.VY {
					t.Fatalf("boundary applied a force or bounce: %+v", c)
				}
				s.Step(.02)
				if s.Len() != 0 || len(s.bridgeIndex) != 0 || len(s.grid.active) != 0 {
					t.Fatal("escaped circle still active")
				}
				if next := addBoundaryCircle(t, s, CircleSpec{X: .5, Y: .5}); next <= id {
					t.Fatal("removed ID reused")
				}
				s.Step(.01)
			})
		}
	}
}

func TestBoundaryRemoveExactEdgesAndInvalidAdds(t *testing.T) {
	s := removalWorld()
	for _, p := range []Point{{0, .5}, {1, .5}, {.5, 0}, {.5, 1}} {
		addBoundaryCircle(t, s, CircleSpec{X: p.X, Y: p.Y})
	}
	before := bridgeCircles(s)
	s.Step(.01)
	if !slices.Equal(before, bridgeCircles(s)) {
		t.Fatal("stationary centers on edges were confined or removed")
	}
	for _, c := range []CircleSpec{
		{X: -.001, Y: .5}, {X: 1.001, Y: .5}, {X: .5, Y: -.001}, {X: .5, Y: 1.001},
		{X: float32(math.NaN()), Y: .5}, {X: .5, Y: float32(math.Inf(1))},
	} {
		c.InnerRadius, c.OuterRadius = .01, .04
		if _, err := s.AddCircle(c); err == nil {
			t.Fatalf("invalid addition accepted: %+v", c)
		}
	}
	if s.nextID != 5 || s.Len() != 4 {
		t.Fatal("rejected additions mutated state")
	}
}

func TestBoundaryRemoveBoundsCleanup(t *testing.T) {
	s := removalWorld()
	large := addBoundaryCircle(t, s, CircleSpec{X: .15, Y: .5, InnerRadius: .02, OuterRadius: .2})
	left := addBoundaryCircle(t, s, CircleSpec{X: .5, Y: .5, Mass: 2})
	right := addBoundaryCircle(t, s, CircleSpec{X: .7, Y: .5, Mass: 4})
	deleted := addTestBridge(t, s, BridgeSpec{A: large, B: left, MaxDistance: 1})
	kept := addTestBridge(t, s, BridgeSpec{A: left, B: right, MaxDistance: 1, Damping: 1})
	s.rebuildGrid()
	b := Bounds{MinX: .4, MinY: .4, MaxX: .8, MaxY: .6}
	if err := s.SetBounds(b); err != nil {
		t.Fatal(err)
	}
	if s.Bounds() != b || s.Len() != 2 || s.RemoveBridge(deleted) {
		t.Fatal("bounds cleanup failed")
	}
	if bridges := s.BridgeSnapshot(nil); len(bridges) != 1 || bridges[0].ID != kept {
		t.Fatalf("unrelated bridge lost: %v", bridges)
	}
	if _, err := s.AddBridge(BridgeSpec{A: large, B: right}); err == nil {
		t.Fatal("deleted endpoint accepted")
	}
	if s.maxOuter != .04 {
		t.Fatalf("removed circle still controls maximum radius: %g", s.maxOuter)
	}
	if err := s.SetBoundaryMode(BoundaryWalls); err != nil {
		t.Fatal("removed large circle still prevents wall confinement:", err)
	}
	s.Step(.01)
	for _, c := range bridgeCircles(s) {
		if c.ID == left && c.X != .5 || c.ID == right && c.X != .7 {
			t.Fatal("resize moved surviving entities")
		}
	}
}

func TestBoundaryModeChanges(t *testing.T) {
	s := removalWorld()
	addBoundaryCircle(t, s, CircleSpec{X: 0, Y: .5, VX: -1})
	if err := s.SetBoundaryMode(BoundaryWalls); err != nil {
		t.Fatal(err)
	}
	if c := s.Snapshot(nil)[0]; c.X != c.InnerRadius || c.VX <= 0 {
		t.Fatalf("switch to walls did not confine and bounce: %+v", c)
	}
	if s.Config().BoundaryMode != BoundaryWalls {
		t.Fatal("Config has stale boundary mode")
	}
	outside := addTestPolygon(t, s, rectangle(2, 2, 3, 3))
	if err := s.SetBoundaryMode(BoundaryRemove); err != nil || s.RemovePolygon(outside) {
		t.Fatal("switch to removal did not clean up outside polygon")
	}
	before, bounds := s.Snapshot(nil), s.Bounds()
	if err := s.SetBoundaryMode(BoundaryMode(255)); err == nil {
		t.Fatal("unknown mode accepted")
	}
	if s.Config().BoundaryMode != BoundaryRemove || s.Bounds() != bounds || !slices.Equal(before, s.Snapshot(nil)) {
		t.Fatal("invalid mode mutated state")
	}
	// Removal mode allows circles larger than the rectangle, but wall mode cannot.
	addBoundaryCircle(t, s, CircleSpec{X: .5, Y: .5, InnerRadius: .6, OuterRadius: .7})
	if err := s.SetBoundaryMode(BoundaryWalls); err == nil || s.Config().BoundaryMode != BoundaryRemove {
		t.Fatal("invalid wall switch accepted")
	}
	if err := s.SetBounds(Bounds{MinX: .49, MinY: .49, MaxX: .51, MaxY: .51}); err != nil {
		t.Fatal("remove-mode bounds unnecessarily require fitting circles:", err)
	}
	cfg := DefaultConfig()
	cfg.BoundaryMode = BoundaryMode(255)
	if New(cfg).Config().BoundaryMode != BoundaryWalls {
		t.Fatal("New did not normalize unknown mode")
	}
}

func TestBoundaryRemovePolygonsAndCandidateIndices(t *testing.T) {
	s := removalWorld()
	out := addTestPolygon(t, s, rectangle(.8, .2, .9, .3))
	inside := addTestPolygon(t, s, rectangle(.4, .3, .5, .7))
	partial := addTestPolygon(t, s, rectangle(.6, .8, 1.1, 1.1))
	touch := addTestPolygon(t, s, rectangle(.7, -.2, .8, 0))
	addBoundaryCircle(t, s, CircleSpec{X: .35, Y: .5, InnerRadius: .02, OuterRadius: .08})
	s.Step(.001) // Materialize candidates before deleting the first polygon.
	before := s.PolygonSnapshot(nil)
	if err := s.SetBounds(Bounds{MaxX: .7, MaxY: 1}); err != nil {
		t.Fatal(err)
	}
	polys := s.PolygonSnapshot(nil)
	if len(polys) != 3 || polys[0].ID != inside || polys[1].ID != partial || polys[2].ID != touch || s.RemovePolygon(out) {
		t.Fatalf("polygon cleanup mismatch: %+v", polys)
	}
	if !reflect.DeepEqual(polys, before[1:]) {
		t.Fatal("partially overlapping polygon was clipped")
	}
	s.Step(.001)
	if c := s.Snapshot(nil)[0]; c.VX >= 0 {
		t.Fatal("retained polygon shell response lost after index compaction")
	}
	if _, err := s.AddPolygon(rectangle(2, 2, 3, 3)); err == nil {
		t.Fatal("outside polygon accepted")
	}
	// A polygon enclosing the rectangle intersects despite all vertices outside.
	enclosing := removalWorld()
	addTestPolygon(t, enclosing, rectangle(-1, -1, 2, 2))
	if err := enclosing.SetBounds(Bounds{MinX: .2, MinY: .2, MaxX: .8, MaxY: .8}); err != nil || len(enclosing.polygons) != 1 {
		t.Fatal("enclosing polygon incorrectly removed")
	}
}

func TestBoundaryRemoveDragAndDrop(t *testing.T) {
	for _, drop := range []bool{false, true} {
		t.Run(fmt.Sprint(drop), func(t *testing.T) {
			s := removalWorld()
			a := addDragCircle(t, s, .48, .5, 2, 0)
			b := addDragCircle(t, s, .52, .5, 4, 0)
			addTestBridge(t, s, BridgeSpec{A: a, B: b, MaxDistance: .2, Damping: 2, ConstrainDistance: true})
			if len(s.Drag(DragSpec{X: .5, Y: .5})) != 2 {
				t.Fatal("fixture did not select both circles")
			}
			s.Carry(.99, .5) // First held center remains, second exits.
			if drop {
				s.Drop()
			} else {
				s.Step(.1)
			}
			c := s.Snapshot(nil)
			if len(c) != 1 || c[0].ID != a || len(s.bridges) != 0 {
				t.Fatalf("dragged exit cleanup failed: %+v", c)
			}
			nearBridge(t, "surviving held center", float64(c[0].X), .97)
			if !drop && (len(s.dragged) != 1 || s.dragged[0].id != a) {
				t.Fatal("surviving selection was dropped")
			}
			s.Drop()
			nearBridge(t, "restored surviving mass", float64(s.p.invMass[s.bridgeIndex[a]]), .5)
			s.Step(.01)
		})
	}
}

func TestBoundaryRemoveBeforeBridgeCorrection(t *testing.T) {
	s := removalWorld()
	a := addBoundaryCircle(t, s, CircleSpec{X: .5, Y: .5})
	b := addBoundaryCircle(t, s, CircleSpec{X: .95, Y: .5, VX: 2})
	addTestBridge(t, s, BridgeSpec{A: a, B: b, MaxDistance: .5, ConstrainDistance: true, Damping: 5})
	s.Step(.1)
	if c := s.Snapshot(nil); len(c) != 1 || c[0].ID != a || c[0].X != .5 || len(s.bridges) != 0 {
		t.Fatalf("constraint pulled escaped endpoint back: %+v", c)
	}
}

func TestBoundaryRemoveConstraintAndContactExits(t *testing.T) {
	t.Run("constraint", func(t *testing.T) {
		s := removalWorld()
		a := addBoundaryCircle(t, s, CircleSpec{X: .1, Y: .5})
		b := addBoundaryCircle(t, s, CircleSpec{X: .3, Y: .5})
		addTestBridge(t, s, BridgeSpec{A: a, B: b, MinDistance: .8, MaxDistance: .8, ConstrainDistance: true})
		s.Step(.001)
		if c := s.Snapshot(nil); len(c) != 1 || c[0].ID != b || len(s.bridges) != 0 {
			t.Fatalf("constraint-created exit not removed: %+v", c)
		}
	})
	t.Run("contact projection", func(t *testing.T) {
		s := removalWorld()
		a := addBoundaryCircle(t, s, CircleSpec{X: .01, Y: .5})
		b := addBoundaryCircle(t, s, CircleSpec{X: .4, Y: .5})
		addTestBridge(t, s, BridgeSpec{A: a, B: b, MaxDistance: 1, ConstrainDistance: true})
		// Isolate the projection-pass checkpoint from the initial force solve.
		s.rebuildGrid()
		s.p.x[s.bridgeIndex[b]] = .015
		s.p.inner[s.bridgeIndex[a]], s.p.inner[s.bridgeIndex[b]] = .03, .03
		s.solveBridgeConstraints(.01)
		if s.Len() != 1 || s.Snapshot(nil)[0].ID != b || len(s.bridges) != 0 {
			t.Fatal("contact-created exit not removed")
		}
	})
}

func TestBoundaryRemoveSparseIDsAndRepopulation(t *testing.T) {
	s := removalWorld()
	survivor := addBoundaryCircle(t, s, CircleSpec{X: .5, Y: .5})
	for range 200 {
		gone := addBoundaryCircle(t, s, CircleSpec{X: .1, Y: .5, VX: -2})
		addTestBridge(t, s, BridgeSpec{A: survivor, B: gone, MaxDistance: 1, Damping: 1})
		s.Step(.1)
		if s.Len() != 1 || len(s.bridgeIndex) != 1 || len(s.bridges) != 0 {
			t.Fatal("sparse ID lifecycle failed")
		}
	}
	if err := s.SetBounds(Bounds{MinX: 2, MinY: 2, MaxX: 3, MaxY: 3}); err != nil {
		t.Fatal(err)
	}
	if s.Len() != 0 || s.maxOuter != 0 {
		t.Fatal("world evacuation failed")
	}
	a := addBoundaryCircle(t, s, CircleSpec{X: 2.4, Y: 2.5})
	b := addBoundaryCircle(t, s, CircleSpec{X: 2.6, Y: 2.5})
	addTestBridge(t, s, BridgeSpec{A: a, B: b, MaxDistance: .2, ConstrainDistance: true, Damping: 2})
	s.Step(.01)
	if s.Len() != 2 || a <= 200 || len(s.bridgeIndex) != 2 {
		t.Fatal("repopulation failed")
	}
}

func TestIntegrateWithoutWalls(t *testing.T) {
	for _, workers := range []int{1, 4} {
		t.Run(fmt.Sprint(workers), func(t *testing.T) {
			// Full SIMD vectors, a scalar tail, and parallel grains; held lanes
			// must retain prescribed motion even when already outside the world.
			const n = 1025
			var p particleData
			p.resize(n)
			for i := range n {
				p.x[i], p.y[i], p.vx[i], p.vy[i], p.inner[i] = .5, .5, 2, -2, .1
				if i%3 != 0 {
					p.invMass[i] = .5
				}
			}
			zero := make([]float32, n)
			integrateKernel(&p, zero, zero, zero, zero, zero, zero, 1, 0, 0, .5, Bounds{MaxX: 1, MaxY: 1}, workers, false)
			for i := range n {
				wantX, wantY := float32(2.5), float32(-1.5)
				if i%3 == 0 {
					wantX, wantY = .5, .5
				}
				if p.x[i] != wantX || p.y[i] != wantY || p.vx[i] != 2 || p.vy[i] != -2 {
					t.Fatalf("particle %d was confined: (%g,%g), (%g,%g)", i, p.x[i], p.y[i], p.vx[i], p.vy[i])
				}
			}
		})
	}
}
