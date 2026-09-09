package metaballs

import (
	"sync"

	"github.com/razzie/ebiten-metaballs/internal/bitset"
	"github.com/razzie/ebiten-metaballs/internal/pool"
)

type materializeBuffers struct {
	circlesPtr *[]Circle
	bridgesPtr *[]Bridge
}

// groupPrep holds the per-group, per-tile included-circle bitset computed
// once by prepareGroupsForTile and reused by both tier-picking and
// materializing, instead of recomputing it in each step. included is nil
// when the group has no survivors for the tile.
type groupPrep struct {
	included    bitset.BitSet
	circleCount int
	bridgeCount int
}

// groupPrepPool recycles slices of groupPrep, initializing each group's
// included bitset to the pool's maxCircles. This avoids repeated allocation
// and bitset initialization on the per-tile filtering hot path.
type groupPrepPool struct {
	pool       sync.Pool
	numGroups  int
	maxCircles int
}

func (p *groupPrepPool) init(numGroups, maxCircles int) {
	p.numGroups = numGroups
	p.maxCircles = maxCircles
	p.pool.New = func() any {
		s := make([]groupPrep, numGroups)
		for i := range s {
			s[i].included.Init(maxCircles)
		}
		return &s
	}
}

func (p *groupPrepPool) get() *[]groupPrep {
	ptr := p.pool.Get().(*[]groupPrep)
	if cap(*ptr) < p.numGroups || (*ptr)[:1][0].included.Len() < p.maxCircles {
		s := make([]groupPrep, p.numGroups)
		for i := range s {
			s[i].included.Init(p.maxCircles)
		}
		ptr = &s
	}
	*ptr = (*ptr)[:p.numGroups]
	return ptr
}

func (p *groupPrepPool) put(ptr *[]groupPrep) {
	for i := range *ptr {
		(*ptr)[i].included.Reset()
	}
	*ptr = (*ptr)[:0]
	p.pool.Put(ptr)
}

// rendererPools bundles every scratch pool a Renderer uses on its Draw hot
// path, so each Renderer recycles its own buffers instead of sharing
// package-level pools (which let one renderer's workload poison another's).
// Every pool has a fixed default length; ensure grows those defaults
// (recreating pools) to fit larger inputs, but never shrinks them.
type rendererPools struct {
	ints        pool.SlicePool[int]
	circles     pool.SlicePool[Circle]
	bridges     pool.SlicePool[Bridge]
	groups      pool.SlicePool[Group]
	preps       groupPrepPool
	float32s    pool.SlicePool[float32]
	int32s      pool.SlicePool[int32]
	materialize pool.SlicePool[materializeBuffers]
}

func (p *rendererPools) init(numGroups, maxCircles, maxBridges int) {
	p.ints.Init(maxCircles)
	p.circles.Init(maxCircles)
	p.bridges.Init(maxBridges)
	p.groups.Init(numGroups)
	p.preps.init(numGroups, maxCircles)
	p.float32s.Init(maxCircles)
	p.int32s.Init(simdLaneCount())
	p.materialize.Init(numGroups)
}

// ensure grows the pools' default lengths to at least the given values,
// recreating any pool whose current default is too small. Defaults never
// shrink. Must be called before the pools are used concurrently (Draw calls
// it before spawning its workers). Concurrent use is not safe.
func (p *rendererPools) ensure(numGroups, maxCircles, maxBridges int) {
	if p.ints.N() < maxCircles {
		p.ints.Init(maxCircles)
	}
	if p.circles.N() < maxCircles {
		p.circles.Init(maxCircles)
	}
	if p.float32s.N() < maxCircles {
		p.float32s.Init(maxCircles)
	}
	if p.bridges.N() < maxBridges {
		p.bridges.Init(maxBridges)
	}
	if p.groups.N() < numGroups {
		p.groups.Init(numGroups)
	}
	if p.preps.numGroups < numGroups || p.preps.maxCircles < maxCircles {
		p.preps.init(numGroups, maxCircles)
	}
	if p.materialize.N() < numGroups {
		p.materialize.Init(numGroups)
	}
}
