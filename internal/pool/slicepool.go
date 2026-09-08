package pool

import "sync"

// SlicePool recycles slices of T to avoid repeated allocation on the
// per-tile filtering hot path. All slices in a pool share the pool's fixed
// default length n: get returns a *[]T with length exactly n (reusing the
// backing array when possible), so mixed request sizes never trigger a
// realloc. Callers that need fewer elements take a prefix of the result.
type SlicePool[T any] struct {
	pool sync.Pool
	n    int
}

func NewSlicePool[T any](n int) *SlicePool[T] {
	p := new(SlicePool[T])
	p.Init(n)
	return p
}

func (p *SlicePool[T]) Init(n int) {
	p.n = n
	p.pool.New = func() any {
		s := make([]T, n)
		return &s
	}
}

func (p *SlicePool[T]) N() int {
	return p.n
}

func (p *SlicePool[T]) Get() *[]T {
	ptr := p.pool.Get().(*[]T)
	if cap(*ptr) < p.n {
		s := make([]T, p.n)
		ptr = &s
	} else {
		*ptr = (*ptr)[:p.n]
	}
	return ptr
}

func (p *SlicePool[T]) Put(ptr *[]T) {
	*ptr = (*ptr)[:0]
	p.pool.Put(ptr)
}
