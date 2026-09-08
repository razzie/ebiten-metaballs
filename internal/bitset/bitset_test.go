package bitset

import "testing"

func TestBitSetMutationAndQueries(t *testing.T) {
	set := New(65)
	if set.Len() != 65 {
		t.Fatalf("Len() = %d, want 65", set.Len())
	}
	if !set.None() || set.Any() || set.Count() != 0 {
		t.Fatal("new bitset should be empty")
	}

	for _, index := range []int{0, 63, 64} {
		set.Set(index)
		if !set.Has(index) {
			t.Errorf("Has(%d) = false after Set", index)
		}
	}
	if !set.Any() || set.None() || set.Count() != 3 {
		t.Fatalf("unexpected state after Set: any=%v none=%v count=%d", set.Any(), set.None(), set.Count())
	}

	set.Clear(63)
	if set.Has(63) || set.Count() != 2 {
		t.Fatalf("unexpected state after Clear: has(63)=%v count=%d", set.Has(63), set.Count())
	}
	set.Toggle(63)
	if !set.Has(63) || set.Count() != 3 {
		t.Fatalf("unexpected state after Toggle on: has(63)=%v count=%d", set.Has(63), set.Count())
	}
	set.Toggle(63)
	if set.Has(63) || set.Count() != 2 {
		t.Fatalf("unexpected state after Toggle off: has(63)=%v count=%d", set.Has(63), set.Count())
	}

	set.Reset()
	if !set.None() || set.Any() || set.Count() != 0 {
		t.Fatal("Reset should empty the bitset")
	}
}

func TestBitSetAll(t *testing.T) {
	tests := []struct {
		name string
		n    int
		set  []int
		want bool
	}{
		{name: "empty", n: 0, want: true},
		{name: "partial", n: 65, set: []int{0, 63, 64}, want: false},
		{name: "full single word", n: 64, set: makeRange(64), want: true},
		{name: "full partial word", n: 65, set: makeRange(65), want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			set := New(test.n)
			for _, index := range test.set {
				set.Set(index)
			}
			if got := set.All(); got != test.want {
				t.Fatalf("All() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestBitSetNextSet(t *testing.T) {
	set := New(65)
	for _, index := range []int{1, 63, 64} {
		set.Set(index)
	}

	tests := []struct {
		start int
		want  int
		ok    bool
	}{
		{start: -1, want: 1, ok: true},
		{start: 1, want: 1, ok: true},
		{start: 2, want: 63, ok: true},
		{start: 64, want: 64, ok: true},
		{start: 65, ok: false},
		{start: 100, ok: false},
	}

	for _, test := range tests {
		got, ok := set.NextSet(test.start)
		if got != test.want || ok != test.ok {
			t.Errorf("NextSet(%d) = (%d, %v), want (%d, %v)", test.start, got, ok, test.want, test.ok)
		}
	}
}

func TestBitSetInvalidIndexPanics(t *testing.T) {
	for _, test := range []struct {
		name string
		fn   func(*BitSet)
	}{
		{name: "Set", fn: func(set *BitSet) { set.Set(-1) }},
		{name: "Clear", fn: func(set *BitSet) { set.Clear(2) }},
		{name: "Toggle", fn: func(set *BitSet) { set.Toggle(2) }},
		{name: "Has", fn: func(set *BitSet) { set.Has(2) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("expected panic")
				}
			}()
			test.fn(New(2))
		})
	}
}

func makeRange(n int) []int {
	indices := make([]int, n)
	for index := range indices {
		indices[index] = index
	}
	return indices
}
