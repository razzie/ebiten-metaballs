package metaballs

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
)

// TestNewMetaballShaderCompilesTemplates is a smoke test that the main Kage
// template generates valid, compilable basic and edge shader variants.
// ebiten.NewShader only parses/type-checks Kage and needs no GPU or
// display, so this runs headless and catches template breakage that
// `go build` cannot see (e.g. uniform renames).
func TestNewMetaballShaderCompilesTemplates(t *testing.T) {
	configs := map[string]ShaderConfig{
		"basic": {
			ShaderCapacity:     ShaderCapacity{Groups: 3, Circles: 16},
			ShaderCommonConfig: ShaderCommonConfig{SmoothK: 0.02},
		},
		"basic with bridges": {
			ShaderCapacity:     ShaderCapacity{Groups: 3, Circles: 16, Bridges: 8},
			ShaderCommonConfig: ShaderCommonConfig{SmoothK: 0.02},
		},
		"edge": {
			ShaderCapacity:     ShaderCapacity{Groups: 3, Circles: 16, Bridges: 8},
			ShaderCommonConfig: ShaderCommonConfig{SmoothK: 0.02, LightDirX: 1, LightDirY: -1, EdgeThickness: 0.015},
		},
	}

	for name, cfg := range configs {
		t.Run(name, func(t *testing.T) {
			for _, border := range []float32{0, 0.005} {
				cfg.BorderThickness = border
				shader, err := NewMetaballShader(cfg)
				if err != nil {
					t.Fatalf("NewMetaballShader(%+v): %v", cfg, err)
				}
				shader.shader.Deallocate()
			}
		})
	}
}

func TestShaderRejectsInvalidBorderThickness(t *testing.T) {
	for _, border := range []float32{-1, float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))} {
		cfg := ShaderConfig{ShaderCapacity: ShaderCapacity{Groups: 1, Circles: 1}, ShaderCommonConfig: ShaderCommonConfig{SmoothK: 0.02, BorderThickness: border}}
		if _, err := NewMetaballShader(cfg); err == nil || !strings.Contains(err.Error(), "BorderThickness") {
			t.Fatalf("border %v: expected BorderThickness validation error, got %v", border, err)
		}
	}
}

func TestPackCircles(t *testing.T) {
	circles := []Circle{
		{X: 1.5, Y: -2.25, Radius: 3.75},
		{X: 4, Y: 5.5, Radius: 6.25},
	}

	out := make([]float32, len(circles)*4)
	packCircles(out, circles, 2)

	want := []float32{1.5, -2.25, 3.75, 2, 4, 5.5, 6.25, 2}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("packCircles mismatch: got %v, want %v", out, want)
	}
}

func TestPackBridges(t *testing.T) {
	circles := []Circle{
		{X: 1, Y: 2, Radius: 4},
		{X: 7, Y: 8, Radius: 10},
		{X: 9, Y: 10, Radius: 6},
	}
	bridges := []Bridge{
		{A: 0, B: 1, MiddleRadius: 2.5},
		{A: 1, B: 2, MiddleRadius: 3.5},
	}

	ends := make([]float32, len(bridges)*4)
	radii := make([]float32, len(bridges)*4)
	packBridges(ends, radii, circles, bridges, 1)

	wantEnds := []float32{1, 2, 7, 8, 7, 8, 9, 10}
	wantRadii := []float32{2, 2.5, 5, 1, 5, 3.5, 3, 1}
	if !reflect.DeepEqual(ends, wantEnds) {
		t.Fatalf("packBridges ends mismatch: got %v, want %v", ends, wantEnds)
	}
	if !reflect.DeepEqual(radii, wantRadii) {
		t.Fatalf("packBridges radii mismatch: got %v, want %v", radii, wantRadii)
	}
}

func TestValidateGroupsRejectsInvalidIndices(t *testing.T) {
	circles := []Circle{{X: 1, Y: 2, Radius: 3}}
	bridges := []Bridge{{A: 0, B: 1, MiddleRadius: 4}}
	err := validateGroups([]Group{{Circles: circles, Bridges: bridges}})
	if err == nil {
		t.Fatal("packBridges: expected error for invalid bridge indices")
	}
	if !strings.Contains(err.Error(), "invalid bridge indices") {
		t.Fatalf("packBridges error = %q, want invalid bridge indices", err)
	}
}

func TestPackGroupsDenseIDsAndLocalBridges(t *testing.T) {
	groups := []Group{
		{},
		{Circles: []Circle{{X: 1, Y: 2, Radius: 4}, {X: 7, Y: 8, Radius: 10}}, Bridges: []Bridge{{A: 0, B: 1, MiddleRadius: 2.5}}, Color: NewColorScale(1, 0, 0, 1)},
		{},
		{Circles: []Circle{{X: 20, Y: 30, Radius: 6}, {X: 40, Y: 50, Radius: 8}}, Bridges: []Bridge{{A: 0, B: 1, MiddleRadius: 3}}, Color: NewColorScale(0, 0, 1, 0.5)},
	}
	circles, ends, radii, colors := make([]float32, 16), make([]float32, 8), make([]float32, 8), make([]float32, 8)
	packGroups(circles, ends, radii, colors, groups)
	for name, test := range map[string]struct{ got, want []float32 }{
		"circles": {circles, []float32{1, 2, 4, 0, 7, 8, 10, 0, 20, 30, 6, 1, 40, 50, 8, 1}},
		"ends":    {ends, []float32{1, 2, 7, 8, 20, 30, 40, 50}},
		"radii":   {radii, []float32{2, 2.5, 5, 0, 3, 3, 4, 1}},
		"colors":  {colors, []float32{1, 0, 0, 1, 0, 0, 1, 0.5}},
	} {
		if !reflect.DeepEqual(test.got, test.want) {
			t.Fatalf("%s = %v, want %v", name, test.got, test.want)
		}
	}
	if got := CapacityForGroups(groups); got != (ShaderCapacity{Groups: 2, Circles: 4, Bridges: 2}) {
		t.Fatalf("capacity = %v", got)
	}
}

func TestShaderRejectsSceneExceedingTotalCapacity(t *testing.T) {
	s, err := NewMetaballShader(ShaderConfig{ShaderCapacity: ShaderCapacity{Groups: 2, Circles: 3, Bridges: 1}, ShaderCommonConfig: ShaderCommonConfig{SmoothK: 0.1}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.shader.Deallocate()
	var xf UVTransform
	xf.SetScale(1, 1)
	for name, groups := range map[string][]Group{
		"groups":  {{Circles: []Circle{{}}}, {Circles: []Circle{{}}}, {Circles: []Circle{{}}}},
		"circles": {{Circles: make([]Circle, 2)}, {Circles: make([]Circle, 2)}},
		"bridges": {{Circles: []Circle{{}}, Bridges: []Bridge{{}, {}}}},
	} {
		t.Run(name, func(t *testing.T) {
			// Invalid scenes must be rejected before accessing a destination or drawing.
			if err := s.Draw(nil, groups, xf); err == nil || !strings.Contains(err.Error(), "exceed shader capacity") {
				t.Fatalf("expected capacity error, got %v", err)
			}
		})
	}
}

func TestMetaballShaderCapacityVariants(t *testing.T) {
	for _, edge := range []bool{false, true} {
		for mask := 0; mask < 8; mask++ {
			t.Run(fmt.Sprintf("edge=%t/capacities=%d", edge, mask), func(t *testing.T) {
				cfg := ShaderConfig{ShaderCapacity: ShaderCapacity{Groups: 1 + mask/2, Circles: 2 + mask, Bridges: mask & 1}, ShaderCommonConfig: ShaderCommonConfig{SmoothK: 0.02}}
				if edge {
					cfg.LightDirX, cfg.EdgeThickness = 1, 0.01
				}
				shader, err := NewMetaballShader(cfg)
				if err != nil {
					t.Fatal(err)
				}
				shader.shader.Deallocate()
			})
		}
	}
}
