package metaballs

import (
	"github.com/hajimehoshi/ebiten/v2"
)

// ShaderCapacity sets the fixed-size array capacities baked into the
// generated Kage shader. This is the only part of a shader's config that
// should vary across a Renderer's capacity tiers.
type ShaderCapacity struct {
	MainCircles  int
	MainBridges  int
	OtherCircles int
	OtherBridges int
}

// ShaderCommonConfig holds the common configuration parameters for a shader,
// including smooth-min blending radius, edge shading, and FXAA settings.
type ShaderCommonConfig struct {
	// SmoothK is the smooth-min blending radius for the metaball field. Must be positive.
	SmoothK float32
	// Lighting direction for edge shading, normalized to unit length. (0, 0) disables edge shading.
	LightDirX, LightDirY float32
	// Edge thickness for the edge shading. Needs light direction to be non-zero. Must be positive.
	EdgeThickness float32
	// FxaaEnabled renders all groups to an offscreen buffer and applies FXAA
	// to it in a final pass to dst. See MetaballShader's doc comment for the
	// resulting concurrency constraint.
	FxaaEnabled   bool
	FxaaReduceMin float32
	FxaaReduceMul float32
	FxaaSpanMax   float32
	// Disables FXAA pass at shader level and enables it on the renderer level instead.
	rendererFxaaEnabled bool
}

// ShaderConfig is the full set of compile-time constants for a single
// generated Kage shader.
type ShaderConfig struct {
	ShaderCapacity
	ShaderCommonConfig
}

type Circle struct {
	X, Y   float32
	Radius float32
}

type Bridge struct {
	A, B         int
	MiddleRadius float32
}

type Group struct {
	Circles []Circle
	Bridges []Bridge
	Color   ebiten.ColorScale
}

// NewColorScale builds an ebiten.ColorScale since its fields are unexported.
func NewColorScale(r, g, b, a float32) ebiten.ColorScale {
	var cs ebiten.ColorScale
	cs.SetR(r)
	cs.SetG(g)
	cs.SetB(b)
	cs.SetA(a)
	return cs
}

// combineGroups merges every group except the one at exclude into a single
// Group, offsetting bridge indices to match the concatenated circle slice.
func combineGroups(groups []Group, exclude int) Group {
	var out Group

	for i, g := range groups {
		if i == exclude {
			continue
		}

		offset := len(out.Circles)
		out.Circles = append(out.Circles, g.Circles...)

		for _, b := range g.Bridges {
			out.Bridges = append(out.Bridges, Bridge{
				A:            b.A + offset,
				B:            b.B + offset,
				MiddleRadius: b.MiddleRadius,
			})
		}
	}

	return out
}

// CapacityForGroups derives shader array capacities from a set of groups: main
// capacities cover the largest single group, other capacities cover the sum
// of all groups (a safe upper bound for any combined "other" pass). Bridge
// and other-circle capacities are left at 0 when unused, so the generated
// shader can skip those loops entirely.
func CapacityForGroups(groups []Group) ShaderCapacity {
	var mainCircles, mainBridges, totalCircles, totalBridges int

	for _, g := range groups {
		if len(g.Circles) > mainCircles {
			mainCircles = len(g.Circles)
		}
		if len(g.Bridges) > mainBridges {
			mainBridges = len(g.Bridges)
		}

		totalCircles += len(g.Circles)
		totalBridges += len(g.Bridges)
	}

	return ShaderCapacity{
		MainCircles:  mainCircles,
		MainBridges:  mainBridges,
		OtherCircles: totalCircles,
		OtherBridges: totalBridges,
	}
}

// UVBounds is a uv-space rectangle within the visible uv domain.
type UVBounds struct {
	MinX, MinY, MaxX, MaxY float32
}

func (t UVBounds) Padded(padding float32) UVBounds {
	return UVBounds{
		MinX: t.MinX - padding,
		MinY: t.MinY - padding,
		MaxX: t.MaxX + padding,
		MaxY: t.MaxY + padding,
	}
}

func (t UVBounds) Size() (float32, float32) {
	return t.MaxX - t.MinX, t.MaxY - t.MinY
}

// UVTransform holds the uv scale, its inverse and the offset
type UVTransform struct {
	scale, invScale, offset [2]float32
}

func (t *UVTransform) Scale() (float32, float32) {
	return t.scale[0], t.scale[1]
}

func (t *UVTransform) SetScale(x, y float32) {
	t.scale[0] = x
	t.scale[1] = y
	t.invScale[0] = 1.0 / x
	t.invScale[1] = 1.0 / y
}

func (t *UVTransform) Offset() (float32, float32) {
	return t.offset[0], t.offset[1]
}

func (t *UVTransform) SetOffset(x, y float32) {
	t.offset[0] = x
	t.offset[1] = y
}

func (t *UVTransform) ScreenToUV(sx, sy int) (float32, float32) {
	uvX := float32(sx)*t.scale[0] + t.offset[0]
	uvY := float32(sy)*t.scale[1] + t.offset[1]
	return uvX, uvY
}

// NewCenteredUVTransform generates a UVTransform and corresponding UVBounds for the given pixel dimensions,
// making sure 0.5:0.5 in UV space maps to the center of the pixel dimensions, keeping aspect ratio at 1:1.
//
// Intended transform:
//
//	uv = screenPos * scale + offset
func NewCenteredUVTransform(width, height int) (UVTransform, UVBounds) {
	if width <= 0 || height <= 0 {
		panic("width and height must be positive")
	}

	// The shorter screen dimension spans exactly 1 UV unit.
	size := float32(min(width, height))
	scale := float32(1) / size

	// Screen center must map to UV (0.5, 0.5).
	ox := float32(0.5) - float32(width)*0.5*scale
	oy := float32(0.5) - float32(height)*0.5*scale

	var transform UVTransform
	transform.SetScale(scale, scale)
	transform.SetOffset(ox, oy)

	bounds := UVBounds{
		MinX: ox,
		MinY: oy,
		MaxX: float32(width)*scale + ox,
		MaxY: float32(height)*scale + oy,
	}

	return transform, bounds
}
