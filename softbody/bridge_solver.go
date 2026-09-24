package softbody

// solveBridgeConstraints runs after prediction (the ordinary integration step).
// Corrections are applied immediately, so later links see earlier links' work.
// Alternating sweep direction reduces bias toward either end of a chain.
func (s *State) solveBridgeConstraints(dt float32) {
	var constrain, damp bool
	for _, b := range s.bridges {
		constrain = constrain || b.ConstrainDistance
		damp = damp || b.Damping > 0
	}
	if !constrain && !damp {
		return
	}
	// solveBridges indexed the current particle order before integration.
	// Check the predicted separation before a constraint can pull it back.
	s.pruneBrokenBridges()
	if len(s.bridges) == 0 {
		return
	}

	if constrain {
		for pass := range s.cfg.BridgeIterations {
			for k := range s.bridges {
				if pass%2 != 0 {
					k = len(s.bridges) - 1 - k
				}
				b := s.bridges[k]
				if !b.ConstrainDistance {
					continue
				}
				i, j := s.bridgeIndex[b.A], s.bridgeIndex[b.B]
				d, nx, ny := s.bridgeDirection(i, j)
				target := min(max(d, float64(b.MinDistance)), float64(b.MaxDistance))
				w := float64(s.p.invMass[i]) + float64(s.p.invMass[j])
				if w == 0 {
					continue // Both endpoints are held; the pointer takes precedence.
				}
				correction := (d - target) / w
				dxi := float32(nx * correction * float64(s.p.invMass[i]))
				dyi := float32(ny * correction * float64(s.p.invMass[i]))
				dxj := float32(nx * correction * float64(s.p.invMass[j]))
				dyj := float32(ny * correction * float64(s.p.invMass[j]))
				s.p.x[i] += dxi
				s.p.y[i] += dyi
				s.p.x[j] -= dxj
				s.p.y[j] -= dyj
				// Feed the constraint displacement back into velocity. Without
				// this the endpoints would keep trying to stretch every step.
				s.p.vx[i] += dxi / dt
				s.p.vy[i] += dyi / dt
				s.p.vx[j] -= dxj / dt
				s.p.vy[j] -= dyj / dt
			}
			// A bridge can pull its endpoints into a wall or another circle.
			// Rebuild the broad phase after moving them, then resolve cores.
			// Shell forces are still evaluated only once per substep.
			s.confineBridgeParticles()
			s.rebuildGrid()
			s.indexBridges()
			s.projectBridgeContacts()
			s.confineBridgeParticles()
		}
	}
	if damp {
		s.dampBridges(dt)
	}
}

func (s *State) confineBridgeParticles() {
	for i, radius := range s.p.inner {
		s.p.x[i], s.p.vx[i] = confine(s.p.x[i], s.p.vx[i], s.bounds.MinX+radius, s.bounds.MaxX-radius, s.cfg.Restitution)
		s.p.y[i], s.p.vy[i] = confine(s.p.y[i], s.p.vy[i], s.bounds.MinY+radius, s.bounds.MaxY-radius, s.cfg.Restitution)
	}
}

// projectBridgeContacts interleaves hard-core collision corrections with bridge
// constraints. As in the ordinary contact solver, penetration correction does
// not add velocity; an approaching pair receives a normal collision impulse.
// The grid is rebuilt each pass, catching contacts created by prior corrections.
func (s *State) projectBridgeContacts() {
	for _, cid := range s.grid.active {
		own := s.grid.cells[cid]
		q, r := s.grid.coord(cid)
		for _, off := range s.grid.stencil {
			nid := s.grid.index(q+off.q, r+off.r)
			if nid < cid {
				continue
			}
			other := s.grid.cells[nid]
			for i := own.start; i < own.end; i++ {
				start := other.start
				if nid == cid {
					start = i + 1
				}
				for j := start; j < other.end; j++ {
					inner := float64(s.p.inner[i]) + float64(s.p.inner[j])
					dx := float64(s.p.x[j]) - float64(s.p.x[i])
					dy := float64(s.p.y[j]) - float64(s.p.y[i])
					if dx*dx+dy*dy >= inner*inner {
						continue
					}
					d, nx, ny := s.bridgeDirection(i, j)
					wi, wj := float64(s.p.invMass[i]), float64(s.p.invMass[j])
					if wi+wj == 0 {
						continue
					}
					correction := (inner - d) * float64(s.cfg.CoreCorrection) / (wi + wj)
					s.p.x[i] -= float32(nx * correction * wi)
					s.p.y[i] -= float32(ny * correction * wi)
					s.p.x[j] += float32(nx * correction * wj)
					s.p.y[j] += float32(ny * correction * wj)
					rel := (float64(s.p.vx[j])-float64(s.p.vx[i]))*nx + (float64(s.p.vy[j])-float64(s.p.vy[i]))*ny
					if rel < 0 {
						impulse := -(1 + float64(s.cfg.Restitution)) * rel / (wi + wj)
						s.p.vx[i] -= float32(nx * impulse * wi)
						s.p.vy[i] -= float32(ny * impulse * wi)
						s.p.vx[j] += float32(nx * impulse * wj)
						s.p.vy[j] += float32(ny * impulse * wj)
					}
				}
			}
		}
	}
}

func (s *State) dampBridges(dt float32) {
	s.bridgeDampingImpulse = resize(s.bridgeDampingImpulse, len(s.bridges))
	clear(s.bridgeDampingImpulse)
	for pass := range s.cfg.BridgeIterations {
		for k := range s.bridges {
			if pass%2 != 0 {
				k = len(s.bridges) - 1 - k
			}
			b := s.bridges[k]
			if b.Damping == 0 {
				continue
			}
			i, j := s.bridgeIndex[b.A], s.bridgeIndex[b.B]
			_, nx, ny := s.bridgeDirection(i, j)
			rel := (float64(s.p.vx[j])-float64(s.p.vx[i]))*nx + (float64(s.p.vy[j])-float64(s.p.vy[i]))*ny
			wi, wj := float64(s.p.invMass[i]), float64(s.p.invMass[j])
			if wi+wj == 0 {
				continue
			}
			// Backward Euler: impulse = dt * damping * final relative speed.
			// Accumulating impulses solves that equation across connected links;
			// repeating passes does not repeatedly apply a full step of damping.
			softness := 1 / (float64(b.Damping) * float64(dt))
			impulse := (rel - softness*s.bridgeDampingImpulse[k]) / (wi + wj + softness)
			s.bridgeDampingImpulse[k] += impulse
			s.p.vx[i] += float32(nx * impulse * wi)
			s.p.vy[i] += float32(ny * impulse * wi)
			s.p.vx[j] -= float32(nx * impulse * wj)
			s.p.vy[j] -= float32(ny * impulse * wj)
		}
	}
}
