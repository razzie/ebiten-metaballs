package metaballs

import "testing"

func TestSlicePoolUniformLength(t *testing.T) {
	p := newSlicePool[int](8)

	// sync.Pool gives no reuse guarantee (GC, or -race instrumentation, may
	// drop pooled items), so assert: every get has uniform length n, and
	// whenever the pool does hand back the same slice pointer, its backing
	// array must be the original one (never a reallocated copy).
	var firstPtr *[]int
	var backing *int
	for range 8 {
		ptr := p.get()
		if got := len(*ptr); got != 8 {
			t.Fatalf("expected uniform length 8, got %d", got)
		}
		if firstPtr == nil {
			firstPtr, backing = ptr, &(*ptr)[0]
		} else if ptr == firstPtr && &(*ptr)[0] != backing {
			t.Fatalf("reused slice got new backing array %p, want %p", &(*ptr)[0], backing)
		}
		p.put(ptr)
	}
}

func TestSlicePoolZeroLength(t *testing.T) {
	p := newSlicePool[Circle](0)

	ptr := p.get()
	if len(*ptr) != 0 {
		t.Fatalf("expected length 0, got %d", len(*ptr))
	}
	p.put(ptr)

	// Second get must still work (pool reuse of a zero-length slice).
	ptr = p.get()
	if len(*ptr) != 0 {
		t.Fatalf("expected length 0 after reuse, got %d", len(*ptr))
	}
	p.put(ptr)
}

func TestBitSetPoolUniformLength(t *testing.T) {
	p := newBitSetPool(100)

	for i := range 5 {
		b := p.get()
		if b.Len() != 100 {
			t.Fatalf("expected uniform bitset length 100, got %d", b.Len())
		}
		if i > 0 && b.Any() {
			t.Fatalf("expected reset bitset from pool, got %d bits set", b.Count())
		}
		b.Set(3)
		b.Set(99)
		p.put(b)
	}
}

func TestRendererPoolsEnsureGrowsButNeverShrinks(t *testing.T) {
	p := newRendererPools(2, 10, 5)

	p.ensure(4, 30, 12)
	if p.groups.n != 4 || p.preps.n != 4 {
		t.Errorf("expected group/prep pools grown to 4, got %d / %d", p.groups.n, p.preps.n)
	}
	if p.bitsets.n != 30 || p.ints.n != 30 || p.circles.n != 30 || p.float32s.n != 30 {
		t.Errorf("expected circle-sized pools grown to 30, got %d / %d / %d / %d",
			p.bitsets.n, p.ints.n, p.circles.n, p.float32s.n)
	}
	if p.bridges.n != 12 {
		t.Errorf("expected bridge pool grown to 12, got %d", p.bridges.n)
	}

	// Asking for less must not shrink any pool.
	p.ensure(1, 5, 2)
	if p.groups.n != 4 || p.preps.n != 4 {
		t.Errorf("expected group/prep pools to stay 4, got %d / %d", p.groups.n, p.preps.n)
	}
	if p.bitsets.n != 30 || p.ints.n != 30 || p.circles.n != 30 || p.float32s.n != 30 {
		t.Errorf("expected circle-sized pools to stay 30, got %d / %d / %d / %d",
			p.bitsets.n, p.ints.n, p.circles.n, p.float32s.n)
	}
	if p.bridges.n != 12 {
		t.Errorf("expected bridge pool to stay 12, got %d", p.bridges.n)
	}
}

func TestRendererPoolsEnsureMixedGrowth(t *testing.T) {
	p := newRendererPools(4, 10, 5)
	groups, bitsets, bridges := p.groups, p.bitsets, p.bridges

	// Grow only the circle dimension: group/bridge pools must keep their
	// existing instances (and thus their already-pooled buffers).
	p.ensure(4, 20, 5)
	if p.groups != groups {
		t.Errorf("expected group pool instance to survive circle-only growth")
	}
	if p.bridges != bridges {
		t.Errorf("expected bridge pool instance to survive circle-only growth")
	}
	if p.bitsets == bitsets {
		t.Errorf("expected bitset pool to be recreated for larger circles")
	}
}
