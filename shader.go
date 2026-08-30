package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"text/template"

	"github.com/hajimehoshi/ebiten/v2"
)

//go:embed shader.kage
var kageTemplate string

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

	mainCircles  int
	mainBridges  int
	otherCircles int
	otherBridges int
}

func NewMetaballShader(main, other Group) (*MetaballShader, error) {
	data := struct {
		MainCircles  int
		MainBridges  int
		OtherCircles int
		OtherBridges int
	}{
		MainCircles:  len(main.Circles),
		MainBridges:  len(main.Bridges),
		OtherCircles: len(other.Circles),
		OtherBridges: len(other.Bridges),
	}

	t, err := template.New("metaballs").Funcs(template.FuncMap{
		"seq": func(n int) []int {
			r := make([]int, n)
			for i := range r {
				r[i] = i
			}
			return r
		},
	}).Parse(kageTemplate)
	if err != nil {
		return nil, err
	}

	var src bytes.Buffer
	if err := t.Execute(&src, data); err != nil {
		return nil, err
	}

	shader, err := ebiten.NewShader(src.Bytes())
	if err != nil {
		return nil, fmt.Errorf("compile metaball shader: %w\n\n%s", err, src.String())
	}

	return &MetaballShader{
		shader:       shader,
		mainCircles:  data.MainCircles,
		mainBridges:  data.MainBridges,
		otherCircles: data.OtherCircles,
		otherBridges: data.OtherBridges,
	}, nil
}

func packCircles(circles []Circle) []float32 {
	out := make([]float32, 0, len(circles)*4)

	for _, c := range circles {
		// vec4(x, y, radius, unused)
		out = append(out, c.X, c.Y, c.Radius, 0)
	}

	return out
}

func packBridges(circles []Circle, bridges []Bridge) (ends, radii []float32, err error) {
	ends = make([]float32, 0, len(bridges)*4)
	radii = make([]float32, 0, len(bridges)*3)

	for _, br := range bridges {
		if br.A < 0 || br.A >= len(circles) ||
			br.B < 0 || br.B >= len(circles) {
			return nil, nil, fmt.Errorf(
				"invalid bridge indices: %d -> %d",
				br.A, br.B,
			)
		}

		a := circles[br.A]
		b := circles[br.B]

		// vec4(a.x, a.y, b.x, b.y)
		ends = append(ends, a.X, a.Y, b.X, b.Y)

		// vec3(radiusA, radiusMiddle, radiusB)
		radii = append(
			radii,
			a.Radius,
			br.MiddleRadius,
			b.Radius,
		)
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
	if len(main.Circles) != s.mainCircles ||
		len(main.Bridges) != s.mainBridges ||
		len(other.Circles) != s.otherCircles ||
		len(other.Bridges) != s.otherBridges {
		return fmt.Errorf("metaball counts changed; shader must be recompiled")
	}

	mainEnds, mainRadii, err := packBridges(main.Circles, main.Bridges)
	if err != nil {
		return err
	}

	otherEnds, otherRadii, err := packBridges(other.Circles, other.Bridges)
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
	}

	if s.mainCircles > 0 {
		uniforms["MainCircles"] = packCircles(main.Circles)
	}
	if s.mainBridges > 0 {
		uniforms["MainBridgeEnds"] = mainEnds
		uniforms["MainBridgeRadii"] = mainRadii
	}

	if s.otherCircles > 0 {
		uniforms["OtherCircles"] = packCircles(other.Circles)
	}
	if s.otherBridges > 0 {
		uniforms["OtherBridgeEnds"] = otherEnds
		uniforms["OtherBridgeRadii"] = otherRadii
	}

	dst.DrawRectShader(w, h, s.shader, &ebiten.DrawRectShaderOptions{
		Uniforms: uniforms,
	})

	return nil
}
