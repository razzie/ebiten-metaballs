package metaballs

import (
	"image"
	"testing"

	"github.com/razzie/ebiten-metaballs/internal/bitset"
	"github.com/razzie/ebiten-metaballs/internal/pool"
)

func TestCenteredUVBoundsRoundTripNonSquareDestinations(t *testing.T) {
	tests := []struct {
		name          string
		width, height int
	}{
		{name: "wide", width: 1600, height: 900},
		{name: "tall", width: 900, height: 1600},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			xform, wantBounds := NewCenteredUVTransform(test.width, test.height)
			gotBounds := visibleUVBounds(image.Point{}, image.Pt(test.width, test.height), xform)
			if gotBounds != wantBounds {
				t.Fatalf("visibleUVBounds() = %+v, want %+v", gotBounds, wantBounds)
			}

			wantRect := image.Rect(0, 0, test.width, test.height)
			if gotRect := tileToPixelRect(gotBounds, xform); gotRect != wantRect {
				t.Fatalf("tileToPixelRect() = %v, want %v", gotRect, wantRect)
			}

			// Adjacent UV tiles must map to pixel rectangles with exactly the
			// same shared edge; otherwise independently rendered tiles leave a
			// gap or overlap at the boundary.
			midX := (gotBounds.MinX + gotBounds.MaxX) / 2
			left := tileToPixelRect(UVBounds{
				MinX: gotBounds.MinX, MinY: gotBounds.MinY,
				MaxX: midX, MaxY: gotBounds.MaxY,
			}, xform)
			right := tileToPixelRect(UVBounds{
				MinX: midX, MinY: gotBounds.MinY,
				MaxX: gotBounds.MaxX, MaxY: gotBounds.MaxY,
			}, xform)
			if left.Max.X != right.Min.X {
				t.Fatalf("adjacent tiles disagree at x boundary: %v and %v", left, right)
			}

			midY := (gotBounds.MinY + gotBounds.MaxY) / 2
			top := tileToPixelRect(UVBounds{
				MinX: gotBounds.MinX, MinY: gotBounds.MinY,
				MaxX: gotBounds.MaxX, MaxY: midY,
			}, xform)
			bottom := tileToPixelRect(UVBounds{
				MinX: gotBounds.MinX, MinY: midY,
				MaxX: gotBounds.MaxX, MaxY: gotBounds.MaxY,
			}, xform)
			if top.Max.Y != bottom.Min.Y {
				t.Fatalf("adjacent tiles disagree at y boundary: %v and %v", top, bottom)
			}
		})
	}
}

func TestUVBoundsRoundTripWithPixelAndUVOffsets(t *testing.T) {
	var xform UVTransform
	xform.SetScale(0.002, 0.004)
	xform.SetOffset(-0.3, 0.2)

	origin := image.Pt(25, 40)
	size := image.Pt(300, 100)
	bounds := visibleUVBounds(origin, size, xform)
	wantBounds := UVBounds{
		MinX: -0.25,
		MinY: 0.36,
		MaxX: 0.35,
		MaxY: 0.76,
	}
	const epsilon = 1e-6
	if abs32(bounds.MinX-wantBounds.MinX) > epsilon ||
		abs32(bounds.MinY-wantBounds.MinY) > epsilon ||
		abs32(bounds.MaxX-wantBounds.MaxX) > epsilon ||
		abs32(bounds.MaxY-wantBounds.MaxY) > epsilon {
		t.Fatalf("visibleUVBounds() = %+v, want %+v", bounds, wantBounds)
	}

	wantRect := image.Rectangle{Min: origin, Max: origin.Add(size)}
	if gotRect := tileToPixelRect(bounds, xform); gotRect != wantRect {
		t.Fatalf("tileToPixelRect() = %v, want %v", gotRect, wantRect)
	}
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

func mustGroup(circles []Circle, bridges []Bridge) Group {
	return Group{Circles: circles, Bridges: bridges}
}

// initPoolsForGroups initializes the given rendererPools to have buffer lengths that exactly
// fit the given groups, for tests that call the filtering/materialization
// functions directly without a Renderer.
func initPoolsForGroups(pools *rendererPools, groups []Group) {
	maxCircles, maxBridges := 0, 0
	for i := range groups {
		maxCircles = max(maxCircles, len(groups[i].Circles))
		maxBridges = max(maxBridges, len(groups[i].Bridges))
	}
	pools.init(len(groups), maxCircles, maxBridges)
}

// testRenderer builds a bare Renderer for tests that only exercise tile
// filtering/materialization methods, skipping NewRenderer's shader compilation.
func testRenderer(groups []Group, smoothK float32) *Renderer {
	r := &Renderer{
		cfg: RendererConfig{Common: ShaderCommonConfig{SmoothK: smoothK}},
	}
	initPoolsForGroups(&r.pools, groups)
	return r
}

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

func TestProbeMaterializeAgree(t *testing.T) {
	tile := UVBounds{MinX: 0, MinY: 0, MaxX: 0.5, MaxY: 0.5}

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

	r := testRenderer(groups, 0)

	_, any, cap, release := r.prepareGroupsForTile(groups, tile)
	defer release()
	if !any {
		t.Fatalf("expected probe to find overlap")
	}

	filtered, release := r.materializeGroupsForTile(groups, tile)
	defer release()

	if len(filtered) != 1 {
		t.Fatalf("expected 1 filtered group, got %d", len(filtered))
	}
	fg := filtered[0]

	if len(fg.Circles) != cap.MainCircles || len(fg.Circles) != cap.OtherCircles {
		t.Errorf("circle count mismatch: probe main=%d other=%d materialize=%d", cap.MainCircles, cap.OtherCircles, len(fg.Circles))
	}
	if len(fg.Bridges) != cap.MainBridges || len(fg.Bridges) != cap.OtherBridges {
		t.Errorf("bridge count mismatch: probe main=%d other=%d materialize=%d", cap.MainBridges, cap.OtherBridges, len(fg.Bridges))
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
	tile := UVBounds{MinX: 0, MinY: 0, MaxX: 0.1, MaxY: 0.1}
	groups := []Group{mustGroup([]Circle{{X: 0.9, Y: 0.9, Radius: 0.01}}, nil)}
	r := testRenderer(groups, 0)

	_, any, _, release := r.prepareGroupsForTile(groups, tile)
	defer release()
	if any {
		t.Fatalf("expected no overlap")
	}

	filtered, release := r.materializeGroupsForTile(groups, tile)
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
	tile := UVBounds{MinX: 0, MinY: 0, MaxX: 0.5, MaxY: 0.5}

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

	var pools rendererPools
	pools.init(1, n, 0)
	included := bitset.New(n)
	found := filterCirclesOverlap(&pools, circles, tile, included, 0)
	if !found {
		t.Fatalf("expected at least one match")
	}
	for i := range circles {
		if got, want := included.Has(i), wantIncluded.Has(i); got != want {
			t.Errorf("circle %d: got included=%v, want %v", i, got, want)
		}
	}
}

// TestFilterPoolReuseAcrossCalls tests that one Renderer's pools are reused across
// calls with different-sized groups, guarding against stale pooled buffer
// contents leaking between calls.
func TestFilterPoolReuseAcrossCalls(t *testing.T) {
	tile := UVBounds{MinX: 0, MinY: 0, MaxX: 1, MaxY: 1}

	// One Renderer's pools reused across calls with different-sized groups,
	// so the small group sub-slices the big group's pooled buffers.
	r := &Renderer{}
	r.pools.init(1, 20, 0)

	for range 3 {
		big := mustGroup(make([]Circle, 20), nil)
		for i := range big.Circles {
			big.Circles[i] = Circle{X: 0.5, Y: 0.5, Radius: 0.01}
		}

		filtered, release := r.materializeGroupsForTile([]Group{big}, tile)
		if len(filtered) != 1 || len(filtered[0].Circles) != 20 {
			t.Fatalf("expected all 20 circles included, got %d groups / %d circles", len(filtered), len(filtered[0].Circles))
		}
		release()

		small := mustGroup([]Circle{{X: 0.5, Y: 0.5, Radius: 0.01}}, nil)
		filtered, release = r.materializeGroupsForTile([]Group{small}, tile)
		if len(filtered) != 1 || len(filtered[0].Circles) != 1 {
			t.Fatalf("expected 1 circle included, got %d groups / %d circles", len(filtered), len(filtered[0].Circles))
		}
		release()
	}
}
