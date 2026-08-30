package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"text/template"

	"github.com/hajimehoshi/ebiten/v2"
)

//go:embed shader.kage.tmpl
var kageSource string

var kageTemplate = template.Must(template.New("shader").Parse(kageSource))

// ShaderLimits sets the fixed-size array capacities baked into the compiled Kage shader.
type ShaderLimits struct {
	MainCircles  int
	MainBridges  int
	OtherCircles int
	OtherBridges int
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
}

type MetaballShader struct {
	shader *ebiten.Shader
	limits ShaderLimits
}

func NewMetaballShader(limits ShaderLimits) (*MetaballShader, error) {
	if limits.MainCircles <= 0 || limits.MainBridges <= 0 ||
		limits.OtherCircles <= 0 || limits.OtherBridges <= 0 {
		return nil, fmt.Errorf("shader limits must all be positive: %+v", limits)
	}

	var src bytes.Buffer
	if err := kageTemplate.Execute(&src, limits); err != nil {
		return nil, fmt.Errorf("generate metaball shader source: %w", err)
	}

	shader, err := ebiten.NewShader(src.Bytes())
	if err != nil {
		return nil, fmt.Errorf("compile metaball shader: %w", err)
	}

	return &MetaballShader{shader: shader, limits: limits}, nil
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

func (s *MetaballShader) Draw(
	dst *ebiten.Image,
	main Group,
	other Group,
	color [3]float32,
	smoothK float32,
) error {
	if len(main.Circles) > s.limits.MainCircles ||
		len(main.Bridges) > s.limits.MainBridges ||
		len(other.Circles) > s.limits.OtherCircles ||
		len(other.Bridges) > s.limits.OtherBridges {
		return fmt.Errorf(
			"metaball counts exceed shader capacity: main circles %d/%d, main bridges %d/%d, other circles %d/%d, other bridges %d/%d",
			len(main.Circles), s.limits.MainCircles,
			len(main.Bridges), s.limits.MainBridges,
			len(other.Circles), s.limits.OtherCircles,
			len(other.Bridges), s.limits.OtherBridges,
		)
	}

	mainEnds, mainRadii, err := packBridges(main.Circles, main.Bridges, s.limits.MainBridges)
	if err != nil {
		return err
	}

	otherEnds, otherRadii, err := packBridges(other.Circles, other.Bridges, s.limits.OtherBridges)
	if err != nil {
		return err
	}

	w, h := dst.Bounds().Dx(), dst.Bounds().Dy()

	uniforms := map[string]any{
		"Resolution": []float32{
			float32(w),
			float32(h),
		},
		"SmoothK":   smoothK,
		"MainColor": color[:],

		"MainCircleCount": len(main.Circles),
		"MainCircles":     packCircles(main.Circles, s.limits.MainCircles),

		"MainBridgeCount": len(main.Bridges),
		"MainBridgeEnds":  mainEnds,
		"MainBridgeRadii": mainRadii,

		"OtherCircleCount": len(other.Circles),
		"OtherCircles":     packCircles(other.Circles, s.limits.OtherCircles),

		"OtherBridgeCount": len(other.Bridges),
		"OtherBridgeEnds":  otherEnds,
		"OtherBridgeRadii": otherRadii,
	}

	dst.DrawRectShader(w, h, s.shader, &ebiten.DrawRectShaderOptions{
		Uniforms: uniforms,
	})

	return nil
}
