package metaballs

import (
	"testing"

	"github.com/razzie/ebiten-metaballs/internal/bitset"
)

func mustGroup(circles []Circle, bridges []Bridge) Group {
	return Group{Circles: circles, Bridges: bridges}
}

// poolsForGroups returns rendererPools whose default buffer lengths exactly
// fit the given groups, for tests that call the filtering/materialization
// functions directly without a Renderer.
func poolsForGroups(groups []Group) *rendererPools {
	maxCircles, maxBridges := 0, 0
	for i := range groups {
		maxCircles = max(maxCircles, len(groups[i].Circles))
		maxBridges = max(maxBridges, len(groups[i].Bridges))
	}
	return newRendererPools(len(groups), maxCircles, maxBridges)
}

func TestProbeMaterializeAgree(t *testing.T) {
	tile := tileBounds{MinX: 0, MinY: 0, MaxX: 0.5, MaxY: 0.5}

	groups := []Group{
		mustGroup(
			[]Circle{
				{X: 0.1, Y: 0.1, Radius: 0.05}, // overlaps tile
				{X: 0.9, Y: 0.9, Radius: 0.05}, // outside tile
				{X: 0.6, Y: 0.1, Radius: 0.05}, // outside tile, but bridged to circle 0
			},
			[]Bridge{
				{A: 0, B: 2, MiddleRadius: 0.2}, // segment crosses the tile edge
				{A: 1, B: 2, MiddleRadius: 0.01},
			},
		),
	}

	any, mainCircles, mainBridges, otherCircles, otherBridges := probeGroupsForTile(poolsForGroups(groups), groups, tile)
	if !any {
		t.Fatalf("expected probe to find overlap")
	}

	filtered, release := materializeGroupsForTile(poolsForGroups(groups), groups, tile)
	defer release()

	if len(filtered) != 1 {
		t.Fatalf("expected 1 filtered group, got %d", len(filtered))
	}
	fg := filtered[0]

	if len(fg.Circles) != mainCircles || len(fg.Circles) != otherCircles {
		t.Errorf("circle count mismatch: probe main=%d other=%d materialize=%d", mainCircles, otherCircles, len(fg.Circles))
	}
	if len(fg.Bridges) != mainBridges || len(fg.Bridges) != otherBridges {
		t.Errorf("bridge count mismatch: probe main=%d other=%d materialize=%d", mainBridges, otherBridges, len(fg.Bridges))
	}

	// Circle 0 (directly overlapping) and circle 2 (pulled in via the
	// 0-2 bridge crossing the tile) survive. Circle 1 does not: the 1-2
	// bridge is padded by its own small MiddleRadius, and neither its
	// segment nor circle 1 itself overlaps the tile.
	if len(fg.Circles) != 2 {
		t.Fatalf("expected 2 surviving circles, got %d", len(fg.Circles))
	}
	if len(fg.Bridges) != 1 {
		t.Fatalf("expected 1 surviving bridge, got %d", len(fg.Bridges))
	}
}

func TestProbeGroupsForTileNoOverlap(t *testing.T) {
	tile := tileBounds{MinX: 0, MinY: 0, MaxX: 0.1, MaxY: 0.1}
	groups := []Group{mustGroup([]Circle{{X: 0.9, Y: 0.9, Radius: 0.01}}, nil)}

	any, _, _, _, _ := probeGroupsForTile(poolsForGroups(groups), groups, tile)
	if any {
		t.Fatalf("expected no overlap")
	}

	filtered, release := materializeGroupsForTile(poolsForGroups(groups), groups, tile)
	defer release()
	if len(filtered) != 0 {
		t.Fatalf("expected 0 filtered groups, got %d", len(filtered))
	}
}

// TestFilterCirclesOverlapMixedLarge exercises filterCirclesOverlap (the
// build-tag-gated scalar/SIMD implementation) with more circles than a
// single SIMD vector lane width and a mix of included/excluded circles, to
// catch tail-handling and masking bugs.
func TestFilterCirclesOverlapMixedLarge(t *testing.T) {
	tile := tileBounds{MinX: 0, MinY: 0, MaxX: 0.5, MaxY: 0.5}

	const n = 37 // deliberately not a multiple of any lane width
	circles := make([]Circle, n)
	wantIncluded := bitset.New(n)
	for i := range circles {
		if i%3 == 0 {
			circles[i] = Circle{X: 0.1, Y: 0.1, Radius: 0.01} // inside tile
			wantIncluded.Set(i)
		} else {
			circles[i] = Circle{X: 0.9, Y: 0.9, Radius: 0.01} // outside tile
		}
	}

	pools := newRendererPools(1, n, 0)
	included := bitset.New(n)
	found := filterCirclesOverlap(pools, circles, tile, included)
	if !found {
		t.Fatalf("expected at least one match")
	}
	for i := range circles {
		if got, want := included.Has(i), wantIncluded.Has(i); got != want {
			t.Errorf("circle %d: got included=%v, want %v", i, got, want)
		}
	}
}

// calls with different-sized groups, guarding against stale pooled buffer
// contents leaking between calls.
func TestFilterPoolReuseAcrossCalls(t *testing.T) {
	tile := tileBounds{MinX: 0, MinY: 0, MaxX: 1, MaxY: 1}

	// One pools object reused across calls with different-sized groups, so
	// the small group sub-slices the big group's pooled buffers.
	pools := newRendererPools(1, 20, 0)

	for range 3 {
		big := mustGroup(make([]Circle, 20), nil)
		for i := range big.Circles {
			big.Circles[i] = Circle{X: 0.5, Y: 0.5, Radius: 0.01}
		}

		filtered, release := materializeGroupsForTile(pools, []Group{big}, tile)
		if len(filtered) != 1 || len(filtered[0].Circles) != 20 {
			t.Fatalf("expected all 20 circles included, got %d groups / %d circles", len(filtered), len(filtered[0].Circles))
		}
		release()

		small := mustGroup([]Circle{{X: 0.5, Y: 0.5, Radius: 0.01}}, nil)
		filtered, release = materializeGroupsForTile(pools, []Group{small}, tile)
		if len(filtered) != 1 || len(filtered[0].Circles) != 1 {
			t.Fatalf("expected 1 circle included, got %d groups / %d circles", len(filtered), len(filtered[0].Circles))
		}
		release()
	}
}
