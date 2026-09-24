package softbody

import (
	"fmt"
	"math"
	"slices"
)

// Point is a position in world coordinates.
type Point struct{ X, Y float32 }

// PolygonSnapshot describes an immovable solid polygon. Points are in boundary
// order; the last point connects to the first.
type PolygonSnapshot struct {
	ID     uint64
	Points []Point
}

type polygon struct {
	PolygonSnapshot
	bounds  Bounds
	winding float64
}

// AddPolygon adds an immovable solid obstacle and returns its stable ID.
// Points are copied and may describe a convex or concave simple polygon in
// either winding order. A repeated closing point is optional. Holes, crossing
// edges, duplicate vertices, and zero-area polygons are rejected. Outer shells
// compress against the boundary; inner cores cannot cross it. Existing circles
// inside an obstacle are projected toward its nearest boundary.
// Like Step, call this on the simulation goroutine. Leave enough free space
// between obstacles and world bounds for the circles' cores to fit.
func (s *State) AddPolygon(points []Point) (uint64, error) {
	if len(points) > 1 && points[0] == points[len(points)-1] {
		points = points[:len(points)-1]
	}
	if len(points) < 3 {
		return 0, fmt.Errorf("polygon needs at least three vertices")
	}
	p := polygon{PolygonSnapshot: PolygonSnapshot{Points: slices.Clone(points)}}
	p.bounds = Bounds{points[0].X, points[0].Y, points[0].X, points[0].Y}
	area := float64(0)
	for i, a := range points {
		if !finitePointer(a.X, a.Y) {
			return 0, fmt.Errorf("polygon coordinates must be finite")
		}
		b := points[(i+1)%len(points)]
		if a == b {
			return 0, fmt.Errorf("polygon edges must have positive length")
		}
		// Translate to the first vertex to avoid cancellation at large offsets.
		area += (float64(a.X)-float64(points[0].X))*(float64(b.Y)-float64(points[0].Y)) - (float64(b.X)-float64(points[0].X))*(float64(a.Y)-float64(points[0].Y))
		p.bounds.MinX = min(p.bounds.MinX, a.X)
		p.bounds.MaxX = max(p.bounds.MaxX, a.X)
		p.bounds.MinY = min(p.bounds.MinY, a.Y)
		p.bounds.MaxY = max(p.bounds.MaxY, a.Y)
		c := points[(i+2)%len(points)]
		if orient(a, b, c) == 0 && (float64(a.X)-float64(b.X))*(float64(c.X)-float64(b.X))+(float64(a.Y)-float64(b.Y))*(float64(c.Y)-float64(b.Y)) > 0 {
			return 0, fmt.Errorf("polygon edges overlap")
		}
		for j := i + 1; j < len(points); j++ {
			if j == i+1 || i == 0 && j == len(points)-1 {
				continue
			}
			if segmentsIntersect(a, b, points[j], points[(j+1)%len(points)]) {
				return 0, fmt.Errorf("polygon must not self-intersect")
			}
		}
	}
	if area == 0 {
		return 0, fmt.Errorf("polygon must have positive area")
	}
	p.winding = 1
	if area < 0 {
		p.winding = -1
	}
	s.nextPolygonID++
	p.ID = s.nextPolygonID
	s.polygons = append(s.polygons, p)
	s.geometryDirty = true
	for i := range s.p.x {
		s.projectPolygonCore(i)
	}
	return p.ID, nil
}

// RemovePolygon removes an obstacle by ID. Call on the simulation goroutine.
func (s *State) RemovePolygon(id uint64) bool {
	for i, p := range s.polygons {
		if p.ID == id {
			s.polygons = slices.Delete(s.polygons, i, i+1)
			s.geometryDirty = true
			return true
		}
	}
	return false
}

// PolygonSnapshot copies obstacles and their vertices in insertion order.
// It must not run concurrently with mutations.
func (s *State) PolygonSnapshot(dst []PolygonSnapshot) []PolygonSnapshot {
	dst = dst[:0]
	for _, p := range s.polygons {
		dst = append(dst, PolygonSnapshot{p.ID, slices.Clone(p.Points)})
	}
	return dst
}

func orient(a, b, c Point) float64 {
	return (float64(b.X)-float64(a.X))*(float64(c.Y)-float64(a.Y)) - (float64(b.Y)-float64(a.Y))*(float64(c.X)-float64(a.X))
}
func segmentsIntersect(a, b, c, d Point) bool {
	if max(a.X, b.X) < min(c.X, d.X) || max(c.X, d.X) < min(a.X, b.X) || max(a.Y, b.Y) < min(c.Y, d.Y) || max(c.Y, d.Y) < min(a.Y, b.Y) {
		return false
	}
	abC, abD, cdA, cdB := orient(a, b, c), orient(a, b, d), orient(c, d, a), orient(c, d, b)
	return (abC <= 0 && abD >= 0 || abC >= 0 && abD <= 0) && (cdA <= 0 && cdB >= 0 || cdA >= 0 && cdB <= 0)
}

// contact returns signed distance and a normal pointing out of the solid.
func (p *polygon) contact(x, y float64) (dist, nx, ny float64) {
	best := math.Inf(1)
	inside := false
	for i, a := range p.Points {
		b := p.Points[(i+1)%len(p.Points)]
		ax, ay, bx, by := float64(a.X), float64(a.Y), float64(b.X), float64(b.Y)
		ex, ey := bx-ax, by-ay
		t := min(max(((x-ax)*ex+(y-ay)*ey)/(ex*ex+ey*ey), 0), 1)
		dx, dy := x-ax-t*ex, y-ay-t*ey
		d := math.Hypot(dx, dy)
		if d < best {
			best = d
			if d > 0 {
				nx, ny = dx/d, dy/d
			} else {
				l := math.Hypot(ex, ey)
				nx, ny = p.winding*ey/l, -p.winding*ex/l
			}
		}
		if (ay > y) != (by > y) && x < ax+(y-ay)*ex/ey {
			inside = !inside
		}
	}
	if inside && best > 0 {
		return -best, -nx, -ny
	}
	return best, nx, ny
}

func (p *polygon) near(minX, minY, maxX, maxY, r float64) bool {
	return maxX+r >= float64(p.bounds.MinX) && minX-r <= float64(p.bounds.MaxX) && maxY+r >= float64(p.bounds.MinY) && minY-r <= float64(p.bounds.MaxY)
}

func (s *State) polygonShellResponse(i, cid int) (ax, ay float32) {
	if len(s.polygons) == 0 {
		return
	}
	for _, k := range s.grid.polygons[cid] {
		p := &s.polygons[k]
		d, nx, ny := p.contact(float64(s.p.x[i]), float64(s.p.y[i]))
		penetration := s.p.outer[i] - float32(d)
		if penetration <= 0 {
			continue
		}
		compression := min(penetration/max(s.p.outer[i]-s.p.inner[i], collisionEpsilon), 1)
		vn := s.p.vx[i]*float32(nx) + s.p.vy[i]*float32(ny)
		force := max(s.cfg.ShellStiffness*penetration*compression-s.cfg.ShellDamping*vn, 0) * s.p.invMass[i]
		ax += float32(nx) * force
		ay += float32(ny) * force
	}
	return
}

func (s *State) polygonBounce(i int, nx, ny float64) {
	vn := float64(s.p.vx[i])*nx + float64(s.p.vy[i])*ny
	if vn < 0 {
		impulse := -(1 + float64(s.cfg.Restitution)) * vn
		s.p.vx[i] += float32(nx * impulse)
		s.p.vy[i] += float32(ny * impulse)
	}
}

// Recover initial penetrations, including circles created inside a solid.
func (s *State) projectPolygonCore(i int) {
	r := float64(s.p.inner[i])
	for pass := 0; pass < 32; pass++ {
		moved := false
		for k := range s.polygons {
			p := &s.polygons[k]
			x, y := float64(s.p.x[i]), float64(s.p.y[i])
			if !p.near(x, y, x, y, r) {
				continue
			}
			d, nx, ny := p.contact(x, y)
			if d >= r {
				continue
			}
			padding := polygonPadding(x, y, r)
			tx, ty := x+nx*(r-d+padding), y+ny*(r-d+padding)
			if !s.coreWithinBounds(tx, ty, r) {
				// A polygon may extend beyond the world. Choose an exit into
				// free space instead of repeatedly pushing through a world wall.
				if fx, fy, ok := s.freePolygonPosition(x, y, r); ok {
					tx, ty = fx, fy
				}
			}
			s.p.x[i], s.p.y[i] = float32(tx), float32(ty)
			s.polygonBounce(i, nx, ny)
			moved = true
		}
		if !moved {
			break
		}
	}
}

// A few float32 ulps keep rounded contact positions on the free side.
func polygonPadding(x, y, r float64) float64 { return 2e-7 * max(math.Abs(x), math.Abs(y), r, 1e-3) }

// sweep tests the radius-expanded boundary: edge strips and circular end caps.
// It works unchanged for concave polygons; no triangulation seams are present.
func (p *polygon) sweep(x, y, dx, dy, r float64) (hit, nx, ny float64) {
	hit = 1
	accept := func(t, nxx, nyy float64) {
		if t >= -1e-9 && t <= hit && dx*nxx+dy*nyy < -1e-12*math.Hypot(dx, dy) {
			hit = max(t, 0)
			nx, ny = nxx, nyy
		}
	}
	for i, a := range p.Points {
		b := p.Points[(i+1)%len(p.Points)]
		ax, ay := float64(a.X), float64(a.Y)
		ex, ey := float64(b.X)-ax, float64(b.Y)-ay
		length := math.Hypot(ex, ey)
		ux, uy := ex/length, ey/length
		for _, sign := range [...]float64{-1, 1} {
			nxx, nyy := sign*uy, -sign*ux
			vn := dx*nxx + dy*nyy
			if vn >= 0 {
				continue
			}
			t := (r - ((x-ax)*nxx + (y-ay)*nyy)) / vn
			along := (x+t*dx-ax)*ux + (y+t*dy-ay)*uy
			if along >= 0 && along <= length {
				accept(t, nxx, nyy)
			}
		}
		ox, oy := x-ax, y-ay
		aa := dx*dx + dy*dy
		bb := ox*dx + oy*dy
		cc := ox*ox + oy*oy - r*r
		disc := bb*bb - aa*cc
		if aa > 0 && disc >= 0 {
			t := (-bb - math.Sqrt(disc)) / aa
			hx, hy := ox+t*dx, oy+t*dy
			l := math.Hypot(hx, hy)
			if l > 0 {
				accept(t, hx/l, hy/l)
			}
		}
	}
	return
}

// movePolygonCore sweeps from the current position and slides along contacts.
// After a bounded number of contacts any remaining movement is discarded.
func (s *State) movePolygonCore(i int, tx, ty float32) {
	if len(s.polygons) == 0 {
		s.p.x[i], s.p.y[i] = tx, ty
		return
	}
	s.projectPolygonCore(i)
	x, y := float64(s.p.x[i]), float64(s.p.y[i])
	dx, dy := float64(tx)-x, float64(ty)-y
	r := float64(s.p.inner[i])
	for pass := 0; pass < 8 && (dx != 0 || dy != 0); pass++ {
		hit, nx, ny := float64(1), float64(0), float64(0)
		// Sliding on a sloped polygon can reach a world wall even when the
		// original target was in bounds. Sweep the world walls as well.
		for _, wall := range [][4]float64{
			{x - float64(s.bounds.MinX) - r, dx, 1, 0},
			{float64(s.bounds.MaxX) - r - x, -dx, -1, 0},
			{y - float64(s.bounds.MinY) - r, dy, 0, 1},
			{float64(s.bounds.MaxY) - r - y, -dy, 0, -1},
		} {
			if wall[1] < 0 {
				t := -wall[0] / wall[1]
				if t >= 0 && t < hit {
					hit, nx, ny = t, wall[2], wall[3]
				}
			}
		}
		for k := range s.polygons {
			p := &s.polygons[k]
			if !p.near(min(x, x+dx), min(y, y+dy), max(x, x+dx), max(y, y+dy), r) {
				continue
			}
			t, nxx, nyy := p.sweep(x, y, dx, dy, r)
			if t <= hit && (nxx != 0 || nyy != 0) {
				hit, nx, ny = t, nxx, nyy
			}
		}
		x += dx * hit
		y += dy * hit
		if nx == 0 && ny == 0 {
			break
		}
		padding := polygonPadding(x, y, r)
		x += nx * padding
		y += ny * padding
		s.polygonBounce(i, nx, ny)
		dx *= 1 - hit
		dy *= 1 - hit
		vn := min(dx*nx+dy*ny, 0)
		dx -= vn * nx
		dy -= vn * ny
	}
	s.p.x[i], s.p.y[i] = float32(x), float32(y)
	s.projectPolygonCore(i)
}

func (s *State) coreWithinBounds(x, y, r float64) bool {
	return x >= float64(s.bounds.MinX)+r && x <= float64(s.bounds.MaxX)-r && y >= float64(s.bounds.MinY)+r && y <= float64(s.bounds.MaxY)-r
}

// Used only when recovering an initial overlap whose nearest exit is outside
// the world. Search boundary projections for the closest feasible alternative.
func (s *State) freePolygonPosition(x, y, r float64) (fx, fy float64, ok bool) {
	best := math.Inf(1)
	for _, p := range s.polygons {
		for i, a := range p.Points {
			b := p.Points[(i+1)%len(p.Points)]
			ax, ay := float64(a.X), float64(a.Y)
			ex, ey := float64(b.X)-ax, float64(b.Y)-ay
			length := math.Hypot(ex, ey)
			t := min(max(((x-ax)*ex+(y-ay)*ey)/(length*length), 0), 1)
			offset := r + polygonPadding(ax+t*ex, ay+t*ey, r)
			tx, ty := ax+t*ex+p.winding*ey/length*offset, ay+t*ey-p.winding*ex/length*offset
			if !s.coreWithinBounds(tx, ty, r) {
				continue
			}
			distance := math.Hypot(tx-x, ty-y)
			if distance >= best {
				continue
			}
			clear := true
			for k := range s.polygons {
				d, _, _ := s.polygons[k].contact(tx, ty)
				if d < r {
					clear = false
					break
				}
			}
			if clear {
				best = distance
				fx, fy, ok = tx, ty, true
			}
		}
	}
	return
}
