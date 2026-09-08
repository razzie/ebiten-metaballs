package bitset

import (
	"math/bits"
)

type BitSet struct {
	words []uint64
	n     int
}

func New(n int) *BitSet {
	b := new(BitSet)
	b.Init(n)
	return b
}

func (b *BitSet) Init(n int) {
	b.words = make([]uint64, (n+63)/64)
	b.n = n
}

func (b *BitSet) Len() int {
	return b.n
}

func (b *BitSet) Set(i int) {
	if uint(i) >= uint(b.n) {
		panic("bitset: index out of range")
	}
	b.words[i>>6] |= uint64(1) << (i & 63)
}

func (b *BitSet) Clear(i int) {
	if uint(i) >= uint(b.n) {
		panic("bitset: index out of range")
	}
	b.words[i>>6] &^= uint64(1) << (i & 63)
}

func (b *BitSet) Toggle(i int) {
	if uint(i) >= uint(b.n) {
		panic("bitset: index out of range")
	}
	b.words[i>>6] ^= uint64(1) << (i & 63)
}

func (b *BitSet) Has(i int) bool {
	if uint(i) >= uint(b.n) {
		panic("bitset: index out of range")
	}
	return b.words[i>>6]&(uint64(1)<<(i&63)) != 0
}

func (b *BitSet) Reset() {
	clear(b.words)
}

func (b *BitSet) Count() int {
	n := 0
	for _, w := range b.words {
		n += bits.OnesCount64(w)
	}
	return n
}

func (b *BitSet) Any() bool {
	for _, w := range b.words {
		if w != 0 {
			return true
		}
	}
	return false
}

func (b *BitSet) None() bool {
	return !b.Any()
}

func (b *BitSet) All() bool {
	if b.n == 0 {
		return true
	}

	last := len(b.words) - 1

	for _, w := range b.words[:last] {
		if w != ^uint64(0) {
			return false
		}
	}

	remaining := b.n & 63
	if remaining == 0 {
		return b.words[last] == ^uint64(0)
	}

	mask := uint64(1)<<remaining - 1
	return b.words[last]&mask == mask
}

func (b *BitSet) NextSet(i int) (int, bool) {
	if i < 0 {
		i = 0
	}
	if i >= b.n {
		return 0, false
	}

	wordIndex := i >> 6
	w := b.words[wordIndex]

	// Ignore bits before i.
	w &= ^uint64(0) << (i & 63)

	for {
		if w != 0 {
			j := wordIndex*64 + bits.TrailingZeros64(w)
			if j < b.n {
				return j, true
			}
			return 0, false
		}

		wordIndex++
		if wordIndex >= len(b.words) {
			return 0, false
		}
		w = b.words[wordIndex]
	}
}
