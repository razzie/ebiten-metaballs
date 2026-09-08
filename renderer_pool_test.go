package metaballs

import (
	"testing"

	"github.com/razzie/ebiten-metaballs/internal/pool"
)

func TestSlicePoolUniformLength(t *testing.T) {
	p := pool.NewSlicePool[int](8)

	// sync.Pool gives no reuse guarantee (GC, or -race instrumentation, may
	// drop pooled items), so assert: every get has uniform length n, and
	// whenever the pool does hand back the same slice pointer, its backing
	// array must be the original one (never a reallocated copy).
	var firstPtr *[]int
	var backing *int
	for range 8 {
		ptr := p.Get()
		if got := len(*ptr); got != 8 {
			t.Fatalf("expected uniform length 8, got %d", got)
		}
		if firstPtr == nil {
			firstPtr, backing = ptr, &(*ptr)[0]
		} else if ptr == firstPtr && &(*ptr)[0] != backing {
			t.Fatalf("reused slice got new backing array %p, want %p", &(*ptr)[0], backing)
		}
		p.Put(ptr)
	}
}

func TestSlicePoolZeroLength(t *testing.T) {
	p := pool.NewSlicePool[Circle](0)

	ptr := p.Get()
	if len(*ptr) != 0 {
		t.Fatalf("expected length 0, got %d", len(*ptr))
	}
	p.Put(ptr)

	// Second get must still work (pool reuse of a zero-length slice).
	ptr = p.Get()
	if len(*ptr) != 0 {
		t.Fatalf("expected length 0 after reuse, got %d", len(*ptr))
	}
	p.Put(ptr)
}

func TestRendererPoolsEnsureGrowsButNeverShrinks(t *testing.T) {
	var p rendererPools
	p.init(2, 10, 5)

	p.ensure(4, 30, 12)
	if p.groups.N() != 4 || p.preps.numGroups != 4 {
		t.Errorf("expected group/prep pools grown to 4, got %d / %d", p.groups.N(), p.preps.numGroups)
	}
	if p.preps.maxCircles != 30 || p.ints.N() != 30 || p.circles.N() != 30 || p.float32s.N() != 30 {
		t.Errorf("expected circle-sized pools grown to 30, got %d / %d / %d / %d",
			p.preps.maxCircles, p.ints.N(), p.circles.N(), p.float32s.N())
	}
	if p.bridges.N() != 12 {
		t.Errorf("expected bridge pool grown to 12, got %d", p.bridges.N())
	}

	// Asking for less must not shrink any pool.
	p.ensure(1, 5, 2)
	if p.groups.N() != 4 || p.preps.numGroups != 4 {
		t.Errorf("expected group/prep pools to stay 4, got %d / %d", p.groups.N(), p.preps.numGroups)
	}
	if p.preps.maxCircles != 30 || p.ints.N() != 30 || p.circles.N() != 30 || p.float32s.N() != 30 {
		t.Errorf("expected circle-sized pools to stay 30, got %d / %d / %d / %d",
			p.preps.maxCircles, p.ints.N(), p.circles.N(), p.float32s.N())
	}
	if p.bridges.N() != 12 {
		t.Errorf("expected bridge pool to stay 12, got %d", p.bridges.N())
	}
}

func TestRendererPoolsEnsureMixedGrowth(t *testing.T) {
	var p rendererPools
	p.init(4, 10, 5)
	groups, preps, bridges := p.groups.N(), p.preps.maxCircles, p.bridges.N()

	// Grow only the circle dimension: group/bridge pools must keep their
	// existing instances (and thus their already-pooled buffers).
	p.ensure(4, 20, 5)
	if p.groups.N() != groups {
		t.Errorf("expected group pool instance to survive circle-only growth")
	}
	if p.bridges.N() != bridges {
		t.Errorf("expected bridge pool instance to survive circle-only growth")
	}
	if p.preps.maxCircles == preps {
		t.Errorf("expected prep pool to be recreated for larger circles")
	}
}
