package softbody

import (
	"math"
	"sync/atomic"
)

const sqrt3 = 1.7320508075688772935

type cell struct {
	start, end int
}

type axialOffset struct {
	q, r int
}

type hexGrid struct {
	spacing float32
	rowH    float32
	invS    float32
	invH    float32

	qMin, qMax int
	rMin, rMax int
	qCount     int
	rCount     int

	cells   []cell
	counts  []int
	cursor  []int
	active  []int
	stencil []axialOffset
}

func (g *hexGrid) index(q, r int) int {
	if q < g.qMin || q > g.qMax || r < g.rMin || r > g.rMax {
		return -1
	}
	return (r-g.rMin)*g.qCount + (q - g.qMin)
}

func (g *hexGrid) coord(index int) (q, r int) {
	return index%g.qCount + g.qMin, index/g.qCount + g.rMin
}

func (s *State) ensureGridGeometry() {
	if !s.geometryDirty || s.maxOuter <= 0 {
		return
	}

	spacing := s.cfg.GridSpacing
	if spacing <= 0 {
		spacing = 2 * s.maxOuter
	}
	if spacing <= 0 {
		spacing = 0.01
	}
	rowH := spacing * float32(sqrt3/2)

	// Cover the world, including negative coordinates, with padded axial cells.
	rMin := int(math.Floor(float64(s.bounds.MinY)/float64(rowH))) - 2
	rMax := int(math.Ceil(float64(s.bounds.MaxY)/float64(rowH))) + 2
	qMin := int(math.Floor(float64(s.bounds.MinX)/float64(spacing)-float64(rMax)/2)) - 2
	qMax := int(math.Ceil(float64(s.bounds.MaxX)/float64(spacing)-float64(rMin)/2)) + 2

	g := &s.grid
	g.spacing = spacing
	g.rowH = rowH
	g.invS = 1 / spacing
	g.invH = 1 / rowH
	g.qMin, g.qMax = qMin, qMax
	g.rMin, g.rMax = rMin, rMax
	g.qCount = qMax - qMin + 1
	g.rCount = rMax - rMin + 1
	nCells := g.qCount * g.rCount
	g.cells = make([]cell, nCells)
	g.counts = make([]int, nCells)
	g.cursor = make([]int, nCells)

	// Conservative stencil: a Voronoi hex has circumradius spacing/sqrt(3).
	// If cell centers are farther apart than interactionCutoff + 2*circumradius,
	// no pair in those two cells can possibly collide or attract.
	interactionCutoff := 2 * s.maxOuter
	if s.cfg.AttractionRange > 0 && s.cfg.AttractionStrength > 0 {
		interactionCutoff += s.cfg.AttractionRange
	}
	circumradius := spacing / float32(sqrt3)
	centerLimit := interactionCutoff + 2*circumradius
	rings := int(math.Ceil(float64(centerLimit/spacing))) + 1
	g.stencil = g.stencil[:0]
	for dr := -rings; dr <= rings; dr++ {
		for dq := -rings; dq <= rings; dq++ {
			d2 := float32(dq*dq + dq*dr + dr*dr)
			if d2*spacing*spacing <= centerLimit*centerLimit {
				g.stencil = append(g.stencil, axialOffset{q: dq, r: dr})
			}
		}
	}

	s.geometryDirty = false
}

func (s *State) rebuildGrid() {
	s.ensureGridGeometry()
	n := s.p.len()
	if n == 0 {
		return
	}

	s.qf = resize(s.qf, n)
	s.rf = resize(s.rf, n)
	fractionalHexKernel(s.p.x, s.p.y, s.qf, s.rf, s.grid.invS, s.grid.invH)

	s.cellID = resize(s.cellID, n)
	parallelFor(n, s.cfg.Workers, 512, func(start, end int) {
		for i := start; i < end; i++ {
			q, r := roundAxial(s.qf[i], s.rf[i], s.p.id[i])
			idx := s.grid.index(q, r)
			if idx < 0 {
				// Should be impossible for a hard-core-clamped particle, but keep
				// pathological inputs from corrupting the counting sort.
				q = min(max(q, s.grid.qMin), s.grid.qMax)
				r = min(max(r, s.grid.rMin), s.grid.rMax)
				idx = s.grid.index(q, r)
			}
			s.cellID[i] = idx
		}
	})

	clear(s.grid.counts)
	for _, id := range s.cellID {
		s.grid.counts[id]++
	}

	total := 0
	s.grid.active = s.grid.active[:0]
	for i, count := range s.grid.counts {
		s.grid.cells[i] = cell{start: total, end: total + count}
		s.grid.cursor[i] = total
		if count != 0 {
			s.grid.active = append(s.grid.active, i)
		}
		total += count
	}

	s.scratch.resize(n)
	for src, cid := range s.cellID {
		dst := s.grid.cursor[cid]
		s.grid.cursor[cid]++
		copyParticle(&s.scratch, dst, &s.p, src)
	}
	s.p, s.scratch = s.scratch, s.p
}

func copyParticle(dst *particleData, di int, src *particleData, si int) {
	dst.id[di] = src.id[si]
	dst.group[di] = src.group[si]
	dst.x[di], dst.y[di] = src.x[si], src.y[si]
	dst.vx[di], dst.vy[di] = src.vx[si], src.vy[si]
	dst.inner[di], dst.outer[di] = src.inner[si], src.outer[si]
	dst.invMass[di] = src.invMass[si]
}

// q,r are axial coordinates with basis vectors (S,0) and (S/2,sqrt(3)S/2).
func roundAxial(qf, rf float32, id uint64) (q, r int) {
	sf := -qf - rf
	qi := int(math.Round(float64(qf)))
	ri := int(math.Round(float64(rf)))
	si := int(math.Round(float64(sf)))

	dq := float32(math.Abs(float64(float32(qi) - qf)))
	dr := float32(math.Abs(float64(float32(ri) - rf)))
	ds := float32(math.Abs(float64(float32(si) - sf)))

	// The ID tie-breaker keeps exact boundary cases deterministic.
	if dq > dr && dq > ds || (dq == dr && dq == ds && id&1 == 0) {
		qi = -ri - si
	} else if dr > ds {
		ri = -qi - si
	}
	return qi, ri
}

func parallelFor(n, workers, grain int, fn func(start, end int)) {
	if n <= 0 {
		return
	}
	if workers <= 1 || n <= grain {
		fn(0, n)
		return
	}

	var next atomic.Int64
	done := make(chan struct{}, workers)
	for range workers {
		go func() {
			for {
				start := int(next.Add(int64(grain))) - grain
				if start >= n {
					break
				}
				end := min(start+grain, n)
				fn(start, end)
			}
			done <- struct{}{}
		}()
	}
	for range workers {
		<-done
	}
}
