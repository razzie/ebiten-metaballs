package softbody

import (
	"math"
	"testing"
)

func TestResizeBounds(t *testing.T) {
	s := New(DefaultConfig())
	wide := Bounds{MinX: -1, MaxX: 2, MaxY: 1}
	if err := s.SetBounds(wide); err != nil {
		t.Fatal(err)
	}
	_, err := s.AddCircle(CircleSpec{X: 1.8, Y: .5, VX: 2, InnerRadius: .03, OuterRadius: .05, Group: 1000})
	if err != nil {
		t.Fatal(err)
	}
	s.Step(1.0 / 60)
	before := s.Snapshot(nil)[0]
	if before.X <= 1 {
		t.Fatalf("expanded world still uses unit-square walls: %+v", before)
	}
	tall := Bounds{MinY: -1, MaxX: 1, MaxY: 2}
	if err := s.SetBounds(tall); err != nil {
		t.Fatal(err)
	}
	after := s.Snapshot(nil)[0]
	if after.X > tall.MaxX-after.InnerRadius || after.VX > 0 {
		t.Fatalf("resize did not immediately confine and reflect circle: %+v", after)
	}
	if after.ID != before.ID || after.Group != before.Group || after.OuterRadius != before.OuterRadius {
		t.Fatalf("resize changed circle identity or size: before=%+v after=%+v", before, after)
	}
	for range 60 {
		s.Step(1.0 / 60)
	}
	after = s.Snapshot(nil)[0]
	if after.X < tall.MinX+after.InnerRadius || after.X > tall.MaxX-after.InnerRadius || after.Y < tall.MinY+after.InnerRadius || after.Y > tall.MaxY-after.InnerRadius {
		t.Fatalf("circle escaped resized world: %+v", after)
	}
}

func TestIntegrationUsesRectangularBounds(t *testing.T) {
	// Exercise full SIMD vectors and a scalar tail in the SIMD build.
	const count = 65
	var p particleData
	p.resize(count)
	b := Bounds{MinX: -2, MinY: -1, MaxX: 3, MaxY: 2}
	for i := range count {
		p.x[i], p.y[i], p.inner[i] = .5, .5, .02
		p.vx[i], p.vy[i] = 100, 100
		if i%2 == 0 {
			p.vx[i] = -100
		}
		if i%3 == 0 {
			p.vy[i] = -100
		}
	}
	zero := make([]float32, count)
	integrateKernel(&p, zero, zero, zero, zero, zero, zero, 1, 1, .1, b, 1)
	for i := range count {
		wantX, wantVX := b.MaxX-p.inner[i], float32(-10)
		wantY, wantVY := b.MaxY-p.inner[i], float32(-10)
		if i%2 == 0 {
			wantX, wantVX = b.MinX+p.inner[i], 10
		}
		if i%3 == 0 {
			wantY, wantVY = b.MinY+p.inner[i], 10
		}
		if p.x[i] != wantX || p.y[i] != wantY || p.vx[i] != wantVX || p.vy[i] != wantVY {
			t.Fatalf("circle %d: got (%g,%g) velocity (%g,%g), want (%g,%g) velocity (%g,%g)", i, p.x[i], p.y[i], p.vx[i], p.vy[i], wantX, wantY, wantVX, wantVY)
		}
	}
}

func TestCollisionsOutsideUnitSquare(t *testing.T) {
	for _, center := range []float32{-1.5, 2.5} {
		s := New(DefaultConfig())
		if err := s.SetBounds(Bounds{MinX: -2, MinY: -2, MaxX: 3, MaxY: 3}); err != nil {
			t.Fatal(err)
		}
		for i := range 2 {
			_, err := s.AddCircle(CircleSpec{X: center + float32(i)*.04, Y: center, InnerRadius: .04, OuterRadius: .07, Group: Group(i)})
			if err != nil {
				t.Fatal(err)
			}
		}
		for range 20 {
			s.Step(1.0 / 120)
		}
		c := s.Snapshot(nil)
		if math.Abs(float64(c[0].X-c[1].X)) < .075 {
			t.Fatalf("cores outside unit square did not separate: %+v", c)
		}
		for i := range s.p.len() {
			q, r := roundAxial(s.p.x[i]*s.grid.invS-.5*s.p.y[i]*s.grid.invH, s.p.y[i]*s.grid.invH, s.p.id[i])
			if s.grid.index(q, r) < 0 {
				t.Fatal("resized hex grid does not cover a circle")
			}
		}
	}
}

func TestAttractionOutsideUnitSquare(t *testing.T) {
	for _, target := range []float32{-1.5, 2.5} {
		cfg := DefaultConfig()
		s := New(cfg)
		if err := s.SetBounds(Bounds{MinX: -2, MaxX: 3, MaxY: 1}); err != nil {
			t.Fatal(err)
		}
		// Put the circle between the target and the old unit-square boundary.
		x := (target + .5) / 2
		_, err := s.AddCircle(CircleSpec{X: x, Y: .5, InnerRadius: .01, OuterRadius: .02, Group: 17})
		if err != nil {
			t.Fatal(err)
		}
		s.QueueRadialImpulse(RadialImpulse{X: target, Y: .5, Radius: float32(math.Inf(1)), Strength: -.35, Groups: []Group{17}})
		s.Step(1.0 / 60)
		if c := s.Snapshot(nil)[0]; c.VX*(target-x) <= 0 {
			t.Fatalf("circle was not attracted toward resized-world target %g: %+v", target, c)
		}
	}
}

func TestCollisionSeparatesCores(t *testing.T) {
	s := New(DefaultConfig())
	_, _ = s.AddCircle(CircleSpec{X: .48, Y: .5, InnerRadius: .04, OuterRadius: .07, Group: 0})
	_, _ = s.AddCircle(CircleSpec{X: .52, Y: .5, InnerRadius: .04, OuterRadius: .07, Group: 1000})
	for range 20 {
		s.Step(1.0 / 120.0)
	}
	c := s.Snapshot(nil)
	dx := c[0].X - c[1].X
	if dx < 0 {
		dx = -dx
	}
	if dx < .075 {
		t.Fatalf("cores did not separate enough: dx=%f", dx)
	}
}

func TestImpulseTargetsOnlyGroup(t *testing.T) {
	s := New(DefaultConfig())
	_, _ = s.AddCircle(CircleSpec{X: .4, Y: .5, InnerRadius: .01, OuterRadius: .02, Group: 0})
	_, _ = s.AddCircle(CircleSpec{X: .4, Y: .6, InnerRadius: .01, OuterRadius: .02, Group: 1000})
	s.QueueRadialImpulse(RadialImpulse{X: .5, Y: .5, Radius: .18, Strength: -.35, Groups: []Group{0}})
	s.Step(1.0 / 60.0)
	c := s.Snapshot(nil)
	var targetVX, otherVX float32
	for _, p := range c {
		if p.Group == 0 {
			targetVX = p.VX
		}
		if p.Group == 1000 {
			otherVX = p.VX
		}
	}
	if targetVX <= 0 {
		t.Fatalf("target group was not attracted: vx=%f", targetVX)
	}
	if otherVX != 0 {
		t.Fatalf("other group was affected by impulse: vx=%f", otherVX)
	}
}

func TestBoundariesContainHardCore(t *testing.T) {
	s := New(DefaultConfig())
	_, _ = s.AddCircle(CircleSpec{X: .06, Y: .5, VX: -10, InnerRadius: .05, OuterRadius: .08, Group: 17})
	for range 4 {
		s.Step(1.0 / 60.0)
	}
	p := s.Snapshot(nil)[0]
	if p.X < p.InnerRadius || p.X > 1-p.InnerRadius || p.Y < p.InnerRadius || p.Y > 1-p.InnerRadius {
		t.Fatalf("core escaped: %+v", p)
	}
}
