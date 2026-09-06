//go:build goexperiment.simd

package metaballs

import (
	"simd"

	"github.com/razzie/ebiten-metaballs/internal/bitset"
)

// simdCircleThreshold is the minimum circle count before the SIMD path's
// AoS->SoA transpose cost is worth paying over the plain scalar scan.
const simdCircleThreshold = 8

// simdLaneCount returns the SIMD Float32s lane width, used to size the
// int32 mask scratch pool.
func simdLaneCount() int {
	return (simd.Float32s{}).Len()
}

// filterCirclesOverlap sets included[i] true for each circle whose AABB
// overlaps tile, returning whether any circle matched. For large enough
// groups it batches the AABB test across SIMD lanes; small groups fall
// back to the scalar scan since the AoS->SoA transpose wouldn't pay off.
// pools supplies the float32/int32 scratch buffers.
func filterCirclesOverlap(pools *rendererPools, circles []Circle, tile tileBounds, included *bitset.BitSet, smoothK float32) bool {
	n := len(circles)
	if n < simdCircleThreshold {
		found := false
		for i, c := range circles {
			if circleOverlapsTile(c, tile, smoothK) {
				included.Set(i)
				found = true
			}
		}
		return found
	}

	xsPtr := pools.float32s.get()
	defer pools.float32s.put(xsPtr)
	ysPtr := pools.float32s.get()
	defer pools.float32s.put(ysPtr)
	rsPtr := pools.float32s.get()
	defer pools.float32s.put(rsPtr)
	xs, ys, rs := (*xsPtr)[:n], (*ysPtr)[:n], (*rsPtr)[:n]

	for i, c := range circles {
		xs[i], ys[i], rs[i] = c.X, c.Y, c.Radius+smoothK // pad by smoothK: blending bulges the shape beyond its radius
	}

	minX := simd.BroadcastFloat32s(tile.MinX)
	maxX := simd.BroadcastFloat32s(tile.MaxX)
	minY := simd.BroadcastFloat32s(tile.MinY)
	maxY := simd.BroadcastFloat32s(tile.MaxY)

	maskBufPtr := pools.int32s.get()
	defer pools.int32s.put(maskBufPtr)
	maskBuf := (*maskBufPtr)[:simdLaneCount()]

	found := false
	for i := 0; i < n; {
		vx, ln := simd.LoadFloat32sPart(xs[i:])
		vy, _ := simd.LoadFloat32sPart(ys[i:])
		vr, _ := simd.LoadFloat32sPart(rs[i:])

		maskX := vx.Add(vr).GreaterEqual(minX).And(vx.Sub(vr).LessEqual(maxX))
		maskY := vy.Add(vr).GreaterEqual(minY).And(vy.Sub(vr).LessEqual(maxY))
		mask := maskX.And(maskY).ToInt32s()

		written := mask.StorePart(maskBuf[:ln])
		for k := 0; k < written; k++ {
			if maskBuf[k] != 0 {
				included.Set(i + k)
				found = true
			}
		}

		i += ln
	}

	return found
}
