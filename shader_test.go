package metaballs

import "testing"

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
