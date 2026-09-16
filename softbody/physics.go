package softbody

import (
	"math"
	"sync/atomic"
)

const collisionEpsilon = float32(1e-7)

func (s *State) solveCells(clicks []click) {
	active := s.grid.active
	if len(active) == 0 {
		return
	}

	workers := min(s.cfg.Workers, len(active))
	if workers <= 1 {
		for _, cid := range active {
			s.solveCell(cid, clicks)
		}
		return
	}

	var next atomic.Int64
	done := make(chan struct{}, workers)
	for range workers {
		go func() {
			for {
				k := int(next.Add(1)) - 1
				if k >= len(active) {
					break
				}
				s.solveCell(active[k], clicks)
			}
			done <- struct{}{}
		}()
	}
	for range workers {
		<-done
	}
}

func (s *State) solveCell(cid int, clicks []click) {
	own := s.grid.cells[cid]
	cq, cr := s.grid.coord(cid)

	for i := own.start; i < own.end; i++ {
		var ax, ay, dvx, dvy, cx, cy float32

		for _, off := range s.grid.stencil {
			nid := s.grid.index(cq+off.q, cr+off.r)
			if nid < 0 {
				continue
			}
			nc := s.grid.cells[nid]
			if nc.start == nc.end {
				continue
			}

			// Avoid self-interaction without putting a per-lane index comparison
			// in the SIMD kernel.
			if nid == cid {
				if nc.start < i {
					a, b, c, d, e, f := pairSpanKernel(&s.p, i, nc.start, i, s.cfg)
					ax += a
					ay += b
					dvx += c
					dvy += d
					cx += e
					cy += f
				}
				if i+1 < nc.end {
					a, b, c, d, e, f := pairSpanKernel(&s.p, i, i+1, nc.end, s.cfg)
					ax += a
					ay += b
					dvx += c
					dvy += d
					cx += e
					cy += f
				}
			} else {
				a, b, c, d, e, f := pairSpanKernel(&s.p, i, nc.start, nc.end, s.cfg)
				ax += a
				ay += b
				dvx += c
				dvy += d
				cx += e
				cy += f
			}
		}

		wa, wb, wc, wd, we, wf := s.wallResponse(i)
		ax += wa
		ay += wb
		dvx += wc
		dvy += wd
		cx += we
		cy += wf

		ca, cb := s.clickResponse(i, clicks)
		dvx += ca
		dvy += cb

		s.ax[i], s.ay[i] = ax, ay
		s.dvx[i], s.dvy[i] = dvx, dvy
		s.corrX[i], s.corrY[i] = cx, cy
	}
}

func (s *State) wallResponse(i int) (ax, ay, dvx, dvy, cx, cy float32) {
	x, y := s.p.x[i], s.p.y[i]
	vx, vy := s.p.vx[i], s.p.vy[i]
	inner, outer := s.p.inner[i], s.p.outer[i]
	invMass := s.p.invMass[i]

	apply := func(dist, vn, nx, ny float32) {
		if dist >= outer {
			return
		}
		thickness := max(outer-inner, collisionEpsilon)
		penetration := outer - dist
		compression := min(max(penetration/thickness, 0), 1)
		force := s.cfg.ShellStiffness*penetration*compression - s.cfg.ShellDamping*vn
		if force > 0 {
			ax += nx * force * invMass
			ay += ny * force * invMass
		}
		if dist < inner {
			corePen := inner - dist
			cx += nx * corePen * s.cfg.CoreCorrection
			cy += ny * corePen * s.cfg.CoreCorrection
			if vn < 0 {
				bounce := -(1 + s.cfg.Restitution) * vn
				dvx += nx * bounce
				dvy += ny * bounce
			}
		}
	}

	apply(x-s.bounds.MinX, vx, 1, 0)
	apply(s.bounds.MaxX-x, -vx, -1, 0)
	apply(y-s.bounds.MinY, vy, 0, 1)
	apply(s.bounds.MaxY-y, -vy, 0, -1)
	return
}

func (s *State) clickResponse(i int, clicks []click) (dvx, dvy float32) {
	if len(clicks) == 0 || s.cfg.ClickRadius <= 0 || s.cfg.ClickImpulse == 0 {
		return 0, 0
	}
	for _, c := range clicks {
		if s.p.group[i] != c.group {
			continue
		}
		dx := c.x - s.p.x[i]
		dy := c.y - s.p.y[i]
		d2 := dx*dx + dy*dy
		r2 := s.cfg.ClickRadius * s.cfg.ClickRadius
		if d2 <= collisionEpsilon*collisionEpsilon || d2 >= r2 {
			continue
		}
		d := float32(math.Sqrt(float64(d2)))
		falloff := 1 - d/s.cfg.ClickRadius
		falloff *= falloff
		impulse := s.cfg.ClickImpulse * falloff * s.p.invMass[i]
		dvx += dx / d * impulse
		dvy += dy / d * impulse
	}
	return
}

func (s *State) integrate(dt float32) {
	if dt <= 0 {
		return
	}
	damping := float32(1)
	if s.cfg.LinearDamping > 0 {
		damping = 1 / (1 + s.cfg.LinearDamping*dt)
	}
	integrateKernel(&s.p, s.ax, s.ay, s.dvx, s.dvy, s.corrX, s.corrY, dt, damping, s.cfg.Restitution, s.bounds, s.cfg.Workers)
}

func pairScalar(p *particleData, i, j int, cfg Config) (ax, ay, dvx, dvy, cx, cy float32) {
	dx := p.x[i] - p.x[j]
	dy := p.y[i] - p.y[j]
	d2 := dx*dx + dy*dy
	outer := p.outer[i] + p.outer[j]
	attract := cfg.AttractionRange > 0 && cfg.AttractionStrength > 0 && p.group[i] == p.group[j]
	cutoff := outer
	if attract {
		cutoff += cfg.AttractionRange
	}
	if d2 >= cutoff*cutoff {
		return
	}

	var d float32
	if d2 <= collisionEpsilon*collisionEpsilon {
		// Stable anti-symmetry for coincident centers.
		if p.id[i] < p.id[j] {
			dx = -collisionEpsilon
		} else {
			dx = collisionEpsilon
		}
		dy = 0
		d = collisionEpsilon
	} else {
		d = float32(math.Sqrt(float64(d2)))
	}
	nx, ny := dx/d, dy/d

	if attract && d2 > collisionEpsilon*collisionEpsilon {
		falloff := max(1-max(d-outer, 0)/cfg.AttractionRange, 0)
		accel := cfg.AttractionStrength * falloff * falloff * p.invMass[i]
		ax -= nx * accel
		ay -= ny * accel
	}
	if d >= outer {
		return
	}

	inner := p.inner[i] + p.inner[j]
	thickness := max(outer-inner, collisionEpsilon)
	penetration := outer - d
	compression := min(max(penetration/thickness, 0), 1)
	rel := (p.vx[i]-p.vx[j])*nx + (p.vy[i]-p.vy[j])*ny

	force := cfg.ShellStiffness*penetration*compression - cfg.ShellDamping*rel
	if force > 0 {
		accel := force * p.invMass[i]
		ax += nx * accel
		ay += ny * accel
	}

	if d < inner {
		denom := p.invMass[i] + p.invMass[j]
		if denom > 0 {
			share := p.invMass[i] / denom
			corr := (inner - d) * cfg.CoreCorrection * share
			cx += nx * corr
			cy += ny * corr
			if rel < 0 {
				impulse := -(1 + cfg.Restitution) * rel / denom
				dv := impulse * p.invMass[i]
				dvx += nx * dv
				dvy += ny * dv
			}
		}
	}
	return
}
