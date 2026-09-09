package metaballs

import (
	"bytes"
	"embed"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"text/template"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/razzie/ebiten-metaballs/internal/pool"
)

//go:embed *.kage.tmpl
var kageFS embed.FS

var kageTemplates = template.Must(template.New("").Funcs(template.FuncMap{
	"float": formatKageFloat,
	"vec2":  formatKageVec2,
}).ParseFS(kageFS, "*.kage.tmpl"))

const (
	basicShaderTemplate = "shader_basic.kage.tmpl"
	edgeShaderTemplate  = "shader_edge.kage.tmpl"
)

// formatKageFloat renders f as a Kage float literal, which requires a decimal point.
func formatKageFloat(f float32) string {
	s := strconv.FormatFloat(float64(f), 'f', -1, 32)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

// formatKageVec2 renders x, y as a Kage vec2(...) literal.
func formatKageVec2(x, y float32) string {
	return fmt.Sprintf("vec2(%s, %s)", formatKageFloat(x), formatKageFloat(y))
}

// ShaderCapacity sets the fixed-size array capacities baked into the
// generated Kage shader. This is the only part of a shader's config that
// should vary across a Renderer's capacity tiers.
type ShaderCapacity struct {
	MainCircles  int
	MainBridges  int
	OtherCircles int
	OtherBridges int
}

// ShaderCommonConfig sets the smooth-min blending radius and edge shading,
// shared by every capacity tier of a Renderer. LightDirX/LightDirY of
// (0, 0) disables edge shading entirely (no gradients computed).
type ShaderCommonConfig struct {
	SmoothK       float32
	LightDirX     float32
	LightDirY     float32
	EdgeThickness float32
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

// shaderPools recycles the packed-uniform float32 buffers a MetaballShader
// builds every drawPass, sized once from the shader's fixed ShaderCapacity.
type shaderPools struct {
	mainCircles      pool.SlicePool[float32]
	otherCircles     pool.SlicePool[float32]
	mainBridgeEnds   pool.SlicePool[float32]
	mainBridgeRadii  pool.SlicePool[float32]
	otherBridgeEnds  pool.SlicePool[float32]
	otherBridgeRadii pool.SlicePool[float32]
	uniforms         sync.Pool // map[string]any
}

func (p *shaderPools) init(capacity ShaderCapacity) {
	p.mainCircles.Init(capacity.MainCircles * 4)
	p.otherCircles.Init(capacity.OtherCircles * 4)
	p.mainBridgeEnds.Init(capacity.MainBridges * 4)
	p.mainBridgeRadii.Init(capacity.MainBridges * 3)
	p.otherBridgeEnds.Init(capacity.OtherBridges * 4)
	p.otherBridgeRadii.Init(capacity.OtherBridges * 3)
	p.uniforms.New = func() any {
		return make(map[string]any)
	}
}

type MetaballShader struct {
	shader *ebiten.Shader
	config ShaderConfig
	pools  shaderPools
}

func NewMetaballShader(config ShaderConfig) (*MetaballShader, error) {
	if config.MainCircles <= 0 || config.MainBridges < 0 ||
		config.OtherCircles < 0 || config.OtherBridges < 0 || config.SmoothK <= 0 {
		return nil, fmt.Errorf("invalid shader config: %+v", config)
	}

	edgeShadingEnabled := false
	data := config

	if length := math.Hypot(float64(config.LightDirX), float64(config.LightDirY)); length > 0 {
		edgeShadingEnabled = true
		data.LightDirX = float32(float64(config.LightDirX) / length)
		data.LightDirY = float32(float64(config.LightDirY) / length)

		if config.EdgeThickness <= 0 {
			return nil, fmt.Errorf("invalid shader config: EdgeThickness must be positive when edge shading is enabled: %+v", config)
		}
	}

	templateName := basicShaderTemplate
	if edgeShadingEnabled {
		templateName = edgeShaderTemplate
	}

	var src bytes.Buffer
	if err := kageTemplates.ExecuteTemplate(&src, templateName, data); err != nil {
		return nil, fmt.Errorf("generate metaball shader source: %w", err)
	}

	shader, err := ebiten.NewShader(src.Bytes())
	if err != nil {
		return nil, fmt.Errorf("compile metaball shader: %w", err)
	}

	ms := &MetaballShader{shader: shader, config: config}
	ms.pools.init(config.ShaderCapacity)
	return ms, nil
}

// packCircles packs circles into vec4(x, y, radius, unused) entries using a
// pooled buffer, padded with zero entries up to p's default length so the
// uniform array length matches the shader. release must be called once the
// returned slice is no longer needed.
func (s *MetaballShader) packCircles(out *[]float32, circles []Circle) {
	for i, c := range circles {
		(*out)[i*4], (*out)[i*4+1], (*out)[i*4+2] = c.X, c.Y, c.Radius
	}
}

// packBridges packs bridges into vec4(a.x, a.y, b.x, b.y) ends and
// vec3(radiusA, radiusMiddle, radiusB) radii, using pooled buffers padded up
// to endsPool/radiiPool's default length. release is non-nil (and must
// still be called) even when err is returned.
func (s *MetaballShader) packBridges(ends, radii *[]float32, circles []Circle, bridges []Bridge) (err error) {
	for i, br := range bridges {
		if br.A < 0 || br.A >= len(circles) ||
			br.B < 0 || br.B >= len(circles) {
			return fmt.Errorf("invalid bridge indices: %d -> %d", br.A, br.B)
		}

		a := circles[br.A]
		b := circles[br.B]

		(*ends)[i*4], (*ends)[i*4+1], (*ends)[i*4+2], (*ends)[i*4+3] = a.X, a.Y, b.X, b.Y
		(*radii)[i*3], (*radii)[i*3+1], (*radii)[i*3+2] = a.Radius/2, br.MiddleRadius, b.Radius/2
	}

	return nil
}

// Draw renders each group in its own pass, using the remaining groups
// combined as the "other" field to drive the squeezing effect. The uv space
// is normalized against dst's own size (uv scale = 1/size).
func (s *MetaballShader) Draw(dst *ebiten.Image, groups []Group) error {
	w, h := dst.Bounds().Dx(), dst.Bounds().Dy()
	return s.DrawScaled(dst, groups, [2]float32{1 / float32(w), 1 / float32(h)})
}

// DrawScaled is like Draw, but with an explicit uv scale (uv units per
// pixel) instead of dst's own reciprocal size. A uniform scale keeps circles
// circular regardless of aspect ratio: e.g. {1/h, 1/h} maps the canvas to
// [0, w/h]x[0, 1] in uv space. Both scale components must be positive.
func (s *MetaballShader) DrawScaled(dst *ebiten.Image, groups []Group, uvScale [2]float32) error {
	return s.DrawScaledAt(dst, groups, uvScale, [2]float32{0, 0})
}

// DrawScaledAt is like DrawScaled, but additionally offsets the fragment
// position by origin before applying the uv scale. This is required when dst
// is a sub-image: Kage's dstPos is local to the sub-image's own bounds, so
// origin (the sub-image's pixel offset within the full canvas) must be added
// back to recover the full canvas's uv space.
func (s *MetaballShader) DrawScaledAt(dst *ebiten.Image, groups []Group, uvScale, origin [2]float32) error {
	if uvScale[0] <= 0 || uvScale[1] <= 0 {
		return fmt.Errorf("uv scale must be positive: %v", uvScale)
	}

	for i, g := range groups {
		other := combineGroups(groups, i)

		if err := s.drawPass(dst, g, other, g.Color, uvScale, origin); err != nil {
			return err
		}
	}

	return nil
}

func (s *MetaballShader) drawPass(
	dst *ebiten.Image,
	main Group,
	other Group,
	color ebiten.ColorScale,
	uvScale [2]float32,
	origin [2]float32,
) error {
	if len(main.Circles) > s.config.MainCircles ||
		len(main.Bridges) > s.config.MainBridges ||
		len(other.Circles) > s.config.OtherCircles ||
		len(other.Bridges) > s.config.OtherBridges {
		return fmt.Errorf(
			"metaball counts exceed shader capacity: main circles %d/%d, main bridges %d/%d, other circles %d/%d, other bridges %d/%d",
			len(main.Circles), s.config.MainCircles,
			len(main.Bridges), s.config.MainBridges,
			len(other.Circles), s.config.OtherCircles,
			len(other.Bridges), s.config.OtherBridges,
		)
	}

	uniforms := s.pools.uniforms.Get().(map[string]any)
	defer s.pools.uniforms.Put(uniforms)
	uniforms["UvScale"] = uvScale[:]
	uniforms["MainColor"] = []float32{color.R(), color.G(), color.B(), color.A()}

	mainCircles := s.pools.mainCircles.Get()
	s.packCircles(mainCircles, main.Circles)
	defer s.pools.mainCircles.Put(mainCircles)
	uniforms["MainCircleCount"] = len(main.Circles)
	uniforms["MainCircles"] = *mainCircles

	if s.config.MainBridges > 0 {
		mainEnds := s.pools.mainBridgeEnds.Get()
		mainRadii := s.pools.mainBridgeRadii.Get()
		err := s.packBridges(mainEnds, mainRadii, main.Circles, main.Bridges)
		defer s.pools.mainBridgeEnds.Put(mainEnds)
		defer s.pools.mainBridgeRadii.Put(mainRadii)
		if err != nil {
			return err
		}
		uniforms["MainBridgeCount"] = len(main.Bridges)
		uniforms["MainBridgeEnds"] = *mainEnds
		uniforms["MainBridgeRadii"] = *mainRadii
	}

	if s.config.OtherCircles > 0 {
		otherCirclesBuf := s.pools.otherCircles.Get()
		s.packCircles(otherCirclesBuf, other.Circles)
		defer s.pools.otherCircles.Put(otherCirclesBuf)
		uniforms["OtherCircleCount"] = len(other.Circles)
		uniforms["OtherCircles"] = *otherCirclesBuf
	}

	if s.config.OtherBridges > 0 {
		otherEnds := s.pools.otherBridgeEnds.Get()
		otherRadii := s.pools.otherBridgeRadii.Get()
		err := s.packBridges(otherEnds, otherRadii, other.Circles, other.Bridges)
		defer s.pools.otherBridgeEnds.Put(otherEnds)
		defer s.pools.otherBridgeRadii.Put(otherRadii)
		if err != nil {
			return err
		}
		uniforms["OtherBridgeCount"] = len(other.Bridges)
		uniforms["OtherBridgeEnds"] = *otherEnds
		uniforms["OtherBridgeRadii"] = *otherRadii
	}

	var transform ebiten.GeoM
	transform.Translate(float64(origin[0]), float64(origin[1]))
	w, h := dst.Bounds().Dx(), dst.Bounds().Dy()
	dst.DrawRectShader(w, h, s.shader, &ebiten.DrawRectShaderOptions{
		GeoM:     transform,
		Uniforms: uniforms,
	})

	return nil
}
