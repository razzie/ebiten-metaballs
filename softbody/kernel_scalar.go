//go:build !goexperiment.simd

package softbody

func fractionalHexKernel(x, y, qf, rf []float32, invS, invH float32) {
	for i := range x {
		r := y[i] * invH
		rf[i] = r
		qf[i] = x[i]*invS - 0.5*r
	}
}

func pairSpanKernel(p *particleData, i, start, end int, cfg Config) (ax, ay, dvx, dvy, cx, cy float32) {
	for j := start; j < end; j++ {
		a, b, c, d, e, f := pairScalar(p, i, j, cfg)
		ax += a
		ay += b
		dvx += c
		dvy += d
		cx += e
		cy += f
	}
	return
}

func integrateKernel(p *particleData, ax, ay, dvx, dvy, corrX, corrY []float32, dt, damping, restitution float32, bounds Bounds, workers int) {
	parallelFor(p.len(), workers, 512, func(start, end int) {
		for i := start; i < end; i++ {
			vx := (p.vx[i] + dvx[i] + ax[i]*dt) * damping
			vy := (p.vy[i] + dvy[i] + ay[i]*dt) * damping
			x := p.x[i] + corrX[i] + vx*dt
			y := p.y[i] + corrY[i] + vy*dt

			radius := p.inner[i]
			x, vx = confine(x, vx, bounds.MinX+radius, bounds.MaxX-radius, restitution)
			y, vy = confine(y, vy, bounds.MinY+radius, bounds.MaxY-radius, restitution)

			p.x[i], p.y[i] = x, y
			p.vx[i], p.vy[i] = vx, vy
		}
	})
}
