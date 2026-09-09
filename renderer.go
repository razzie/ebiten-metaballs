package metaballs

import (
	"fmt"
	"image"
	"image/color"
	"sync/atomic"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"
	"github.com/razzie/ebiten-metaballs/internal/bitset"
	"golang.org/x/sync/errgroup"
)

// RendererConfig configures a Renderer: a pool of shaders for different
// capacity tiers, plus grid-planning parameters for adaptive tiling.
type RendererConfig struct {
	// Common is shared by every tier's generated shader (SmoothK, LightDir,
	// EdgeThickness).
	Common ShaderCommonConfig

	// Tiers must be sorted ascending by capacity.
	Tiers []ShaderCapacity

	// RootCols/RootRows define the initial coarse grid over the visible uv
	// domain (derived per Draw from dst's size multiplied by the uv scale),
	// before any adaptive subdivision.
	RootCols, RootRows int

	// MaxDepth is the maximum number of adaptive subdivisions below the root
	// grid.
	MaxDepth int

	// MinTileSize is the smallest uv-space tile width/height allowed in the
	// visible uv domain; subdivision stops once a child tile would drop below
	// it.
	MinTileSize float32

	// Debug, when true, overlays every tile visited (skipped, subdivided, or
	// drawn) with a semi-transparent outline: black top/left, white
	// bottom/right.
	Debug bool

	// Workers controls how many goroutines split up the root grid's CPU work
	// (probing, subdivision, and group materialization). 0 or 1 (the default)
	// keeps Draw fully serial with no added overhead; when Workers > 1, CPU
	// filtering work runs in parallel across worker goroutines and pushes
	// draw tasks to a channel, while all Ebiten GPU draw calls execute
	// sequentially on the main goroutine.
	Workers int

	// PoolMaxCircles, PoolMaxBridges and PoolMaxGroups optionally hint the
	// initial lengths of the Renderer's scratch buffer pools (max circles
	// per group, max bridges per group, group count). Zero leaves the
	// default to the largest shader tier's capacities; Draw grows the pools
	// automatically when actual input exceeds them (they never shrink).
	PoolMaxCircles, PoolMaxBridges, PoolMaxGroups int
}

// Renderer draws large numbers of circles by tiling the canvas, skipping
// empty tiles, adaptively subdividing crowded ones, and picking the
// smallest shader tier that fits each tile's contents.
//
// Draw is not safe for concurrent use on the same Renderer (the scratch
// pools grow between frames); use one Renderer per goroutine instead.
type Renderer struct {
	cfg     RendererConfig
	shaders []*MetaballShader
	pools   rendererPools
}

// NewRenderer validates cfg and eagerly compiles one shader per tier.
func NewRenderer(cfg RendererConfig) (*Renderer, error) {
	if len(cfg.Tiers) == 0 {
		return nil, fmt.Errorf("renderer config must have at least one tier")
	}
	if cfg.RootCols <= 0 || cfg.RootRows <= 0 {
		return nil, fmt.Errorf("renderer config must have positive RootCols/RootRows")
	}
	if cfg.MinTileSize <= 0 {
		return nil, fmt.Errorf("renderer config must have a positive MinTileSize")
	}

	shaders := make([]*MetaballShader, len(cfg.Tiers))

	for i, tier := range cfg.Tiers {
		if i > 0 {
			prev := cfg.Tiers[i-1]
			if tier.MainCircles < prev.MainCircles || tier.MainBridges < prev.MainBridges ||
				tier.OtherCircles < prev.OtherCircles || tier.OtherBridges < prev.OtherBridges {
				return nil, fmt.Errorf("renderer tiers must be sorted ascending by capacity: tier %d is smaller than tier %d", i, i-1)
			}
		}

		shader, err := NewMetaballShader(ShaderConfig{ShaderCapacity: tier, ShaderCommonConfig: cfg.Common})
		if err != nil {
			return nil, fmt.Errorf("compile renderer tier %d: %w", i, err)
		}
		shaders[i] = shader
	}

	// Seed pool defaults from the largest tier (tiers are sorted ascending):
	// per-group buffers fit MainCircles/MainBridges, and the group count
	// can't meaningfully exceed the smallest "other" capacity (larger group
	// counts get clipped to the tier anyway). Explicit hints override.
	largest := cfg.Tiers[len(cfg.Tiers)-1]
	poolMaxCircles := max(cfg.PoolMaxCircles, largest.MainCircles)
	poolMaxBridges := max(cfg.PoolMaxBridges, largest.MainBridges)
	poolMaxGroups := max(cfg.PoolMaxGroups, min(largest.OtherCircles, largest.OtherBridges))

	r := &Renderer{
		cfg:     cfg,
		shaders: shaders,
	}
	r.pools.init(poolMaxGroups, poolMaxCircles, poolMaxBridges)
	return r, nil
}

// tileBounds is a uv-space rectangle within the visible uv domain.
type tileBounds struct {
	MinX, MinY, MaxX, MaxY float32
}

func (t tileBounds) padded(padding float32) tileBounds {
	return tileBounds{
		MinX: t.MinX - padding,
		MinY: t.MinY - padding,
		MaxX: t.MaxX + padding,
		MaxY: t.MaxY + padding,
	}
}

func (t tileBounds) size() (float32, float32) {
	return t.MaxX - t.MinX, t.MaxY - t.MinY
}

// Stats reports how much work the last Draw call actually performed.
type Stats struct {
	TilesDrawn     atomic.Int32
	TilesSkipped   atomic.Int32
	CirclesClipped atomic.Int32
}

// Draw renders groups by planning a tile grid over dst and drawing only the
// tiles that contain circles, subdividing crowded tiles as needed. The uv
// space is normalized against dst's own size (uv scale = 1/size), i.e. the
// canvas covers [0,1]x[0,1] in uv space; on non-square canvases circles
// stretch with the aspect ratio - use DrawScaled to avoid that.
func (r *Renderer) Draw(dst *ebiten.Image, groups []Group) (*Stats, error) {
	w, h := dst.Bounds().Dx(), dst.Bounds().Dy()
	return r.DrawScaled(dst, groups, [2]float32{1 / float32(w), 1 / float32(h)})
}

// DrawScaled is like Draw, but with an explicit uv scale (uv units per
// pixel) instead of dst's own reciprocal size. The visible uv domain becomes
// [0, width*scaleX] x [0, height*scaleY] and the tile grid is planned over
// it. A uniform scale keeps circles circular regardless of aspect ratio:
// e.g. {1/h, 1/h} maps the canvas to [0, w/h]x[0, 1] in uv space. Both scale
// components must be positive.
func (r *Renderer) DrawScaled(dst *ebiten.Image, groups []Group, uvScale [2]float32) (*Stats, error) {
	if uvScale[0] <= 0 || uvScale[1] <= 0 {
		return nil, fmt.Errorf("renderer: uv scale must be positive: %v", uvScale)
	}

	xform := uvTransform{
		scale:    uvScale,
		invScale: [2]float32{1 / uvScale[0], 1 / uvScale[1]},
	}

	maxCircles, maxBridges := 0, 0
	for i := range groups {
		maxCircles = max(maxCircles, len(groups[i].Circles))
		maxBridges = max(maxBridges, len(groups[i].Bridges))
	}

	// Grow the scratch pools (never shrinks) so every pool get below returns
	// buffers big enough for this frame's groups, before any tile work and
	// before drawParallel spawns its workers.
	r.pools.ensure(len(groups), maxCircles, maxBridges)

	w, h := dst.Bounds().Dx(), dst.Bounds().Dy()

	// The visible uv domain covered by dst.
	domainW := float32(w) * uvScale[0]
	domainH := float32(h) * uvScale[1]

	dw := domainW / float32(r.cfg.RootCols)
	dh := domainH / float32(r.cfg.RootRows)

	rootTile := func(idx int) tileBounds {
		col := idx % r.cfg.RootCols
		row := idx / r.cfg.RootCols
		return tileBounds{
			MinX: float32(col) * dw,
			MinY: float32(row) * dh,
			MaxX: float32(col+1) * dw,
			MaxY: float32(row+1) * dh,
		}
	}

	total := r.cfg.RootCols * r.cfg.RootRows

	if r.cfg.Workers > 1 {
		var (
			workers = r.cfg.Workers
			stats   Stats
			g       errgroup.Group
		)
		/*if workers > total {
			workers = total
		}*/
		sem := make(chan struct{}, workers)
		for idx := 0; idx < total; idx++ {
			g.Go(func() error {
				return r.drawTile(dst, xform, groups, rootTile(idx), 0, &stats, sem)
			})
		}
		return &stats, g.Wait()
	} else {
		var stats Stats
		for idx := 0; idx < total; idx++ {
			if err := r.drawTile(dst, xform, groups, rootTile(idx), 0, &stats, nil); err != nil {
				return &stats, err
			}
		}
		return &stats, nil
	}
}

// uvTransform holds the uv scale (uv units per pixel) and its inverse
// (pixels per uv unit), the latter precomputed once per Draw so that
// uv-to-pixel conversions stay multiplications.
type uvTransform struct {
	scale, invScale [2]float32
}

type tileDrawTask struct {
	tile           tileBounds
	pixelRect      image.Rectangle
	tierIdx        int
	filtered       []Group
	release        func()
	circlesClipped int
	hasDraw        bool
	hasDebug       bool
}

func (r *Renderer) drawTile(
	dst *ebiten.Image,
	xform uvTransform,
	groups []Group,
	tile tileBounds,
	depth int,
	stats *Stats,
	sem chan struct{},
) error {
	if sem != nil {
		sem <- struct{}{}
		defer func() { <-sem }()
	}

	// Cheap counting pass: decides tier/subdivision without materializing
	// any filtered circle/bridge data, since that's thrown away whenever
	// the tile ends up subdividing instead of drawing.
	prep, any, cap, prepRelease := r.prepareGroupsForTile(groups, tile)

	if !any {
		prepRelease()
		stats.TilesSkipped.Add(1)
		return nil
	}

	if r.cfg.Debug {
		// Deferred so it draws last, on top of this tile's content and any children's.
		defer r.drawDebugOutline(dst, xform, tile)
	}

	tierIdx := r.pickTier(cap)

	if tierIdx < 0 {
		tw, th := tile.size()

		if depth < r.cfg.MaxDepth && tw > 2*r.cfg.MinTileSize && th > 2*r.cfg.MinTileSize {
			prepRelease()

			midX := (tile.MinX + tile.MaxX) / 2
			midY := (tile.MinY + tile.MaxY) / 2

			children := [4]tileBounds{
				{tile.MinX, tile.MinY, midX, midY},
				{midX, tile.MinY, tile.MaxX, midY},
				{tile.MinX, midY, midX, tile.MaxY},
				{midX, midY, tile.MaxX, tile.MaxY},
			}

			for _, child := range children {
				if err := r.drawTile(dst, xform, groups, child, depth+1, stats, sem); err != nil {
					return err
				}
			}

			return nil
		}
	}

	// Now actually drawing this tile (either a tier fit, or a final
	// fallback): materialize the filtered circle/bridge data from the
	// bitsets prepareGroupsForTile already computed above.
	filtered, matRelease := materializeGroupsFromPrep(&r.pools, groups, prep)
	defer prepRelease()
	defer matRelease()

	if tierIdx < 0 {
		// Can't subdivide further: fall back to the largest tier and clip.
		tierIdx = len(r.shaders) - 1
		clipped := clipGroupsToTier(filtered, r.cfg.Tiers[tierIdx])
		stats.CirclesClipped.Add(int32(clipped))
	}

	pixelRect := tileToPixelRect(tile, xform.invScale)
	if pixelRect.Dx() <= 0 || pixelRect.Dy() <= 0 {
		return nil
	}

	sub, ok := dst.SubImage(pixelRect).(*ebiten.Image)
	if !ok {
		return fmt.Errorf("renderer: SubImage did not return *ebiten.Image")
	}

	origin := [2]float32{float32(pixelRect.Min.X), float32(pixelRect.Min.Y)}
	if err := r.shaders[tierIdx].DrawScaledAt(sub, filtered, xform.scale, origin); err != nil {
		return err
	}

	stats.TilesDrawn.Add(1)
	return nil
}

func (r *Renderer) pickTier(cap ShaderCapacity) int {
	for i, tier := range r.cfg.Tiers {
		if cap.MainCircles <= tier.MainCircles && cap.MainBridges <= tier.MainBridges &&
			cap.OtherCircles <= tier.OtherCircles && cap.OtherBridges <= tier.OtherBridges {
			return i
		}
	}
	return -1
}

var (
	debugTopLeftColor     = color.NRGBA{R: 64, G: 64, B: 64, A: 128}
	debugBottomRightColor = color.NRGBA{R: 192, G: 192, B: 192, A: 128}
)

// drawDebugOutline draws a 1px outline around tile: black top/left edges,
// white bottom/right edges, both at ~0.5 alpha.
func (r *Renderer) drawDebugOutline(dst *ebiten.Image, xform uvTransform, tile tileBounds) {
	pixelRect := tileToPixelRect(tile, xform.invScale)
	if pixelRect.Dx() <= 0 || pixelRect.Dy() <= 0 {
		return
	}

	minX, minY := float32(pixelRect.Min.X), float32(pixelRect.Min.Y)
	maxX, maxY := float32(pixelRect.Max.X), float32(pixelRect.Max.Y)

	vector.StrokeLine(dst, minX, minY, maxX, minY, 1, debugTopLeftColor, false)
	vector.StrokeLine(dst, minX, minY, minX, maxY, 1, debugTopLeftColor, false)
	vector.StrokeLine(dst, minX, maxY, maxX, maxY, 1, debugBottomRightColor, false)
	vector.StrokeLine(dst, maxX, minY, maxX, maxY, 1, debugBottomRightColor, false)
}

// tileToPixelRect converts a uv-space tile to pixel coordinates; invScale
// is the inverse uv scale (pixels per uv unit).
func tileToPixelRect(tile tileBounds, invScale [2]float32) image.Rectangle {
	return image.Rect(
		int(tile.MinX*invScale[0]+0.5),
		int(tile.MinY*invScale[1]+0.5),
		int(tile.MaxX*invScale[0]+0.5),
		int(tile.MaxY*invScale[1]+0.5),
	)
}

// circleOverlapsTile reports whether a circle's AABB overlaps the tile,
// padded by smoothK since smooth-min blending bulges the rendered shape
// beyond the nominal radius.
func circleOverlapsTile(c Circle, tile tileBounds, smoothK float32) bool {
	r := c.Radius + smoothK
	return c.X+r >= tile.MinX && c.X-r <= tile.MaxX &&
		c.Y+r >= tile.MinY && c.Y-r <= tile.MaxY
}

// segmentIntersectsRect reports whether the segment from (x1,y1) to (x2,y2)
// intersects rect, via Liang-Barsky clipping.
func segmentIntersectsRect(x1, y1, x2, y2 float32, rect tileBounds) bool {
	dx, dy := x2-x1, y2-y1
	tMin, tMax := float32(0), float32(1)

	clip := func(p, q float32) bool {
		if p == 0 {
			return q >= 0 // parallel to this axis: reject if outside on that side
		}
		t := q / p
		if p < 0 {
			if t > tMax {
				return false
			}
			if t > tMin {
				tMin = t
			}
		} else {
			if t < tMin {
				return false
			}
			if t < tMax {
				tMax = t
			}
		}
		return true
	}

	return clip(-dx, x1-rect.MinX) &&
		clip(dx, rect.MaxX-x1) &&
		clip(-dy, y1-rect.MinY) &&
		clip(dy, rect.MaxY-y1)
}

// prepareGroupsForTile computes each group's included-circle bitset for
// tile exactly once (via computeIncludedSet), along with the aggregate
// main/other counts (mirroring ConfigForGroups' semantics: main = largest
// single group, other = sum of all groups) used to pick a shader tier.
// The returned prep is later reused by materializeGroupsFromPrep when the
// tile ends up drawn, instead of recomputing the same bitset; if the tile
// subdivides instead, the caller just calls release without materializing.
func (r *Renderer) prepareGroupsForTile(groups []Group, tile tileBounds) (prep []groupPrep, any bool, cap ShaderCapacity, release func()) {
	prepPtr := r.pools.preps.get()
	p := (*prepPtr)[:len(groups)]

	for i := range groups {
		g := &groups[i]
		if len(g.Circles) == 0 {
			continue
		}

		included := &p[i].included
		if !computeIncludedSet(&r.pools, g, tile, included, r.cfg.Common.SmoothK) {
			continue
		}

		circleCount := included.Count()
		bridgeCount := 0
		for _, b := range g.Bridges {
			if included.Has(b.A) && included.Has(b.B) {
				bridgeCount++
			}
		}

		p[i].circleCount = circleCount
		p[i].bridgeCount = bridgeCount

		any = true
		if circleCount > cap.MainCircles {
			cap.MainCircles = circleCount
		}
		if bridgeCount > cap.MainBridges {
			cap.MainBridges = bridgeCount
		}
		cap.OtherCircles += circleCount
		cap.OtherBridges += bridgeCount
	}

	release = func() {
		r.pools.preps.put(prepPtr)
	}

	return p, any, cap, release
}

// computeIncludedSet marks included[i] true for each of g's circles
// overlapping tile (padded by each circle's own radius) plus the endpoints
// of any bridge whose segment crosses tile (padded by that bridge's own
// MiddleRadius, since its rendered shape bulges by up to that much off its
// segment). Reports whether anything was included. pools supplies scratch
// buffers for filterCirclesOverlap.
func computeIncludedSet(pools *rendererPools, g *Group, tile tileBounds, included *bitset.BitSet, smoothK float32) bool {
	found := filterCirclesOverlap(pools, g.Circles, tile, included, smoothK)

	for _, b := range g.Bridges {
		a, c := g.Circles[b.A], g.Circles[b.B]
		bridgeTile := tile.padded(b.MiddleRadius + smoothK)
		if segmentIntersectsRect(a.X, a.Y, c.X, c.Y, bridgeTile) {
			included.Set(b.A)
			included.Set(b.B)
			found = true
		}
	}

	return found
}

// materializeGroupsForTile is a convenience wrapper for callers (tests,
// non-hot-path code) that don't already have a prepareGroupsForTile
// result; the Draw hot path uses materializeGroupsFromPrep directly to
// avoid recomputing the included bitsets it already has.
func (r *Renderer) materializeGroupsForTile(groups []Group, tile tileBounds) (filtered []Group, release func()) {
	prep, _, _, prepRelease := r.prepareGroupsForTile(groups, tile)
	filtered, matRelease := materializeGroupsFromPrep(&r.pools, groups, prep)
	return filtered, func() {
		matRelease()
		prepRelease()
	}
}

// materializeGroupsFromPrep builds the actual filtered subset of
// groups/circles/bridges from bitsets already computed by
// prepareGroupsForTile, remapping bridge endpoints accordingly. All
// backing storage comes from pools; the caller must invoke the returned
// release func (typically via defer) once it's done using the result, e.g.
// right after the draw call that consumes it.
func materializeGroupsFromPrep(pools *rendererPools, groups []Group, prep []groupPrep) (filtered []Group, release func()) {
	groupsPtr := pools.groups.Get()
	*groupsPtr = (*groupsPtr)[:0]

	materializeBuffersPtr := pools.materialize.Get()
	*materializeBuffersPtr = (*materializeBuffersPtr)[:0]

	remapPtr := pools.ints.Get()
	defer pools.ints.Put(remapPtr)

	for i := range groups {
		if !prep[i].included.Any() {
			continue
		}

		buffers := materializeBuffers{
			circlesPtr: pools.circles.Get(),
			bridgesPtr: pools.bridges.Get(),
		}
		filteredGroup := materializeGroupFromIncluded(remapPtr, buffers.circlesPtr, buffers.bridgesPtr, &groups[i], &prep[i].included)
		*groupsPtr = append(*groupsPtr, filteredGroup)
		*materializeBuffersPtr = append(*materializeBuffersPtr, buffers)
	}

	return *groupsPtr, func() {
		for _, r := range *materializeBuffersPtr {
			pools.circles.Put(r.circlesPtr)
			pools.bridges.Put(r.bridgesPtr)
		}
		pools.groups.Put(groupsPtr)
		pools.materialize.Put(materializeBuffersPtr)
	}
}

// materializeGroupFromIncluded filters g's circles down to those already
// marked in included (computed by prepareGroupsForTile via
// computeIncludedSet) plus the remapped bridges whose endpoints both
// survived.
func materializeGroupFromIncluded(remapPtr *[]int, circlesPtr *[]Circle, bridgesPtr *[]Bridge, g *Group, included *bitset.BitSet) Group {
	remap := (*remapPtr)[:len(g.Circles)]

	circles := (*circlesPtr)[:0]
	for i := range g.Circles {
		if included.Has(i) {
			remap[i] = len(circles) + 1 // +1 so the zero value means "dropped"
			circles = append(circles, g.Circles[i])
		} else {
			remap[i] = 0
		}
	}
	*circlesPtr = circles

	bridges := (*bridgesPtr)[:0]
	for _, b := range g.Bridges {
		na, nb := remap[b.A], remap[b.B]
		if na == 0 || nb == 0 {
			continue
		}
		bridges = append(bridges, Bridge{A: na - 1, B: nb - 1, MiddleRadius: b.MiddleRadius})
	}
	*bridgesPtr = bridges

	return Group{Circles: circles, Bridges: bridges, Color: g.Color}
}

// clipGroupsToTier truncates each group's circles/bridges in place to fit
// tier's capacity (per-group cap for "main", running total cap for
// "other"), returning the number of circles dropped. This is a last resort
// for tiles too dense to subdivide further.
func clipGroupsToTier(groups []Group, tier ShaderCapacity) int {
	dropped := 0
	remainingCircles := tier.OtherCircles
	remainingBridges := tier.OtherBridges

	for i := range groups {
		g := &groups[i]

		circleCap := min(tier.MainCircles, max(remainingCircles, 0))
		if len(g.Circles) > circleCap {
			dropped += len(g.Circles) - circleCap
			g.Circles = g.Circles[:circleCap]
		}
		remainingCircles -= len(g.Circles)

		bridgeCap := min(tier.MainBridges, max(remainingBridges, 0))
		if len(g.Bridges) > bridgeCap {
			g.Bridges = g.Bridges[:bridgeCap]
		}
		remainingBridges -= len(g.Bridges)
	}

	return dropped
}
