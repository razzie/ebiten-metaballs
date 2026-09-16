package softbody

import (
	"math"
	"testing"
)

func assertResponse(t *testing.T, got, want [6]float32) {
	t.Helper()
	for i, name := range [...]string{"ax", "ay", "dvx", "dvy", "cx", "cy"} {
		if math.IsNaN(float64(got[i])) || math.Abs(float64(got[i]-want[i])) > 1e-5 {
			t.Errorf("%s = %g, want %g", name, got[i], want[i])
		}
	}
}

func TestPairCollisionResponse(t *testing.T) {
	cfg := Config{ShellStiffness: 8, ShellDamping: 2, CoreCorrection: .8, Restitution: .5}
	for _, tt := range []struct {
		name         string
		distance, vx float32
		want         [6]float32
	}{
		{"separated", 1.25, 0, [6]float32{}},
		{"shells touching", 1, 0, [6]float32{}},
		// Combined radii are .5 (core) and 1 (shell); masses are 1 and 2.
		{"shell compression", .75, 0, [6]float32{-1, 0, 0, 0, 0, 0}},
		{"shell damping while approaching", .75, 1, [6]float32{-3, 0, 0, 0, 0, 0}},
		{"shell never pulls while separating", .75, -1, [6]float32{}},
		{"cores touching", .5, 1, [6]float32{-6, 0, 0, 0, 0, 0}},
		{"core overlap approaching", .25, 1, [6]float32{-8, 0, -1, 0, -2.0 / 15, 0}},
		{"core overlap separating", .25, -1, [6]float32{-4, 0, 0, 0, -2.0 / 15, 0}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var p particleData
			p.resize(2)
			p.id[0], p.id[1] = 1, 2
			p.x[1], p.vx[0] = tt.distance, tt.vx
			p.inner[0], p.inner[1] = .25, .25
			p.outer[0], p.outer[1] = .5, .5
			p.invMass[0], p.invMass[1] = 1, .5
			a, b, c, d, e, f := pairScalar(&p, 0, 1, cfg)
			assertResponse(t, [6]float32{a, b, c, d, e, f}, tt.want)
			a, b, c, d, e, f = pairScalar(&p, 1, 0, cfg)
			var reverse [6]float32
			for i, v := range tt.want {
				reverse[i] = -v / 2
			}
			assertResponse(t, [6]float32{a, b, c, d, e, f}, reverse)
		})
	}
}

func TestCoincidentPairSeparatesByID(t *testing.T) {
	var p particleData
	p.resize(2)
	for i := range 2 {
		p.id[i], p.inner[i], p.outer[i], p.invMass[i] = uint64(i+1), .1, .2, 1
	}
	cfg := DefaultConfig()
	cfg.AttractionRange, cfg.AttractionStrength = .5, 100
	a, b, c, d, e, f := pairScalar(&p, 0, 1, cfg)
	left := [6]float32{a, b, c, d, e, f}
	a, b, c, d, e, f = pairScalar(&p, 1, 0, cfg)
	var want [6]float32
	for i, v := range left {
		want[i] = -v
	}
	assertResponse(t, [6]float32{a, b, c, d, e, f}, want)
	if left[0] >= 0 || left[4] >= 0 || left[1] != 0 || left[5] != 0 {
		t.Fatalf("coincident response does not separate along ID-derived normal: %v", left)
	}
	cfg.AttractionStrength = 0
	a, b, c, d, e, f = pairScalar(&p, 0, 1, cfg)
	assertResponse(t, [6]float32{a, b, c, d, e, f}, left)
	// The normal follows stable IDs even when storage order changes.
	p.id[0], p.id[1] = p.id[1], p.id[0]
	a, b, c, d, e, f = pairScalar(&p, 0, 1, cfg)
	assertResponse(t, [6]float32{a, b, c, d, e, f}, want)
}

func TestWallCoreResponse(t *testing.T) {
	for _, tt := range []struct {
		name         string
		x, y, vx, vy float32
		want         [6]float32
	}{
		{"left", .125, .5, -1, 0, [6]float32{1, 0, 1.5, 0, .1, 0}},
		{"right", .875, .5, 1, 0, [6]float32{-1, 0, -1.5, 0, -.1, 0}},
		{"bottom", .5, .125, 0, -1, [6]float32{0, 1, 0, 1.5, 0, .1}},
		{"top", .5, .875, 0, 1, [6]float32{0, -1, 0, -1.5, 0, -.1}},
		{"moving inward", .125, .5, 1, 0, [6]float32{0, 0, 0, 0, .1, 0}},
		{"corner", .125, .125, -1, -1, [6]float32{1, 1, 1.5, 1.5, .1, .1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := New(Config{ShellStiffness: 4, ShellDamping: 1, CoreCorrection: .8, Restitution: .5})
			if _, err := s.AddCircle(CircleSpec{X: .5, Y: .5, InnerRadius: .25, OuterRadius: .375, Mass: 2}); err != nil {
				t.Fatal(err)
			}
			// A solver can encounter a penetrated core before integration confines it.
			s.p.x[0], s.p.y[0], s.p.vx[0], s.p.vy[0] = tt.x, tt.y, tt.vx, tt.vy
			a, b, c, d, e, f := s.wallResponse(0)
			assertResponse(t, [6]float32{a, b, c, d, e, f}, tt.want)
		})
	}
}

func TestRadialImpulseResponseFalloffAndExclusions(t *testing.T) {
	for _, tt := range []struct {
		name               string
		radius, impulse, x float32
		group              Group
		want               float32
	}{
		{"quadratic falloff and mass", .5, 2, .75, 0, .25},
		{"opposite direction", .5, 2, .25, 0, -.25},
		{"at center", .5, 2, .5, 0, 0},
		{"at radius", .5, 2, 1, 0, 0},
		{"outside radius", .5, 2, 1.25, 0, 0},
		{"other group", .5, 2, .75, 1000, 0},
		{"disabled impulse", .5, 0, .75, 0, 0},
		{"infinite radius", float32(math.Inf(1)), 2, .75, 0, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := New(DefaultConfig())
			if _, err := s.AddCircle(CircleSpec{X: .5, Y: .5, InnerRadius: .01, OuterRadius: .02, Mass: 2, Group: 0}); err != nil {
				t.Fatal(err)
			}
			vx, vy := s.radialImpulseResponse(0, []RadialImpulse{{X: tt.x, Y: .5, Radius: tt.radius, Strength: -tt.impulse, Groups: []Group{tt.group}}})
			if vx != tt.want || vy != 0 {
				t.Fatalf("radial impulse response = (%g, %g), want (%g, 0)", vx, vy, tt.want)
			}
		})
	}
}
