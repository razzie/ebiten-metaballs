package metaballs

import (
	"reflect"
	"strings"
	"testing"
)

// TestNewMetaballShaderCompilesTemplates is a smoke test that both Kage
// templates (basic and edge variants) generate valid, compilable shader
// source. ebiten.NewShader only parses/type-checks Kage and needs no GPU or
// display, so this runs headless and catches template breakage that
// `go build` cannot see (e.g. uniform renames).
func TestNewMetaballShaderCompilesTemplates(t *testing.T) {
	configs := map[string]ShaderConfig{
		"basic": {
			ShaderCapacity:     ShaderCapacity{MainCircles: 8, OtherCircles: 8},
			ShaderCommonConfig: ShaderCommonConfig{SmoothK: 0.02},
		},
		"basic with bridges": {
			ShaderCapacity:     ShaderCapacity{MainCircles: 8, MainBridges: 4, OtherCircles: 8, OtherBridges: 4},
			ShaderCommonConfig: ShaderCommonConfig{SmoothK: 0.02},
		},
		"edge": {
			ShaderCapacity:     ShaderCapacity{MainCircles: 8, MainBridges: 4, OtherCircles: 8, OtherBridges: 4},
			ShaderCommonConfig: ShaderCommonConfig{SmoothK: 0.02, LightDirX: 1, LightDirY: -1, EdgeThickness: 0.015},
		},
	}

	for name, cfg := range configs {
		t.Run(name, func(t *testing.T) {
			if _, err := NewMetaballShader(cfg); err != nil {
				t.Fatalf("NewMetaballShader(%+v): %v", cfg, err)
			}
		})
	}
}

func TestPackCircles(t *testing.T) {
	circles := []Circle{
		{X: 1.5, Y: -2.25, Radius: 3.75},
		{X: 4, Y: 5.5, Radius: 6.25},
	}

	out := make([]float32, len(circles)*4)
	packCircles(&out, circles)

	want := []float32{1.5, -2.25, 3.75, 0, 4, 5.5, 6.25, 0}
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
	radii := make([]float32, len(bridges)*3)
	if err := packBridges(&ends, &radii, circles, bridges); err != nil {
		t.Fatalf("packBridges returned error: %v", err)
	}

	wantEnds := []float32{1, 2, 7, 8, 7, 8, 9, 10}
	wantRadii := []float32{2, 2.5, 5, 5, 3.5, 3}
	if !reflect.DeepEqual(ends, wantEnds) {
		t.Fatalf("packBridges ends mismatch: got %v, want %v", ends, wantEnds)
	}
	if !reflect.DeepEqual(radii, wantRadii) {
		t.Fatalf("packBridges radii mismatch: got %v, want %v", radii, wantRadii)
	}
}

func TestPackBridgesRejectsInvalidIndices(t *testing.T) {
	circles := []Circle{{X: 1, Y: 2, Radius: 3}}
	bridges := []Bridge{{A: 0, B: 1, MiddleRadius: 4}}
	ends := make([]float32, 4)
	radii := make([]float32, 3)

	err := packBridges(&ends, &radii, circles, bridges)
	if err == nil {
		t.Fatal("packBridges: expected error for invalid bridge indices")
	}
	if !strings.Contains(err.Error(), "invalid bridge indices") {
		t.Fatalf("packBridges error = %q, want invalid bridge indices", err)
	}
}
