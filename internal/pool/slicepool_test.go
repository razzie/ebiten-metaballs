package pool

import "testing"

func TestSlicePoolGetAndPut(t *testing.T) {
	p := NewSlicePool[int](4)
	if p.N() != 4 {
		t.Fatalf("N() = %d, want 4", p.N())
	}

	values := p.Get()
	if len(*values) != 4 || cap(*values) < 4 {
		t.Fatalf("Get() returned len=%d cap=%d, want len=4 and cap>=4", len(*values), cap(*values))
	}
	*values = (*values)[:2]
	p.Put(values)

	values = p.Get()
	if len(*values) != 4 {
		t.Fatalf("Get() after Put returned len=%d, want 4", len(*values))
	}
	p.Put(values)
}

func TestSlicePoolZeroLength(t *testing.T) {
	p := NewSlicePool[int](0)
	if p.N() != 0 {
		t.Fatalf("N() = %d, want 0", p.N())
	}

	values := p.Get()
	if len(*values) != 0 {
		t.Fatalf("Get() returned len=%d, want 0", len(*values))
	}
	p.Put(values)
}
