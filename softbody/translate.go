package softbody

import "fmt"

// TranslateCircles adds (dx, dy) to unanchored circles, including held circles and
// their drag target. IDs, velocities, masses, radii, and bridges are preserved
// unless BoundaryRemove deletes an outside center and its incident bridges. Bounds,
// polygons, and queued impulse sources stay fixed. The movement is a teleport,
// not a sweep. Translations into polygon cores or outside wall confinement are
// rejected without mutation. Call on the simulation goroutine.
func (s *State) TranslateCircles(dx, dy float32) error {
	if !finitePointer(dx, dy) {
		return fmt.Errorf("translation must be finite")
	}
	if dx == 0 && dy == 0 {
		return nil
	}
	for i := range s.p.x {
		if s.isAnchored(i) {
			continue
		}
		x, y := s.p.x[i]+dx, s.p.y[i]+dy
		if !finitePointer(x, y) {
			return fmt.Errorf("translated circle coordinates must be finite")
		}
		r := s.p.inner[i]
		if s.cfg.BoundaryMode == BoundaryWalls {
			if x < s.bounds.MinX+r || x > s.bounds.MaxX-r || y < s.bounds.MinY+r || y > s.bounds.MaxY-r {
				return fmt.Errorf("translated circle core must fit the world walls")
			}
		} else if !s.bounds.contains(x, y) {
			continue // This circle will be removed, not projected into an obstacle.
		}
		for k := range s.polygons {
			p := &s.polygons[k]
			if !p.near(float64(x), float64(y), float64(x), float64(y), float64(r)) {
				continue
			}
			d, _, _ := p.contact(float64(x), float64(y))
			if d < float64(r) {
				return fmt.Errorf("translated circle core overlaps a polygon")
			}
		}
	}
	if len(s.dragged) > 0 && !finitePointer(s.dragX+dx, s.dragY+dy) {
		return fmt.Errorf("translated drag target must be finite")
	}
	for i := range s.p.x {
		if s.isAnchored(i) {
			continue
		}
		s.p.x[i] += dx
		s.p.y[i] += dy
	}
	if len(s.dragged) > 0 {
		s.dragX += dx
		s.dragY += dy
	}
	s.removeOutsideCircles()
	return nil
}

// TranslatePolygons adds (dx, dy) to all polygon vertices, preserving polygon
// IDs. Invalid or rounded-degenerate geometry is rejected without mutation.
// Existing unanchored circle penetrations are recovered, as with AddPolygon;
// this can move circles and reflect their velocities. BoundaryRemove deletes
// wholly outside polygon boxes and any circles pushed outside. Bounds, drag
// targets, and impulse sources stay fixed. This repositions static geometry;
// it does not simulate moving-platform velocity or sweep the polygons' motion.
// Call on the simulation goroutine.
func (s *State) TranslatePolygons(dx, dy float32) error {
	if !finitePointer(dx, dy) {
		return fmt.Errorf("translation must be finite")
	}
	if dx == 0 && dy == 0 || len(s.polygons) == 0 {
		return nil
	}
	polygons, err := s.translatedPolygons(dx, dy)
	if err != nil {
		return err
	}
	s.polygons = polygons
	s.geometryDirty = true
	s.removeOutsidePolygons()
	for i := range s.p.x {
		s.projectPolygonCore(i)
	}
	s.removeOutsideCircles()
	return nil
}

// ShiftOrigin subtracts (dx, dy) from every world-coordinate quantity: circles,
// polygons, bounds, the active drag target, and queued impulse sources. IDs,
// velocities, masses, radii, and bridge limits stay unchanged. It never confines
// or removes entities. Nonfinite results or geometry collapsed by float32
// rounding are rejected atomically. Call on the simulation goroutine; synchronize
// input producers with the coordinate-frame change so new impulses and pointer
// positions use the new origin. Queue mutation itself remains mutex-protected.
func (s *State) ShiftOrigin(dx, dy float32) error {
	if !finitePointer(dx, dy) {
		return fmt.Errorf("origin shift must be finite")
	}
	if dx == 0 && dy == 0 {
		return nil
	}
	s.impulseMu.Lock()
	defer s.impulseMu.Unlock()
	b := Bounds{s.bounds.MinX - dx, s.bounds.MinY - dy, s.bounds.MaxX - dx, s.bounds.MaxY - dy}
	if err := validateBounds(b, s.cfg.BoundaryMode, s.maxOuter); err != nil {
		return err
	}
	for i := range s.p.x {
		if !finitePointer(s.p.x[i]-dx, s.p.y[i]-dy) {
			return fmt.Errorf("shifted circle coordinates must be finite")
		}
	}
	if len(s.dragged) > 0 && !finitePointer(s.dragX-dx, s.dragY-dy) {
		return fmt.Errorf("shifted drag target must be finite")
	}
	for _, impulse := range s.impulses {
		if !finitePointer(impulse.X-dx, impulse.Y-dy) {
			return fmt.Errorf("shifted impulse source must be finite")
		}
	}
	polygons, err := s.translatedPolygons(-dx, -dy)
	if err != nil {
		return err
	}
	// Commit only after validating every result. No intermediate projection or
	// boundary cleanup may observe entities and bounds in different frames.
	s.bounds = b
	s.polygons = polygons
	for i := range s.p.x {
		s.p.x[i] -= dx
		s.p.y[i] -= dy
	}
	if len(s.dragged) > 0 {
		s.dragX -= dx
		s.dragY -= dy
	}
	for i := range s.impulses {
		s.impulses[i].X -= dx
		s.impulses[i].Y -= dy
	}
	s.geometryDirty = true
	return nil
}

func (s *State) translatedPolygons(dx, dy float32) ([]polygon, error) {
	polygons := make([]polygon, len(s.polygons))
	for k, p := range s.polygons {
		points := make([]Point, len(p.Points))
		for i, a := range p.Points {
			points[i] = Point{a.X + dx, a.Y + dy}
		}
		for i, a := range points {
			if a == points[(i+1)%len(points)] {
				return nil, fmt.Errorf("translated polygon %d has an edge collapsed by rounding", p.ID)
			}
		}
		translated, err := preparePolygon(points)
		if err != nil {
			return nil, fmt.Errorf("translated polygon %d: %w", p.ID, err)
		}
		translated.ID = p.ID
		polygons[k] = translated
	}
	return polygons, nil
}
