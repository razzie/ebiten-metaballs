//go:build goexperiment.simd

package softbody

import "simd"

// Go 1.27 currently tops out at 512-bit scalable vectors, i.e. 16 float32 lanes.
const maxSIMDFloat32Lanes = 16

func fractionalHexKernel(x, y, qf, rf []float32, invS, invH float32) {
	var probe simd.Float32s
	lanes := probe.Len()
	vs := simd.BroadcastFloat32s(invS)
	vh := simd.BroadcastFloat32s(invH)
	half := simd.BroadcastFloat32s(0.5)

	i := 0
	for ; i+lanes <= len(x); i += lanes {
		xv := simd.LoadFloat32s(x[i : i+lanes])
		yv := simd.LoadFloat32s(y[i : i+lanes])
		rv := yv.Mul(vh)
		qv := xv.Mul(vs).Sub(rv.Mul(half))
		rv.Store(rf[i : i+lanes])
		qv.Store(qf[i : i+lanes])
	}
	for ; i < len(x); i++ {
		r := y[i] * invH
		rf[i] = r
		qf[i] = x[i]*invS - 0.5*r
	}
}

func pairSpanKernel(p *particleData, i, start, end int, cfg Config) (ax, ay, dvx, dvy, cx, cy float32) {
	var probe simd.Float32s
	lanes := probe.Len()
	if lanes > maxSIMDFloat32Lanes {
		panic("softbody: Go SIMD vector wider than supported scratch buffer")
	}

	xi := simd.BroadcastFloat32s(p.x[i])
	yi := simd.BroadcastFloat32s(p.y[i])
	vxi := simd.BroadcastFloat32s(p.vx[i])
	vyi := simd.BroadcastFloat32s(p.vy[i])
	innerI := simd.BroadcastFloat32s(p.inner[i])
	outerI := simd.BroadcastFloat32s(p.outer[i])
	invMassI := simd.BroadcastFloat32s(p.invMass[i])

	zero := simd.BroadcastFloat32s(0)
	one := simd.BroadcastFloat32s(1)
	eps := simd.BroadcastFloat32s(collisionEpsilon)
	eps2 := simd.BroadcastFloat32s(collisionEpsilon * collisionEpsilon)
	k := simd.BroadcastFloat32s(cfg.ShellStiffness)
	c := simd.BroadcastFloat32s(cfg.ShellDamping)
	coreCorrection := simd.BroadcastFloat32s(cfg.CoreCorrection)
	onePlusRest := simd.BroadcastFloat32s(1 + cfg.Restitution)

	var sx, sy, svx, svy, scx, scy [maxSIMDFloat32Lanes]float32
	var nearZero [maxSIMDFloat32Lanes]int32
	var sameGroup [maxSIMDFloat32Lanes]float32

	j := start
	for ; j+lanes <= end; j += lanes {
		xj := simd.LoadFloat32s(p.x[j : j+lanes])
		yj := simd.LoadFloat32s(p.y[j : j+lanes])
		vxj := simd.LoadFloat32s(p.vx[j : j+lanes])
		vyj := simd.LoadFloat32s(p.vy[j : j+lanes])
		innerJ := simd.LoadFloat32s(p.inner[j : j+lanes])
		outerJ := simd.LoadFloat32s(p.outer[j : j+lanes])
		invMassJ := simd.LoadFloat32s(p.invMass[j : j+lanes])

		dx := xi.Sub(xj)
		dy := yi.Sub(yj)
		d2 := dx.Mul(dx).Add(dy.Mul(dy))
		outer := outerI.Add(outerJ)
		withinOuter := d2.Less(outer.Mul(outer))
		nonZero := d2.Greater(eps2)
		active := withinOuter.And(nonZero)

		// Exact/near-coincident centers need an ID-derived normal, handled scalar.
		withinOuter.And(d2.LessEqual(eps2)).ToInt32s().Store(nearZero[:lanes])

		dist := d2.Max(eps2).Sqrt()
		nx := dx.Div(dist)
		ny := dy.Div(dist)
		inner := innerI.Add(innerJ)
		thickness := outer.Sub(inner).Max(eps)
		penetration := outer.Sub(dist).Max(zero)
		compression := penetration.Div(thickness).Max(zero).Min(one)
		rel := vxi.Sub(vxj).Mul(nx).Add(vyi.Sub(vyj).Mul(ny))

		force := penetration.Mul(compression).Mul(k).Sub(rel.Mul(c)).Max(zero).Masked(active)
		if cfg.AttractionRange > 0 && cfg.AttractionStrength > 0 {
			for lane := range lanes {
				sameGroup[lane] = 0
				if p.group[i] == p.group[j+lane] {
					sameGroup[lane] = 1
				}
			}
			groupMask := simd.LoadFloat32s(sameGroup[:lanes]).Greater(zero)
			gap := dist.Sub(outer).Max(zero)
			falloff := one.Sub(gap.Div(simd.BroadcastFloat32s(cfg.AttractionRange))).Max(zero)
			pull := falloff.Mul(falloff).Mul(simd.BroadcastFloat32s(cfg.AttractionStrength))
			force = force.Sub(pull.Masked(groupMask.And(nonZero)))
		}
		accel := force.Mul(invMassI)
		fx := nx.Mul(accel)
		fy := ny.Mul(accel)

		corePen := inner.Sub(dist).Max(zero).Masked(active)
		denom := invMassI.Add(invMassJ).Max(eps)
		share := invMassI.Div(denom)
		corr := corePen.Mul(coreCorrection).Mul(share)
		px := nx.Mul(corr)
		py := ny.Mul(corr)

		closing := rel.Min(zero)
		impulse := closing.Neg().Mul(onePlusRest).Div(denom)
		coreMask := corePen.Greater(zero)
		impulse = impulse.Masked(coreMask)
		dv := impulse.Mul(invMassI)
		ix := nx.Mul(dv)
		iy := ny.Mul(dv)

		fx.Store(sx[:lanes])
		fy.Store(sy[:lanes])
		ix.Store(svx[:lanes])
		iy.Store(svy[:lanes])
		px.Store(scx[:lanes])
		py.Store(scy[:lanes])
		for lane := range lanes {
			ax += sx[lane]
			ay += sy[lane]
			dvx += svx[lane]
			dvy += svy[lane]
			cx += scx[lane]
			cy += scy[lane]
			if nearZero[lane] != 0 {
				a, b, c0, d, e, f := pairScalar(p, i, j+lane, cfg)
				ax += a
				ay += b
				dvx += c0
				dvy += d
				cx += e
				cy += f
			}
		}
	}

	for ; j < end; j++ {
		a, b, c0, d, e, f := pairScalar(p, i, j, cfg)
		ax += a
		ay += b
		dvx += c0
		dvy += d
		cx += e
		cy += f
	}
	return
}

func integrateKernel(p *particleData, ax, ay, dvx, dvy, corrX, corrY []float32, dt, dampingStep, massDampingStep, restitution float32, bounds Bounds, workers int) {
	damping := 1 / (1 + dampingStep)
	parallelFor(p.len(), workers, 512, func(start, end int) {
		var probe simd.Float32s
		lanes := probe.Len()
		vdt := simd.BroadcastFloat32s(dt)
		vdamp := simd.BroadcastFloat32s(damping)
		vdampDenom := simd.BroadcastFloat32s(1 + dampingStep)
		vmassDamp := simd.BroadcastFloat32s(massDampingStep)
		one := simd.BroadcastFloat32s(1)
		vrest := simd.BroadcastFloat32s(restitution)
		minX := simd.BroadcastFloat32s(bounds.MinX)
		minY := simd.BroadcastFloat32s(bounds.MinY)
		maxX := simd.BroadcastFloat32s(bounds.MaxX)
		maxY := simd.BroadcastFloat32s(bounds.MaxY)
		zero := simd.BroadcastFloat32s(0)

		i := start
		for ; i+lanes <= end; i += lanes {
			particleDamping := vdamp
			if massDampingStep > 0 {
				invMass := simd.LoadFloat32s(p.invMass[i : i+lanes])
				particleDamping = one.Div(vdampDenom.Add(vmassDamp.Div(invMass)))
			}
			vx := simd.LoadFloat32s(p.vx[i : i+lanes]).Add(simd.LoadFloat32s(dvx[i : i+lanes]))
			vy := simd.LoadFloat32s(p.vy[i : i+lanes]).Add(simd.LoadFloat32s(dvy[i : i+lanes]))
			vx = vx.Add(simd.LoadFloat32s(ax[i : i+lanes]).Mul(vdt)).Mul(particleDamping)
			vy = vy.Add(simd.LoadFloat32s(ay[i : i+lanes]).Mul(vdt)).Mul(particleDamping)

			x := simd.LoadFloat32s(p.x[i : i+lanes]).Add(simd.LoadFloat32s(corrX[i : i+lanes])).Add(vx.Mul(vdt))
			y := simd.LoadFloat32s(p.y[i : i+lanes]).Add(simd.LoadFloat32s(corrY[i : i+lanes])).Add(vy.Mul(vdt))
			radius := simd.LoadFloat32s(p.inner[i : i+lanes])
			xMin, xMax := minX.Add(radius), maxX.Sub(radius)
			yMin, yMax := minY.Add(radius), maxY.Sub(radius)

			xLo := x.Less(xMin)
			xHi := x.Greater(xMax)
			yLo := y.Less(yMin)
			yHi := y.Greater(yMax)
			x = x.Max(xMin).Min(xMax)
			y = y.Max(yMin).Min(yMax)

			// Reflect only when the velocity points farther out of bounds.
			xBounce := xLo.And(vx.Less(zero)).Or(xHi.And(vx.Greater(zero)))
			yBounce := yLo.And(vy.Less(zero)).Or(yHi.And(vy.Greater(zero)))
			vx = vx.Neg().Mul(vrest).IfElse(xBounce, vx)
			vy = vy.Neg().Mul(vrest).IfElse(yBounce, vy)

			x.Store(p.x[i : i+lanes])
			y.Store(p.y[i : i+lanes])
			vx.Store(p.vx[i : i+lanes])
			vy.Store(p.vy[i : i+lanes])
		}
		for ; i < end; i++ {
			particleDamping := damping
			if massDampingStep > 0 {
				particleDamping = 1 / (1 + dampingStep + massDampingStep/p.invMass[i])
			}
			vx := (p.vx[i] + dvx[i] + ax[i]*dt) * particleDamping
			vy := (p.vy[i] + dvy[i] + ay[i]*dt) * particleDamping
			x := p.x[i] + corrX[i] + vx*dt
			y := p.y[i] + corrY[i] + vy*dt
			radius := p.inner[i]
			x, vx = confine(x, vx, bounds.MinX+radius, bounds.MaxX-radius, restitution)
			y, vy = confine(y, vy, bounds.MinY+radius, bounds.MaxY-radius, restitution)

			p.x[i], p.y[i], p.vx[i], p.vy[i] = x, y, vx, vy
		}
	})
}
