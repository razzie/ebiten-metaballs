package softbody

import (
	"math"
	"slices"
	"sync"
	"testing"
)

func TestNewConfig(t *testing.T) {
	defaults := DefaultConfig()
	zeroDefaults := defaults
	zeroDefaults.Restitution = 0 // Zero restitution explicitly requests no bounce.
	custom := Config{
		GridSpacing: .1, Workers: 3, Substeps: 4,
		ShellStiffness: 12, ShellDamping: 3, Restitution: .5,
		CoreCorrection: .7, LinearDamping: .2,
		LinearDampingMassFactor: .5,
		AttractionRange:         .3, AttractionStrength: 2,
	}
	clamped := zeroDefaults
	clamped.Workers, clamped.Substeps = 1, 1
	maxRestitution := zeroDefaults
	maxRestitution.Restitution = 1
	for _, tt := range []struct {
		name     string
		in, want Config
	}{
		{"zero", Config{}, zeroDefaults},
		{"defaults", defaults, defaults},
		{"custom", custom, custom},
		{"negative counts and restitution", Config{Workers: -2, Substeps: -3, Restitution: -1}, clamped},
		{"excess restitution", Config{Restitution: 2}, maxRestitution},
		{"negative mass damping factor", Config{LinearDampingMassFactor: -1}, zeroDefaults},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := New(tt.in)
			if got := s.Config(); got != tt.want {
				t.Fatalf("Config() = %+v, want %+v", got, tt.want)
			}
			if s.Len() != 0 || len(s.Snapshot(nil)) != 0 {
				t.Fatal("new state contains circles")
			}
		})
	}
}

func TestAddCircleValidation(t *testing.T) {
	valid := CircleSpec{X: .5, Y: .5, InnerRadius: .1, OuterRadius: .2, Mass: 2, Group: 17}
	for _, tt := range []struct {
		name   string
		change func(*CircleSpec)
	}{
		{"zero inner radius", func(c *CircleSpec) { c.InnerRadius = 0 }},
		{"negative inner radius", func(c *CircleSpec) { c.InnerRadius = -.1 }},
		{"outer smaller than inner", func(c *CircleSpec) { c.OuterRadius = .05 }},
		{"diameter exceeds width", func(c *CircleSpec) { c.OuterRadius = .6 }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := New(DefaultConfig())
			if err := s.SetBounds(Bounds{MaxX: 1, MaxY: 2}); err != nil {
				t.Fatal(err)
			}
			c := valid
			tt.change(&c)
			if id, err := s.AddCircle(c); err == nil || id != 0 {
				t.Fatalf("AddCircle(%+v) = (%d, %v), want (0, error)", c, id, err)
			}
			if s.Len() != 0 {
				t.Fatal("invalid circle changed state")
			}
			if id, err := s.AddCircle(valid); err != nil || id != 1 {
				t.Fatalf("first valid circle = (%d, %v), want ID 1", id, err)
			}
		})
	}
}

func TestAddCircleClampsCoreAndDefaultsMass(t *testing.T) {
	s := New(DefaultConfig())
	if err := s.SetBounds(Bounds{MinX: -1, MinY: -2, MaxX: 1, MaxY: 2}); err != nil {
		t.Fatal(err)
	}
	for i, mass := range []float32{0, -2, 4} {
		id, err := s.AddCircle(CircleSpec{X: -10, Y: 10, VX: 2, VY: -3, InnerRadius: .25, OuterRadius: 1, Mass: mass, Group: Group(i)})
		if err != nil {
			t.Fatal(err)
		}
		want := CircleSnapshot{ID: uint64(i + 1), X: -.75, Y: 1.75, VX: 2, VY: -3, InnerRadius: .25, OuterRadius: 1, Group: Group(i)}
		if got := s.Snapshot(nil)[i]; got != want || id != want.ID {
			t.Fatalf("circle %d = %+v (ID %d), want %+v", i, got, id, want)
		}
		wantInvMass := float32(1)
		if mass > 0 {
			wantInvMass = 1 / mass
		}
		if s.p.invMass[i] != wantInvMass {
			t.Fatalf("inverse mass = %g, want %g", s.p.invMass[i], wantInvMass)
		}
	}
	if s.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", s.Len())
	}
}

func TestSetBoundsRejectsInvalidWithoutMutation(t *testing.T) {
	for _, b := range []Bounds{
		{}, {MaxX: -1, MaxY: 1}, {MaxX: 1, MaxY: -1},
		{MaxX: .3, MaxY: 1}, {MaxX: 1, MaxY: .3},
		{MinX: float32(math.NaN()), MaxX: 1, MaxY: 1},
		{MinY: float32(math.Inf(-1)), MaxX: 1, MaxY: 1},
		{MaxX: float32(math.Inf(1)), MaxY: 1},
		{MaxX: 1, MaxY: float32(math.NaN())},
	} {
		s := New(DefaultConfig())
		if _, err := s.AddCircle(CircleSpec{X: .5, Y: .5, InnerRadius: .1, OuterRadius: .2}); err != nil {
			t.Fatal(err)
		}
		before, bounds := s.Snapshot(nil), s.bounds
		if err := s.SetBounds(b); err == nil {
			t.Errorf("SetBounds(%+v) succeeded", b)
		}
		if s.bounds != bounds || !slices.Equal(before, s.Snapshot(nil)) {
			t.Fatalf("invalid bounds %+v mutated state", b)
		}
	}
}

func TestSnapshotPreservesIdentityThroughReordering(t *testing.T) {
	s := New(DefaultConfig())
	want := make(map[uint64]CircleSnapshot)
	for i, x := range []float32{.8, .2, .5} {
		if _, err := s.AddCircle(CircleSpec{X: x, Y: .5, VX: float32(i), VY: -float32(i), InnerRadius: .01 * float32(i+1), OuterRadius: .04, Mass: float32(i + 1), Group: Group(i)}); err != nil {
			t.Fatal(err)
		}
	}
	before := s.Snapshot(nil)
	for _, c := range before {
		want[c.ID] = c
	}
	s.rebuildGrid()
	if s.p.id[0] == before[0].ID {
		t.Fatal("fixture did not exercise reordering")
	}
	storage := make([]CircleSnapshot, s.Len()+2)
	sentinel := CircleSnapshot{ID: 999}
	storage[s.Len()] = sentinel
	got := s.Snapshot(storage[:0])
	if len(got) != s.Len() || &got[0] != &storage[0] {
		t.Fatal("Snapshot did not reuse available capacity")
	}
	if storage[s.Len()] != sentinel {
		t.Fatal("Snapshot wrote beyond returned length")
	}
	seen := make(map[uint64]bool)
	for i, c := range got {
		if seen[c.ID] || c != want[c.ID] {
			t.Fatalf("identity or properties changed: %+v, want %+v", c, want[c.ID])
		}
		seen[c.ID] = true
		if s.p.invMass[i] != 1/float32(c.ID) {
			t.Fatalf("mass detached from ID %d", c.ID)
		}
	}
	got[0].X = 99
	if s.Snapshot(nil)[0].X == 99 {
		t.Fatal("snapshot aliases simulation state")
	}
	for _, c := range before {
		if c != want[c.ID] {
			t.Fatal("reordering mutated an earlier snapshot")
		}
	}
}

func TestImpulsesConsumedOnceAcrossSubsteps(t *testing.T) {
	for _, strength := range []float32{-2, 2} {
		for _, group := range []Group{0, 17, 1000} {
			cfg := DefaultConfig()
			cfg.Substeps, cfg.LinearDamping = 4, -1
			s := New(cfg)
			impulse := RadialImpulse{X: .75, Y: .5, Radius: .5, Strength: strength, Groups: []Group{group}}
			s.QueueRadialImpulse(impulse)
			s.Step(.01) // Empty steps retain queued impulses.
			if _, err := s.AddCircle(CircleSpec{X: .5, Y: .5, InnerRadius: .01, OuterRadius: .02, Mass: 2, Group: group}); err != nil {
				t.Fatal(err)
			}
			before := s.Snapshot(nil)
			s.Step(0)
			s.Step(-1)
			if !slices.Equal(before, s.Snapshot(nil)) {
				t.Fatal("queued impulse or nonpositive timestep changed state")
			}
			for step := range 2 {
				s.Step(.01)
				c := s.Snapshot(nil)[0]
				// Half the radius gives 1/4 strength, divided by mass 2.
				if c.VX != -strength/8 || c.VY != 0 {
					t.Fatalf("group %d, step %d: velocity (%g, %g)", group, step, c.VX, c.VY)
				}
			}
		}
	}
}

func TestImpulseSourceUnaffectedByBounds(t *testing.T) {
	cfg := DefaultConfig()
	cfg.LinearDamping = -1
	s := New(cfg)
	if _, err := s.AddCircle(CircleSpec{X: .5, Y: .5, InnerRadius: .01, OuterRadius: .02}); err != nil {
		t.Fatal(err)
	}
	s.QueueRadialImpulse(RadialImpulse{X: 1.5, Y: .5, Radius: 2, Strength: -1})
	if err := s.SetBounds(Bounds{MinX: .2, MinY: .2, MaxX: .8, MaxY: .8}); err != nil {
		t.Fatal(err)
	}
	s.Step(.01)
	if c := s.Snapshot(nil)[0]; c.VX != .25 || c.VY != 0 {
		t.Fatalf("source was clamped to bounds: %+v", c)
	}
}

func TestQueueRadialImpulseConcurrentWithStep(t *testing.T) {
	cfg := DefaultConfig()
	cfg.LinearDamping = -1
	s := New(cfg)
	if _, err := s.AddCircle(CircleSpec{X: .5, Y: .5, InnerRadius: .01, OuterRadius: .02, Group: 1000}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 4 {
		wg.Go(func() {
			<-start
			for range 100 {
				s.QueueRadialImpulse(RadialImpulse{X: 0, Y: .5, Radius: float32(math.Inf(1)), Strength: .001})
			}
		})
	}
	close(start)
	for range 100 {
		s.Step(.001)
	}
	wg.Wait()
	s.Step(.001)
	if len(s.consumeImpulses()) != 0 {
		t.Fatal("Step left impulses queued")
	}
	if c := s.Snapshot(nil)[0]; math.Abs(float64(c.VX-.4)) > 1e-5 || c.VY != 0 {
		t.Fatalf("concurrent impulses lost or duplicated: %+v", c)
	}
}
