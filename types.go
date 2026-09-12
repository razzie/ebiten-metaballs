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
