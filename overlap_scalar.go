//go:build !goexperiment.simd

package metaballs

import "github.com/razzie/ebiten-metaballs/internal/bitset"

// simdLaneCount returns 0 in the scalar build: the int32 SIMD mask scratch
// pool is unused.
func simdLaneCount() int {
	return 0
}

// filterCirclesOverlap sets included[i] true for each circle whose AABB
// overlaps tile, returning whether any circle matched. pools is unused in
// the scalar build (no SIMD scratch buffers needed) but kept for a uniform
// signature with the SIMD build.
func filterCirclesOverlap(pools *rendererPools, circles []Circle, tile tileBounds, included *bitset.BitSet, smoothK float32) bool {
	found := false
	for i, c := range circles {
		if circleOverlapsTile(c, tile, smoothK) {
			included.Set(i)
			found = true
		}
	}
	return found
}
