package softbody

import (
	"math"
	"slices"
	"testing"
)

func bridgeWorld(t *testing.T, distance float32, substeps int) (*State, uint64, uint64) {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Substeps, cfg.Workers, cfg.LinearDamping = substeps, 4, -1
	s := New(cfg)
	// Insert right first to exercise stable IDs through grid reordering.
	right, err := s.AddCircle(CircleSpec{X: .125 + distance, Y: .5, InnerRadius: .01, OuterRadius: .02, Mass: 2, Group: 99})
	if err != nil {
		t.Fatal(err)
	}
	left, err := s.AddCircle(CircleSpec{X: .125, Y: .5, InnerRadius: .01, OuterRadius: .02, Mass: 1})
	if err != nil {
		t.Fatal(err)
	}
	return s, left, right
}

func addTestBridge(t *testing.T, s *State, b BridgeSpec) uint64 {
	t.Helper()
	id, err := s.AddBridge(b)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestBridgeForces(t *testing.T) {
	for _, tt := range []struct {
		name     string
		distance float32
		wantVX   float32
	}{
		{"repel", .125, -.03},
		{"at minimum", .25, 0},
		{"inside range", .375, 0},
		{"at maximum", .5, 0},
		{"attract", .625, .02},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, substeps := range []int{1, 4} {
				s, left, right := bridgeWorld(t, tt.distance, substeps)
				addTestBridge(t, s, BridgeSpec{A: left, B: right, MinDistance: .25, MaxDistance: .5, AttractForce: 2, RepelForce: 3})
				s.Step(.01)
				for _, c := range s.Snapshot(nil) {
					want := tt.wantVX
					if c.ID == right {
						want = -want / 2
					}
					if math.Abs(float64(c.VX-want)) > 1e-7 || c.VY != 0 {
						t.Fatalf("substeps %d: circle %+v, want VX %g", substeps, c, want)
					}
				}
			}
		})
	}
}

func TestBridgeBreaking(t *testing.T) {
	for _, tt := range []struct {
		name          string
		distance      float32
		breakDistance float32
		wantActive    bool
	}{
		{"below threshold", .375, .5, true},
		{"at threshold", .5, .5, true},
		{"above threshold", .625, .5, false},
		{"unbreakable", .625, 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, left, right := bridgeWorld(t, tt.distance, 1)
			id := addTestBridge(t, s, BridgeSpec{A: left, B: right, MaxDistance: .25, BreakDistance: tt.breakDistance, AttractForce: 2})
			s.Step(0)
			s.Step(-1)
			if len(s.BridgeSnapshot(nil)) != 1 {
				t.Fatal("nonpositive step broke bridge")
			}
			s.Step(.01)
			if got := len(s.BridgeSnapshot(nil)) > 0; got != tt.wantActive {
				t.Fatalf("active = %v, want %v", got, tt.wantActive)
			}
			if !tt.wantActive {
				s.Step(.01)
				for _, c := range s.Snapshot(nil) {
					if c.VX != 0 || c.VY != 0 {
						t.Fatal("broken bridge applied force")
					}
				}
				if s.RemoveBridge(id) {
					t.Fatal("broken bridge remained in state")
				}
			}
		})
	}
}

func TestBridgeBreaksBetweenSubsteps(t *testing.T) {
	s, left, right := bridgeWorld(t, .5, 2)
	addTestBridge(t, s, BridgeSpec{A: left, B: right, MaxDistance: .25, BreakDistance: .5})
	s.p.vx[0], s.p.vx[1] = .25, -.25
	s.Step(.1)
	if len(s.BridgeSnapshot(nil)) != 0 {
		t.Fatal("bridge was not checked again after endpoints moved")
	}
}

func TestBridgeValidation(t *testing.T) {
	s, left, right := bridgeWorld(t, .5, 1)
	valid := BridgeSpec{A: left, B: right, MinDistance: .125, MaxDistance: .25, BreakDistance: .5, AttractForce: 2, RepelForce: 3}
	invalid := []BridgeSpec{}
	for _, endpoints := range [][2]uint64{{0, right}, {left, 0}, {left, left}, {999, right}, {left, 999}} {
		b := valid
		b.A, b.B = endpoints[0], endpoints[1]
		invalid = append(invalid, b)
	}
	for field := range 5 {
		for _, value := range []float32{-1, float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))} {
			b := valid
			fields := []*float32{&b.MinDistance, &b.MaxDistance, &b.BreakDistance, &b.AttractForce, &b.RepelForce}
			*fields[field] = value
			invalid = append(invalid, b)
		}
	}
	b := valid
	b.MinDistance = .375
	invalid = append(invalid, b)
	b = valid
	b.BreakDistance = .125
	invalid = append(invalid, b)
	for _, b := range invalid {
		if id, err := s.AddBridge(b); err == nil || id != 0 {
			t.Fatalf("AddBridge(%+v) = (%d, %v), want (0, error)", b, id, err)
		}
	}
	if len(s.BridgeSnapshot(nil)) != 0 {
		t.Fatal("invalid bridge changed state")
	}
	if id := addTestBridge(t, s, valid); id != 1 {
		t.Fatalf("first bridge ID = %d, want 1", id)
	}
	addTestBridge(t, s, BridgeSpec{A: left, B: right}) // Zero distances and forces are valid.
}

func TestBridgeLifecycleAndSnapshot(t *testing.T) {
	s, left, right := bridgeWorld(t, .5, 1)
	b := BridgeSpec{A: left, B: right, MaxDistance: .25, AttractForce: 2}
	broken := b
	broken.BreakDistance = .375
	first := addTestBridge(t, s, broken)
	second := addTestBridge(t, s, b)
	third := addTestBridge(t, s, broken)
	fourth := addTestBridge(t, s, b)
	before := s.BridgeSnapshot(nil)
	s.Step(.01)
	want := []BridgeSnapshot{{ID: second, BridgeSpec: b}, {ID: fourth, BridgeSpec: b}}
	storage := make([]BridgeSnapshot, 3)
	storage[2].ID = 999
	got := s.BridgeSnapshot(storage[:0])
	if !slices.Equal(got, want) || &got[0] != &storage[0] || storage[2].ID != 999 {
		t.Fatalf("snapshot did not reuse storage correctly: %+v", got)
	}
	got[0].AttractForce = 999
	if !slices.Equal(s.BridgeSnapshot(nil), want) || before[0].ID != first || before[2].ID != third {
		t.Fatal("snapshot aliases bridge state")
	}
	for _, c := range s.Snapshot(nil) {
		wantVX := float32(.04)
		if c.ID == right {
			wantVX = -.02
		}
		if math.Abs(float64(c.VX-wantVX)) > 1e-7 {
			t.Fatalf("surviving bridges did not add forces: %+v", c)
		}
	}
	if !s.RemoveBridge(second) || s.RemoveBridge(second) || s.RemoveBridge(0) || !s.RemoveBridge(fourth) {
		t.Fatal("incorrect removal result")
	}
	if len(s.BridgeSnapshot(nil)) != 0 || addTestBridge(t, s, b) <= fourth {
		t.Fatal("removed bridge persisted or bridge ID was reused")
	}
}

func TestBridgeDirection(t *testing.T) {
	for _, coincident := range []bool{false, true} {
		for _, reverse := range []bool{false, true} {
			s, left, right := bridgeWorld(t, .5, 1)
			s.p.x[0], s.p.y[0] = .5, .75
			s.p.x[1], s.p.y[1] = .5, .25
			if coincident {
				s.p.y[0] = s.p.y[1]
			}
			a, b := left, right
			if reverse {
				a, b = b, a
			}
			addTestBridge(t, s, BridgeSpec{A: a, B: b, MinDistance: .125, MaxDistance: .25, AttractForce: 2, RepelForce: 3})
			s.ensureWorkBuffers()
			s.solveBridges() // Isolate bridge force from coincident core collisions.
			if coincident {
				if s.ax[0] != -1.5 || s.ax[1] != 3 || s.ay[0] != 0 || s.ay[1] != 0 {
					t.Fatal("coincident endpoints did not repel in a stable direction")
				}
			} else if s.ax[0] != 0 || s.ax[1] != 0 || s.ay[0] != -1 || s.ay[1] != 2 {
				t.Fatal("bridge force did not follow endpoint direction")
			}
		}
	}
}

func TestBridgesDoNotChangeCollisions(t *testing.T) {
	for _, distance := range []float32{.01, .5} {
		plain, _, _ := bridgeWorld(t, distance, 1)
		bridged, left, right := bridgeWorld(t, distance, 1)
		for _, s := range []*State{plain, bridged} {
			if _, err := s.AddCircle(CircleSpec{X: .375, Y: .5, InnerRadius: .01, OuterRadius: .02}); err != nil {
				t.Fatal(err)
			}
		}
		addTestBridge(t, bridged, BridgeSpec{A: left, B: right, MaxDistance: 1, AttractForce: 2, RepelForce: 3})
		plain.Step(.01)
		bridged.Step(.01)
		if !slices.Equal(plain.Snapshot(nil), bridged.Snapshot(nil)) {
			t.Fatal("bridge changed endpoint collisions or collided with a third circle")
		}
	}
}
