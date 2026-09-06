package metaballs

import (
	"sync"

	"github.com/razzie/ebiten-metaballs/internal/bitset"
)

// slicePool recycles slices of T to avoid repeated allocation on the
// per-tile filtering hot path. All slices in a pool share the pool's fixed
// default length n: get returns a *[]T with length exactly n (reusing the
// backing array when possible), so mixed request sizes never trigger a
// realloc. Callers that need fewer elements take a prefix of the result.
type slicePool[T any] struct {
	pool sync.Pool
	n    int
}

func newSlicePool[T any](n int) *slicePool[T] {
	p := &slicePool[T]{n: n}
	p.pool.New = func() any {
		s := make([]T, n)
		return &s
	}
	return p
}

func (p *slicePool[T]) get() *[]T {
	ptr := p.pool.Get().(*[]T)
	*ptr = (*ptr)[:p.n]
	return ptr
}

func (p *slicePool[T]) put(ptr *[]T) {
	*ptr = (*ptr)[:0]
	p.pool.Put(ptr)
}

// bitsetPool recycles BitSets of a fixed default length n: get always
// returns a Reset() bitset with Len() == n, so callers never hit the
// allocate-on-mismatch path that a mixed-length pool would cause.
type bitsetPool struct {
	pool sync.Pool
	n    int
}

func newBitSetPool(n int) *bitsetPool {
	p := &bitsetPool{n: n}
	p.pool.New = func() any { return bitset.New(n) }
	return p
}

func (p *bitsetPool) get() *bitset.BitSet {
	b := p.pool.Get().(*bitset.BitSet)
	b.Reset()
	return b
}

func (p *bitsetPool) put(b *bitset.BitSet) {
	b.Reset()
	p.pool.Put(b)
}

// groupPrep holds the per-group, per-tile included-circle bitset computed
// once by prepareGroupsForTile and reused by both tier-picking and
// materializing, instead of recomputing it in each step. included is nil
// when the group has no survivors for the tile.
type groupPrep struct {
	included    *bitset.BitSet
	circleCount int
	bridgeCount int
}

// rendererPools bundles every scratch pool a Renderer uses on its Draw hot
// path, so each Renderer recycles its own buffers instead of sharing
// package-level pools (which let one renderer's workload poison another's).
// Every pool has a fixed default length; ensure grows those defaults
// (recreating pools) to fit larger inputs, but never shrinks them.
type rendererPools struct {
	bitsets  *bitsetPool
	ints     *slicePool[int]
	circles  *slicePool[Circle]
	bridges  *slicePool[Bridge]
	groups   *slicePool[Group]
	preps    *slicePool[groupPrep]
	float32s *slicePool[float32]
	int32s   *slicePool[int32]
}

// newRendererPools creates pools whose default lengths fit up to numGroups
// groups with up to maxCircles circles / maxBridges bridges each.
func newRendererPools(numGroups, maxCircles, maxBridges int) *rendererPools {
	return &rendererPools{
		bitsets:  newBitSetPool(maxCircles),
		ints:     newSlicePool[int](maxCircles),
		circles:  newSlicePool[Circle](maxCircles),
		bridges:  newSlicePool[Bridge](maxBridges),
		groups:   newSlicePool[Group](numGroups),
		preps:    newSlicePool[groupPrep](numGroups),
		float32s: newSlicePool[float32](maxCircles),
		int32s:   newSlicePool[int32](simdLaneCount()),
	}
}

// ensure grows the pools' default lengths to at least the given values,
// recreating any pool whose current default is too small. Defaults never
// shrink. Must be called before the pools are used concurrently (Draw calls
// it before spawning its workers).
func (p *rendererPools) ensure(numGroups, maxCircles, maxBridges int) {
	if p.bitsets.n < maxCircles {
		p.bitsets = newBitSetPool(maxCircles)
	}
	if p.ints.n < maxCircles {
		p.ints = newSlicePool[int](maxCircles)
	}
	if p.circles.n < maxCircles {
		p.circles = newSlicePool[Circle](maxCircles)
	}
	if p.float32s.n < maxCircles {
		p.float32s = newSlicePool[float32](maxCircles)
	}
	if p.bridges.n < maxBridges {
		p.bridges = newSlicePool[Bridge](maxBridges)
	}
	if p.groups.n < numGroups {
		p.groups = newSlicePool[Group](numGroups)
	}
	if p.preps.n < numGroups {
		p.preps = newSlicePool[groupPrep](numGroups)
	}
}
