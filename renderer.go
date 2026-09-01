package metaballs

import (
	"fmt"
	"image"
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"
)

// RendererConfig configures a Renderer: a pool of shaders for different
// capacity tiers, plus grid-planning parameters for adaptive tiling.
type RendererConfig struct {
	// Tiers must be sorted ascending by capacity. All tiers must share the
	// same SmoothK, LightDirX, LightDirY and EdgeThickness.
	Tiers []ShaderConfig

	// RootCols/RootRows define the initial coarse grid over the [0,1]x[0,1]
	// uv space, before any adaptive subdivision.
	RootCols, RootRows int

	// MaxDepth is the maximum number of adaptive subdivisions below the root
	// grid.
	MaxDepth int

	// MinTileSize is the smallest uv-space tile width/height allowed;
	// subdivision stops once a child tile would drop below it.
	MinTileSize float32

	// Padding is the uv-space margin added around a tile's bounds when
	// collecting circles/bridges, so metaball blending (whose range is
	// roughly SmoothK) stays correct across tile borders. Should be at
	// least ~2x the shared SmoothK.
	Padding float32

	// Debug, when true, overlays every tile visited (skipped, subdivided, or
	// drawn) with a semi-transparent outline: black top/left, white
	// bottom/right.
	Debug bool
}

// Renderer draws large numbers of circles by tiling the canvas, skipping
// empty tiles, adaptively subdividing crowded ones, and picking the
// smallest shader tier that fits each tile's contents.
type Renderer struct {
	cfg     RendererConfig
	shaders []*MetaballShader
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

	first := cfg.Tiers[0]
	shaders := make([]*MetaballShader, len(cfg.Tiers))

	for i, tier := range cfg.Tiers {
		if tier.SmoothK != first.SmoothK ||
			tier.LightDirX != first.LightDirX ||
			tier.LightDirY != first.LightDirY ||
			tier.EdgeThickness != first.EdgeThickness {
			return nil, fmt.Errorf("renderer tiers must share the same SmoothK/LightDir/EdgeThickness: tier %d differs from tier 0", i)
		}
		if i > 0 {
			prev := cfg.Tiers[i-1]
			if tier.MainCircles < prev.MainCircles || tier.MainBridges < prev.MainBridges ||
				tier.OtherCircles < prev.OtherCircles || tier.OtherBridges < prev.OtherBridges {
				return nil, fmt.Errorf("renderer tiers must be sorted ascending by capacity: tier %d is smaller than tier %d", i, i-1)
			}
		}

		shader, err := NewMetaballShader(tier)
		if err != nil {
			return nil, fmt.Errorf("compile renderer tier %d: %w", i, err)
		}
		shaders[i] = shader
	}

	return &Renderer{cfg: cfg, shaders: shaders}, nil
}

// tileBounds is a uv-space rectangle within [0,1]x[0,1].
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
	TilesDrawn     int
	TilesSkipped   int
	CirclesClipped int
}

// Draw renders groups by planning a tile grid over dst and drawing only the
// tiles that contain circles, subdividing crowded tiles as needed.
func (r *Renderer) Draw(dst *ebiten.Image, groups []Group) (Stats, error) {
	var stats Stats

	w, h := dst.Bounds().Dx(), dst.Bounds().Dy()
	resolution := [2]float32{float32(w), float32(h)}

	dw := 1.0 / float32(r.cfg.RootCols)
	dh := 1.0 / float32(r.cfg.RootRows)

	for row := 0; row < r.cfg.RootRows; row++ {
		for col := 0; col < r.cfg.RootCols; col++ {
			tile := tileBounds{
				MinX: float32(col) * dw,
				MinY: float32(row) * dh,
				MaxX: float32(col+1) * dw,
				MaxY: float32(row+1) * dh,
			}

			if err := r.drawTile(dst, resolution, groups, tile, 0, &stats); err != nil {
				return stats, err
			}
		}
	}

	return stats, nil
}

func (r *Renderer) drawTile(
	dst *ebiten.Image,
	resolution [2]float32,
	groups []Group,
	tile tileBounds,
	depth int,
	stats *Stats,
) error {
	if r.cfg.Debug {
		// Deferred so it draws last, on top of this tile's content and any children's.
		defer r.drawDebugOutline(dst, resolution, tile)
	}

	padded := tile.padded(r.cfg.Padding)
	filtered, mainCircles, mainBridges, otherCircles, otherBridges := filterGroupsForTile(groups, padded)

	if len(filtered) == 0 {
		stats.TilesSkipped++
		return nil
	}

	tierIdx := r.pickTier(mainCircles, mainBridges, otherCircles, otherBridges)

	if tierIdx < 0 {
		tw, th := tile.size()

		if depth < r.cfg.MaxDepth && tw > 2*r.cfg.MinTileSize && th > 2*r.cfg.MinTileSize {
			midX := (tile.MinX + tile.MaxX) / 2
			midY := (tile.MinY + tile.MaxY) / 2

			children := [4]tileBounds{
				{tile.MinX, tile.MinY, midX, midY},
				{midX, tile.MinY, tile.MaxX, midY},
				{tile.MinX, midY, midX, tile.MaxY},
				{midX, midY, tile.MaxX, tile.MaxY},
			}

			for _, child := range children {
				if err := r.drawTile(dst, resolution, groups, child, depth+1, stats); err != nil {
					return err
				}
			}

			return nil
		}

		// Can't subdivide further: fall back to the largest tier and clip.
		tierIdx = len(r.shaders) - 1
		clipped := clipGroupsToTier(filtered, r.cfg.Tiers[tierIdx])
		stats.CirclesClipped += clipped
	}

	pixelRect := tileToPixelRect(tile, resolution)
	if pixelRect.Dx() <= 0 || pixelRect.Dy() <= 0 {
		return nil
	}

	sub, ok := dst.SubImage(pixelRect).(*ebiten.Image)
	if !ok {
		return fmt.Errorf("renderer: SubImage did not return *ebiten.Image")
	}

	origin := [2]float32{float32(pixelRect.Min.X), float32(pixelRect.Min.Y)}
	if err := r.shaders[tierIdx].DrawRegionAt(sub, filtered, resolution, origin); err != nil {
		return err
	}

	stats.TilesDrawn++
	return nil
}

func (r *Renderer) pickTier(mainCircles, mainBridges, otherCircles, otherBridges int) int {
	for i, tier := range r.cfg.Tiers {
		if mainCircles <= tier.MainCircles && mainBridges <= tier.MainBridges &&
			otherCircles <= tier.OtherCircles && otherBridges <= tier.OtherBridges {
			return i
		}
	}
	return -1
}

var (
	debugTopLeftColor     = color.NRGBA{A: 128}
	debugBottomRightColor = color.NRGBA{R: 255, G: 255, B: 255, A: 128}
)

// drawDebugOutline draws a 1px outline around tile: black top/left edges,
// white bottom/right edges, both at ~0.5 alpha.
func (r *Renderer) drawDebugOutline(dst *ebiten.Image, resolution [2]float32, tile tileBounds) {
	pixelRect := tileToPixelRect(tile, resolution)
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

func tileToPixelRect(tile tileBounds, resolution [2]float32) image.Rectangle {
	return image.Rect(
		int(tile.MinX*resolution[0]+0.5),
		int(tile.MinY*resolution[1]+0.5),
		int(tile.MaxX*resolution[0]+0.5),
		int(tile.MaxY*resolution[1]+0.5),
	)
}

// circleOverlapsTile reports whether a circle's AABB overlaps the tile.
func circleOverlapsTile(c Circle, tile tileBounds) bool {
	return c.X+c.Radius >= tile.MinX && c.X-c.Radius <= tile.MaxX &&
		c.Y+c.Radius >= tile.MinY && c.Y-c.Radius <= tile.MaxY
}

// filterGroupsForTile returns the subset of groups/circles/bridges that
// overlap tile, along with the aggregate main/other counts (mirroring
// ConfigForGroups' semantics: main = largest single group, other = sum of
// all groups) used to pick a shader tier.
func filterGroupsForTile(groups []Group, tile tileBounds) (filtered []Group, mainCircles, mainBridges, otherCircles, otherBridges int) {
	for _, g := range groups {
		fg, ok := filterGroup(g, tile)
		if !ok {
			continue
		}

		filtered = append(filtered, fg)

		if len(fg.Circles) > mainCircles {
			mainCircles = len(fg.Circles)
		}
		if len(fg.Bridges) > mainBridges {
			mainBridges = len(fg.Bridges)
		}
		otherCircles += len(fg.Circles)
		otherBridges += len(fg.Bridges)
	}

	return filtered, mainCircles, mainBridges, otherCircles, otherBridges
}

// filterGroup filters g's circles down to those overlapping tile plus any
// circle that is the far end of a bridge from an included circle (even if
// off-tile, otherwise the bridge would pop in/out at tile borders),
// remapping bridge endpoints accordingly. ok is false when no circles
// survive.
func filterGroup(g Group, tile tileBounds) (Group, bool) {
	included := make([]bool, len(g.Circles))
	found := false
	for i, c := range g.Circles {
		if circleOverlapsTile(c, tile) {
			included[i] = true
			found = true
		}
	}

	if !found {
		return Group{}, false
	}

	// Propagate inclusion across bridge chains until it stops spreading.
	for changed := true; changed; {
		changed = false
		for _, b := range g.Bridges {
			if included[b.A] != included[b.B] {
				included[b.A] = true
				included[b.B] = true
				changed = true
			}
		}
	}

	remap := make([]int, len(g.Circles))
	var circles []Circle
	for i, inc := range included {
		if inc {
			remap[i] = len(circles) + 1 // +1 so the zero value means "dropped"
			circles = append(circles, g.Circles[i])
		}
	}

	var bridges []Bridge
	for _, b := range g.Bridges {
		na, nb := remap[b.A], remap[b.B]
		if na == 0 || nb == 0 {
			continue
		}
		bridges = append(bridges, Bridge{A: na - 1, B: nb - 1, MiddleRadius: b.MiddleRadius})
	}

	return Group{Circles: circles, Bridges: bridges, Color: g.Color}, true
}

// clipGroupsToTier truncates each group's circles/bridges in place to fit
// tier's capacity (per-group cap for "main", running total cap for
// "other"), returning the number of circles dropped. This is a last resort
// for tiles too dense to subdivide further.
func clipGroupsToTier(groups []Group, tier ShaderConfig) int {
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
