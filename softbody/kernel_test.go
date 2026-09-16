package softbody

import (
	"fmt"
	"math"
	"testing"
)

func TestFractionalHexKernel(t *testing.T) {
	// Include partial vectors, full vectors, and a tail for supported SIMD widths.
	for _, n := range []int{0, 1, 3, 4, 7, 8, 15, 16, 17, 65} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			x, y, q, r := make([]float32, n), make([]float32, n), make([]float32, n), make([]float32, n)
			for i := range n {
				q[i], r[i] = float32(i%7-3), float32(i%5-2)
				x[i], y[i] = (q[i]+r[i]/2)*.25, r[i]*.5
			}
			gotQ, gotR := make([]float32, n), make([]float32, n)
			fractionalHexKernel(x, y, gotQ, gotR, 4, 2)
			for i := range n {
				if gotQ[i] != q[i] || gotR[i] != r[i] {
					t.Fatalf("point %d: (%g, %g), want (%g, %g)", i, gotQ[i], gotR[i], q[i], r[i])
				}
			}
		})
	}
}

func TestPairSpanMovingParticlesMatchesScalar(t *testing.T) {
	var p particleData
	p.resize(70)
	for i := range p.len() {
		p.id[i], p.group[i] = uint64(i+1), Group(i%3)
		p.x[i], p.y[i] = float32(i%7)*.04, float32(i%5)*.03
		p.vx[i], p.vy[i] = float32(i%3-1)*.5, float32(i%4-2)*.25
		p.inner[i], p.outer[i], p.invMass[i] = .04, .08, 1/float32(i%4+1)
	}
	// This coincident neighbor is inside a full vector in the larger spans.
	p.x[3], p.y[3] = p.x[0], p.y[0]
	for _, attraction := range []bool{false, true} {
		for _, n := range []int{0, 1, 3, 4, 7, 8, 15, 16, 17, 65} {
			t.Run(fmt.Sprintf("attraction=%t/n=%d", attraction, n), func(t *testing.T) {
				cfg := DefaultConfig()
				if attraction {
					cfg.AttractionRange, cfg.AttractionStrength = .1, .3
				}
				var want [6]float32
				for j := 2; j < 2+n; j++ {
					a, b, c, d, e, f := pairScalar(&p, 0, j, cfg)
					for k, v := range [...]float32{a, b, c, d, e, f} {
						want[k] += v
					}
				}
				a, b, c, d, e, f := pairSpanKernel(&p, 0, 2, 2+n, cfg)
				assertResponse(t, [6]float32{a, b, c, d, e, f}, want)
			})
		}
	}
}

func TestIntegrateKernelForcesAndCorrections(t *testing.T) {
	for _, workers := range []int{1, 4} {
		t.Run(fmt.Sprintf("workers=%d", workers), func(t *testing.T) {
			// Cross the 512-particle parallel grain and leave a SIMD tail.
			const n = 1025
			var p particleData
			p.resize(n)
			ax, ay, dvx, dvy, cx, cy := make([]float32, n), make([]float32, n), make([]float32, n), make([]float32, n), make([]float32, n), make([]float32, n)
			for i := range n {
				p.x[i], p.y[i], p.vx[i], p.vy[i], p.inner[i] = .5, .5, .2, -.4, .1
				ax[i], ay[i], dvx[i], dvy[i], cx[i], cy[i] = .4, -.8, .1, .2, .02, -.01
			}
			integrateKernel(&p, ax, ay, dvx, dvy, cx, cy, .25, .5, .5, Bounds{MaxX: 1, MaxY: 1}, workers)
			// v = (initial velocity + impulse + acceleration*dt)*damping;
			// position includes correction and the newly computed velocity.
			for i := range n {
				for k, got := range [...]float32{p.x[i], p.y[i], p.vx[i], p.vy[i]} {
					want := [...]float32{.57, .44, .2, -.2}[k]
					if math.IsNaN(float64(got)) || math.Abs(float64(got-want)) > 1e-6 {
						t.Fatalf("particle %d component %d = %g, want %g", i, k, got, want)
					}
				}
			}
		})
	}
}
