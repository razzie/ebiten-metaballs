package softbody

import (
	"fmt"
	"math"
	"slices"
	"testing"
)

func polygonWorld() *State {
	cfg := DefaultConfig()
	cfg.Workers = 1
	cfg.Substeps = 1
	cfg.LinearDamping = -1
	return New(cfg)
}
func rectangle(x0, y0, x1, y1 float32) []Point {
	return []Point{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}}
}
func addTestPolygon(t *testing.T, s *State, points []Point) uint64 {
	t.Helper()
	id, err := s.AddPolygon(points)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func assertPolygonClear(t *testing.T, s *State) {
	t.Helper()
	for _, c := range s.Snapshot(nil) {
		for k := range s.polygons {
			d, _, _ := s.polygons[k].contact(float64(c.X), float64(c.Y))
			if math.IsNaN(d) || d < float64(c.InnerRadius)-1e-6 {
				t.Fatalf("circle penetrates polygon: %+v, distance=%g", c, d)
			}
		}
	}
}

func TestPolygonAPI(t *testing.T) {
	s := polygonWorld()
	points := rectangle(.4, .2, .6, .8)
	id := addTestPolygon(t, s, append(slices.Clone(points), points[0]))
	points[0] = Point{0, 0}
	snap := s.PolygonSnapshot(nil)
	if len(snap) != 1 || snap[0].ID != id || len(snap[0].Points) != 4 || snap[0].Points[0] != (Point{.4, .2}) {
		t.Fatalf("snapshot: %+v", snap)
	}
	snap[0].Points[0] = Point{1, 1}
	if s.PolygonSnapshot(snap)[0].Points[0] != (Point{.4, .2}) {
		t.Fatal("snapshot aliases polygon")
	}
	if !s.RemovePolygon(id) || s.RemovePolygon(id) || len(s.PolygonSnapshot(nil)) != 0 {
		t.Fatal("remove failed")
	}
	if next := addTestPolygon(t, s, rectangle(.4, .2, .6, .8)); next <= id {
		t.Fatal("ID reused")
	}
}

func TestPolygonValidation(t *testing.T) {
	for name, points := range map[string][]Point{
		"empty": nil, "line": {{0, 0}, {1, 1}}, "collinear": {{0, 0}, {1, 1}, {2, 2}},
		"duplicate": {{0, 0}, {1, 0}, {1, 0}, {0, 1}},
		"crossing":  {{0, 0}, {2, 2}, {0, 2}, {1, 0}},
		"touching":  {{0, 0}, {2, 0}, {2, 2}, {1, 0}, {0, 2}},
		"backtrack": {{0, 0}, {2, 0}, {1, 0}, {1, 1}, {0, 1}},
		"nan":       {{0, 0}, {1, 0}, {0, float32(math.NaN())}},
		"infinite":  {{0, 0}, {1, 0}, {0, float32(math.Inf(1))}},
	} {
		t.Run(name, func(t *testing.T) {
			s := polygonWorld()
			if _, err := s.AddPolygon(points); err == nil {
				t.Fatal("invalid polygon accepted")
			}
			if len(s.polygons) != 0 || s.nextPolygonID != 0 {
				t.Fatal("failed add mutated state")
			}
		})
	}
	// Straight subdivisions of an edge are valid.
	addTestPolygon(t, polygonWorld(), []Point{{0, 0}, {.5, 0}, {1, 0}, {1, 1}, {0, 1}})
}

func TestPolygonDragAndDropCannotTunnel(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		for _, drop := range []bool{false, true} {
			for _, target := range []float32{.5, .9} {
				t.Run(fmt.Sprintf("reverse=%t/drop=%t/target=%g", reverse, drop, target), func(t *testing.T) {
					s := polygonWorld()
					points := rectangle(.45, .1, .55, .9)
					if reverse {
						slices.Reverse(points)
					}
					addTestPolygon(t, s, points)
					id := addDragCircle(t, s, .2, .5, 2, 0)
					s.Drag(DragSpec{X: .19, Y: .5})
					s.Carry(target, .5)
					if drop {
						s.Drop()
					} else {
						s.Step(.1)
					}
					c := s.Snapshot(nil)[0]
					nearBridge(t, "blocked X", float64(c.X), .43)
					if c.ID != id {
						t.Fatal("ID changed")
					}
					assertPolygonClear(t, s)
					if !drop {
						for range 5 {
							s.Step(.1)
							assertPolygonClear(t, s)
						}
						nearBridge(t, "held at obstacle", float64(s.Snapshot(nil)[0].X), .43)
						s.Carry(.2, .7)
						s.Step(.1)
						nearBridge(t, "move away", float64(s.Snapshot(nil)[0].X), .21)
					}
				})
			}
		}
	}
}

func TestPolygonSweepFeatures(t *testing.T) {
	for _, tc := range []struct {
		name          string
		start, target Point
		want          Point
	}{
		{"edge slide", Point{.2, .3}, Point{.6, .6}, Point{.38, .6}},
		{"closing edge", Point{.2, .5}, Point{.8, .5}, Point{.38, .5}},
		{"vertex", Point{.2, .2}, Point{.5, .5}, Point{.4 - .02/float32(math.Sqrt2), .4 - .02/float32(math.Sqrt2)}},
		{"tangent", Point{.2, .38}, Point{.8, .38}, Point{.8, .38}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := polygonWorld()
			addTestPolygon(t, s, rectangle(.4, .4, .6, .8))
			addDragCircle(t, s, tc.start.X, tc.start.Y, 1, 0)
			s.Drag(DragSpec{X: tc.start.X, Y: tc.start.Y})
			s.Carry(tc.target.X, tc.target.Y)
			s.Drop()
			c := s.Snapshot(nil)[0]
			nearBridge(t, "X", float64(c.X), float64(tc.want.X))
			nearBridge(t, "Y", float64(c.Y), float64(tc.want.Y))
			assertPolygonClear(t, s)
		})
	}
}

func TestConcavePolygon(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		s := polygonWorld()
		// L shape: the upper-right quadrant of its AABB is free space.
		points := []Point{{.3, .3}, {.7, .3}, {.7, .4}, {.4, .4}, {.4, .7}, {.3, .7}}
		if reverse {
			slices.Reverse(points)
		}
		addTestPolygon(t, s, points)
		addDragCircle(t, s, .6, .6, 1, 0)
		s.Drag(DragSpec{X: .6, Y: .6})
		s.Carry(.5, .5)
		s.Step(.1)
		c := s.Snapshot(nil)[0]
		nearBridge(t, "concavity remains empty", float64(c.X), .5)
		s.Carry(.35, .35)
		s.Step(.1)
		c = s.Snapshot(nil)[0]
		nearBridge(t, "concave X", float64(c.X), .42)
		nearBridge(t, "concave Y", float64(c.Y), .42)
		assertPolygonClear(t, s)
		s.Carry(.6, .6)
		s.Drop()
		c = s.Snapshot(nil)[0]
		nearBridge(t, "exit corner", float64(c.X), .6)
	}
}

func TestPolygonFastPhysicsAndRestitution(t *testing.T) {
	for _, restitution := range []float32{0, .5, 1} {
		s := polygonWorld()
		s.cfg.Restitution = restitution
		addTestPolygon(t, s, rectangle(.45, .1, .451, .9))
		_, err := s.AddCircle(CircleSpec{X: .2, Y: .5, VX: 10, InnerRadius: .02, OuterRadius: .04, Mass: 1})
		if err != nil {
			t.Fatal(err)
		}
		s.Step(.07)
		c := s.Snapshot(nil)[0]
		nearBridge(t, "swept core", float64(c.X), .43)
		nearBridge(t, "bounce", float64(c.VX), float64(-10*restitution))
		assertPolygonClear(t, s)
	}
}

func TestPolygonInitialPenetration(t *testing.T) {
	for _, polygonFirst := range []bool{false, true} {
		for _, position := range []Point{{.5, .5}, {.4, .5}, {.39, .5}, {.4, .4}} {
			s := polygonWorld()
			if polygonFirst {
				addTestPolygon(t, s, rectangle(.4, .4, .6, .6))
			}
			addDragCircle(t, s, position.X, position.Y, 1, 0)
			if !polygonFirst {
				addTestPolygon(t, s, rectangle(.4, .4, .6, .6))
			}
			assertPolygonClear(t, s)
			s.Step(.01)
			assertPolygonClear(t, s)
		}
	}
}

func TestPolygonShellAndGridInvalidation(t *testing.T) {
	s := polygonWorld()
	addDragCircle(t, s, .42, .5, 2, 0)
	s.rebuildGrid()
	id := addTestPolygon(t, s, rectangle(.45, .1, .55, .9))
	s.rebuildGrid()
	s.ensureWorkBuffers()
	s.solveCells(nil)
	// Shell penetration .01, thickness .02, stiffness 80, inverse mass .5.
	nearBridge(t, "shell force", float64(s.ax[0]), -.2)
	s.RemovePolygon(id)
	s.rebuildGrid()
	s.ensureWorkBuffers()
	s.solveCells(nil)
	nearBridge(t, "removed shell force", float64(s.ax[0]), 0)
	addTestPolygon(t, s, rectangle(.45, .1, .55, .9))
	_, err := s.AddCircle(CircleSpec{X: .62, Y: .5, InnerRadius: .04, OuterRadius: .1, Mass: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetBounds(Bounds{MinX: -1, MinY: -1, MaxX: 2, MaxY: 2}); err != nil {
		t.Fatal(err)
	}
	s.rebuildGrid()
	s.ensureWorkBuffers()
	s.solveCells(nil)
	s.indexBridges()
	nearBridge(t, "small shell after resize", float64(s.ax[s.bridgeIndex[1]]), -.2)
	nearBridge(t, "larger shell after resize", float64(s.ax[s.bridgeIndex[2]]), 1.2)
}

func TestPolygonBridgeCannotCross(t *testing.T) {
	s := polygonWorld()
	s.cfg.BridgeIterations = 16
	addTestPolygon(t, s, rectangle(.45, .1, .55, .9))
	a := addDragCircle(t, s, .2, .5, 1, 0)
	b := addDragCircle(t, s, .8, .5, 1, 0)
	addTestBridge(t, s, BridgeSpec{A: a, B: b, MaxDistance: .05, ConstrainDistance: true, Damping: 10})
	for range 5 {
		s.Step(.1)
		assertPolygonClear(t, s)
		c := bridgeCircles(s)
		if c[0].X > .430001 || c[1].X < .569999 {
			t.Fatalf("bridge crossed solid: %+v", c)
		}
	}
}

func TestPolygonDeterministicWorkers(t *testing.T) {
	worlds := []*State{polygonWorld(), polygonWorld()}
	worlds[1].cfg.Workers = 4
	for _, s := range worlds {
		s.cfg.GridSpacing = .06
		addTestPolygon(t, s, []Point{{.4, .3}, {.6, .3}, {.6, .4}, {.5, .4}, {.5, .7}, {.4, .7}})
		for i := range 600 {
			_, err := s.AddCircle(CircleSpec{X: .05 + float32(i%30)*.03, Y: .05 + float32(i/30)*.045, VX: .5, VY: -.2, InnerRadius: .005, OuterRadius: .01, Mass: 1})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	for range 5 {
		for _, s := range worlds {
			s.Step(.03)
			assertPolygonClear(t, s)
		}
	}
	if !slices.Equal(bridgeCircles(worlds[0]), bridgeCircles(worlds[1])) {
		t.Fatal("polygon collisions depend on worker count")
	}
}

func TestPolygonRecoveryAtWorldBoundary(t *testing.T) {
	s := polygonWorld()
	addTestPolygon(t, s, rectangle(-.2, .1, .3, .9))
	addDragCircle(t, s, .02, .5, 1, 0)
	c := s.Snapshot(nil)[0]
	nearBridge(t, "exit into world", float64(c.X), .32)
	assertPolygonClear(t, s)
}

func TestPolygonSlideStaysWithinWorld(t *testing.T) {
	s := polygonWorld()
	addTestPolygon(t, s, []Point{{.1, .1}, {.9, .9}, {.9, .1}})
	addDragCircle(t, s, .2, .8, 1, 0)
	s.Drag(DragSpec{X: .2, Y: .8})
	for _, target := range []Point{{1, 1}, {1, 0}, {0, 0}, {1, 0}} {
		s.Carry(target.X, target.Y)
		s.Step(.1)
		c := s.Snapshot(nil)[0]
		if c.X < .019999 || c.X > .980001 || c.Y < .019999 || c.Y > .980001 {
			t.Fatalf("escaped world: %+v", c)
		}
		assertPolygonClear(t, s)
	}
}

func TestMultiplePolygonsAndNegativeCoordinates(t *testing.T) {
	for _, substeps := range []int{1, 4} {
		s := polygonWorld()
		s.cfg.Substeps = substeps
		if err := s.SetBounds(Bounds{MinX: -1, MinY: -1, MaxX: 1, MaxY: 1}); err != nil {
			t.Fatal(err)
		}
		addTestPolygon(t, s, rectangle(-.2, -.8, -.1, .8))
		addTestPolygon(t, s, rectangle(.1, -.8, .2, .8))
		addDragCircle(t, s, 0, 0, 1, 0)
		s.Drag(DragSpec{X: 0, Y: 0})
		for _, target := range []float32{.9, -.9, .9, -.9} {
			s.Carry(target, .2)
			s.Step(.1)
			c := s.Snapshot(nil)[0]
			if c.X < -.080001 || c.X > .080001 {
				t.Fatalf("escaped corridor: %+v", c)
			}
			assertPolygonClear(t, s)
		}
	}
}
