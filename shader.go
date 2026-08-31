package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"math"
	"strconv"
	"strings"
	"text/template"

	"github.com/hajimehoshi/ebiten/v2"
)

//go:embed shader.kage.tmpl
var kageSource string

var kageTemplate = template.Must(template.New("shader").Funcs(template.FuncMap{
	"float": formatKageFloat,
	"vec2":  formatKageVec2,
}).Parse(kageSource))

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

// ShaderConfig sets the compile-time constants baked into the generated Kage shader:
// fixed-size array capacities, the smooth-min blending radius, and edge shading.
// LightDirX/LightDirY of (0, 0) disables edge shading entirely (no gradients computed).
type ShaderConfig struct {
	MainCircles   int
	MainBridges   int
	OtherCircles  int
	OtherBridges  int
	SmoothK       float32
	LightDirX     float32
	LightDirY     float32
	EdgeThickness float32
}

// shaderTemplateData augments ShaderConfig with values derived for code generation.
type shaderTemplateData struct {
	ShaderConfig
	EdgeShadingEnabled   bool
	NeedsCapsuleField    bool
	NeedsCapsuleDistance bool
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

// ConfigForGroups derives shader array capacities from a set of groups: main
// capacities cover the largest single group, other capacities cover the sum
// of all groups (a safe upper bound for any combined "other" pass). Bridge
// and other-circle capacities are left at 0 when unused, so the generated
// shader can skip those loops entirely. lightDirX/lightDirY of (0, 0)
// disables edge shading.
func ConfigForGroups(groups []Group, smoothK float32, lightDirX, lightDirY, edgeThickness float32) ShaderConfig {
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

	return ShaderConfig{
		MainCircles:   mainCircles,
		MainBridges:   mainBridges,
		OtherCircles:  totalCircles,
		OtherBridges:  totalBridges,
		SmoothK:       smoothK,
		LightDirX:     lightDirX,
		LightDirY:     lightDirY,
		EdgeThickness: edgeThickness,
	}
}

type MetaballShader struct {
	shader *ebiten.Shader
	config ShaderConfig
}

func NewMetaballShader(config ShaderConfig) (*MetaballShader, error) {
	if config.MainCircles <= 0 || config.MainBridges < 0 ||
		config.OtherCircles < 0 || config.OtherBridges < 0 || config.SmoothK <= 0 {
		return nil, fmt.Errorf("invalid shader config: %+v", config)
	}

	data := shaderTemplateData{ShaderConfig: config}

	if length := math.Hypot(float64(config.LightDirX), float64(config.LightDirY)); length > 0 {
		data.EdgeShadingEnabled = true
		data.LightDirX = float32(float64(config.LightDirX) / length)
		data.LightDirY = float32(float64(config.LightDirY) / length)

		if config.EdgeThickness <= 0 {
			return nil, fmt.Errorf("invalid shader config: EdgeThickness must be positive when edge shading is enabled: %+v", config)
		}
	}

	data.NeedsCapsuleField = data.EdgeShadingEnabled && config.MainBridges > 0
	data.NeedsCapsuleDistance = config.OtherBridges > 0 || (!data.EdgeShadingEnabled && config.MainBridges > 0)

	var src bytes.Buffer
	if err := kageTemplate.Execute(&src, data); err != nil {
		return nil, fmt.Errorf("generate metaball shader source: %w", err)
	}

	shader, err := ebiten.NewShader(src.Bytes())
	if err != nil {
		return nil, fmt.Errorf("compile metaball shader: %w", err)
	}

	return &MetaballShader{shader: shader, config: config}, nil
}

// packCircles packs circles into vec4(x, y, radius, unused) entries, padded
// with zero entries up to max so the uniform array length matches the shader.
func packCircles(circles []Circle, max int) []float32 {
	out := make([]float32, max*4)

	for i, c := range circles {
		out[i*4], out[i*4+1], out[i*4+2] = c.X, c.Y, c.Radius
	}

	return out
}

// packBridges packs bridges into vec4(a.x, a.y, b.x, b.y) ends and
// vec3(radiusA, radiusMiddle, radiusB) radii, padded up to max.
func packBridges(circles []Circle, bridges []Bridge, max int) (ends, radii []float32, err error) {
	ends = make([]float32, max*4)
	radii = make([]float32, max*3)

	for i, br := range bridges {
		if br.A < 0 || br.A >= len(circles) ||
			br.B < 0 || br.B >= len(circles) {
			return nil, nil, fmt.Errorf(
				"invalid bridge indices: %d -> %d",
				br.A, br.B,
			)
		}

		a := circles[br.A]
		b := circles[br.B]

		ends[i*4], ends[i*4+1], ends[i*4+2], ends[i*4+3] = a.X, a.Y, b.X, b.Y
		radii[i*3], radii[i*3+1], radii[i*3+2] = a.Radius, br.MiddleRadius, b.Radius
	}

	return ends, radii, nil
}

// Draw renders each group in its own pass, using the remaining groups
// combined as the "other" field to drive the squeezing effect.
func (s *MetaballShader) Draw(dst *ebiten.Image, groups []Group) error {
	for i, g := range groups {
		other := combineGroups(groups, i)

		if err := s.drawPass(dst, g, other, g.Color); err != nil {
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

	w, h := dst.Bounds().Dx(), dst.Bounds().Dy()

	uniforms := map[string]any{
		"Resolution": []float32{
			float32(w),
			float32(h),
		},
		"MainColor": []float32{color.R(), color.G(), color.B(), color.A()},

		"MainCircleCount": len(main.Circles),
		"MainCircles":     packCircles(main.Circles, s.config.MainCircles),
	}

	if s.config.MainBridges > 0 {
		mainEnds, mainRadii, err := packBridges(main.Circles, main.Bridges, s.config.MainBridges)
		if err != nil {
			return err
		}

		uniforms["MainBridgeCount"] = len(main.Bridges)
		uniforms["MainBridgeEnds"] = mainEnds
		uniforms["MainBridgeRadii"] = mainRadii
	}

	if s.config.OtherCircles > 0 {
		uniforms["OtherCircleCount"] = len(other.Circles)
		uniforms["OtherCircles"] = packCircles(other.Circles, s.config.OtherCircles)
	}

	if s.config.OtherBridges > 0 {
		otherEnds, otherRadii, err := packBridges(other.Circles, other.Bridges, s.config.OtherBridges)
		if err != nil {
			return err
		}

		uniforms["OtherBridgeCount"] = len(other.Bridges)
		uniforms["OtherBridgeEnds"] = otherEnds
		uniforms["OtherBridgeRadii"] = otherRadii
	}

	dst.DrawRectShader(w, h, s.shader, &ebiten.DrawRectShaderOptions{
		Uniforms: uniforms,
	})

	return nil
}
