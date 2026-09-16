package softbody

import (
	"math"
	"math/rand/v2"
	"testing"
)

func TestGroupAttraction(t *testing.T) {
	for _, tt := range []struct {
		name     string
		group    Group
		gap      float32
		strength float32
		wantPull bool
	}{
		{"near same group", Red, .0075, .3, true},
		{"near other group", Blue, .0075, .3, false},
		{"outside range", Red, .016, .3, false},
		{"disabled", Red, .0075, 0, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.AttractionRange, cfg.AttractionStrength = .015, tt.strength
			cfg.Substeps = 1
			s := New(cfg)
			for i, group := range []Group{Red, tt.group} {
				_, err := s.AddCircle(CircleSpec{
					X: .4 + float32(i)*(.05+tt.gap), Y: .5,
					InnerRadius: .015, OuterRadius: .025,
					Mass: float32(i + 1), Group: group,
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			s.Step(1.0 / 60)
			var left, right CircleSnapshot
			for _, c := range s.Snapshot(nil) {
				if c.ID == 1 {
					left = c
				} else {
					right = c
				}
			}
			if tt.wantPull {
				if left.VX <= 0 || right.VX >= 0 {
					t.Fatalf("near same-group circles did not attract: %+v %+v", left, right)
				}
				if math.Abs(float64(left.VX+2*right.VX)) > 1e-7 {
					t.Fatal("attraction did not conserve momentum for unequal masses")
				}
			} else if left.VX != 0 || right.VX != 0 {
				t.Fatalf("unexpected attraction: %+v %+v", left, right)
			}
		})
	}
}

func TestAttractionGridMatchesAllPairs(t *testing.T) {
	for _, spacing := range []float32{0, .006, .08} {
		cfg := DefaultConfig()
		cfg.GridSpacing = spacing
		cfg.AttractionRange, cfg.AttractionStrength = .015, .3
		cfg.Workers = 4
		s := New(cfg)
		rng := rand.New(rand.NewPCG(12, 34))
		for i := range 100 {
			_, err := s.AddCircle(CircleSpec{
				X: .4 + .2*rng.Float32(), Y: .4 + .2*rng.Float32(),
				InnerRadius: .003, OuterRadius: .004 + .008*rng.Float32(),
				Mass: .5 + rng.Float32(), Group: Group(i % 3),
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		s.rebuildGrid()
		s.ensureWorkBuffers()
		s.solveCells(nil)
		for i := range s.p.len() {
			var wantX, wantY float32
			for j := range s.p.len() {
				if i != j {
					x, y, _, _, _, _ := pairScalar(&s.p, i, j, cfg)
					wantX += x
					wantY += y
				}
			}
			if math.Abs(float64(s.ax[i]-wantX)) > 1e-5 || math.Abs(float64(s.ay[i]-wantY)) > 1e-5 {
				t.Fatalf("spacing %g, circle %d: grid acceleration (%g,%g), all-pairs (%g,%g)", spacing, i, s.ax[i], s.ay[i], wantX, wantY)
			}
		}
	}
}

func TestAttractionSpanMatchesScalar(t *testing.T) {
	// Cover full SIMD vectors and a scalar tail, mixed groups, shell/core
	// overlaps, coincident centers, and pairs beyond the attraction cutoff.
	var p particleData
	p.resize(66)
	for i := range p.len() {
		p.id[i], p.group[i] = uint64(i+1), Group(i%3)
		p.x[i], p.y[i] = .4+float32(i%11)*.01, .5+float32(i%5)*.007
		p.inner[i], p.outer[i], p.invMass[i] = .015, .025, 1/float32(i+1)
	}
	cfg := DefaultConfig()
	cfg.AttractionRange, cfg.AttractionStrength = .015, .3
	var want [6]float32
	for j := 1; j < p.len(); j++ {
		a, b, c, d, e, f := pairScalar(&p, 0, j, cfg)
		for k, v := range [...]float32{a, b, c, d, e, f} {
			want[k] += v
		}
	}
	a, b, c, d, e, f := pairSpanKernel(&p, 0, 1, p.len(), cfg)
	for k, got := range [...]float32{a, b, c, d, e, f} {
		if math.Abs(float64(got-want[k])) > 1e-5 {
			t.Fatalf("component %d: span %g, scalar %g", k, got, want[k])
		}
	}
}
