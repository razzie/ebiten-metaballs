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
	packGroups(circles, ends, radii, nil, nil, colors, groups)
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

func TestWallPackingAndCapacity(t *testing.T) {
	groups := []Group{{}, {Walls: []Wall{{AX: 1, AY: 2, BX: 3, BY: 4, Thickness: 6}}, Color: NewColorScale(1, 0, 0, 1)}, {}, {Circles: []Circle{{Radius: 2}}, Walls: []Wall{{AX: 7, BX: 8}}, Color: NewColorScale(0, 1, 0, 1)}}
	if got := CapacityForGroups(groups); got != (ShaderCapacity{Groups: 2, Circles: 1, Walls: 2}) {
		t.Fatal(got)
	}
	circles, ends, data, colors := make([]float32, 4), make([]float32, 8), make([]float32, 4), make([]float32, 8)
	packGroups(circles, nil, nil, ends, data, colors, groups)
	if !reflect.DeepEqual(ends, []float32{1, 2, 3, 4, 7, 0, 8, 0}) || !reflect.DeepEqual(data, []float32{3, 0, 0, 1}) || circles[3] != 1 || colors[0] != 1 || colors[5] != 1 {
		t.Fatalf("bad packed walls: %v %v %v %v", ends, data, circles, colors)
	}
}

func TestWallValidationAndShaderVariants(t *testing.T) {
	for _, wall := range []Wall{{Thickness: -1}, {Thickness: float32(math.NaN())}, {AX: float32(math.Inf(1))}, {BY: float32(math.NaN())}} {
		if err := validateGroups([]Group{{Walls: []Wall{wall}}}); err == nil {
			t.Fatalf("accepted %v", wall)
		}
	}
	for _, groups := range []int{1, 3} {
		for _, circles := range []int{0, 2} {
			for _, edge := range []bool{false, true} {
				cfg := ShaderConfig{ShaderCapacity: ShaderCapacity{Groups: groups, Circles: circles, Walls: 3}, ShaderCommonConfig: ShaderCommonConfig{SmoothK: 0.1, BorderThickness: 0.01}}
				if edge {
					cfg.LightDirX, cfg.EdgeThickness = 1, 0.02
				}
				s, err := NewMetaballShader(cfg)
				if err != nil {
					t.Fatal(err)
				}
				s.shader.Deallocate()
			}
		}
	}
	cfg := ShaderConfig{ShaderCapacity: ShaderCapacity{Groups: 1, Circles: 1, Walls: -1}, ShaderCommonConfig: ShaderCommonConfig{SmoothK: 0.1}}
	if _, err := NewMetaballShader(cfg); err == nil {
		t.Fatal("accepted negative wall capacity")
	}
	cfg.Walls = 1
	s, err := NewMetaballShader(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.shader.Deallocate()
	var xf UVTransform
	xf.SetScale(1, 1)
	if err := s.Draw(nil, []Group{{Walls: make([]Wall, 2)}}, xf); err == nil {
		t.Fatal("accepted wall capacity overflow")
	}
}

func TestWallTileFilteringAndClipping(t *testing.T) {
	groups := []Group{{Walls: []Wall{{AX: -1, AY: 0.25, BX: 2, BY: 0.25}, {AX: 2, AY: 2, BX: 3, BY: 3}, {AX: 0.6, AY: 0, BX: 0.6, BY: 1, Thickness: 0.3}}}, {Circles: []Circle{{X: 0.25, Y: 0.25, Radius: 0.1}}}}
	r := &Renderer{cfg: RendererConfig{Common: ShaderCommonConfig{SmoothK: 0.01}, Tiers: []ShaderCapacity{{Groups: 2, Circles: 1, Walls: 1}, {Groups: 2, Circles: 1, Walls: 3}}}}
	r.pools.init(2, 1, 0, 3)
	tile := UVBounds{MinX: 0, MinY: 0, MaxX: 0.5, MaxY: 0.5}
	prep, any, capacity, release := r.prepareGroupsForTile(groups, tile)
	defer release()
	if !any || capacity != (ShaderCapacity{Groups: 2, Circles: 1, Walls: 2}) || r.pickTier(capacity) != 1 {
		t.Fatalf("bad wall probe: %+v", capacity)
	}
	got, releaseGroups := materializeGroupsFromPrep(&r.pools, groups, prep)
	defer releaseGroups()
	if len(got) != 2 || !reflect.DeepEqual(got[0].Walls, []Wall{groups[0].Walls[0], groups[0].Walls[2]}) {
		t.Fatalf("bad wall culling: %+v", got)
	}
	clipGroupsToTier(got, ShaderCapacity{Groups: 1, Circles: 1, Walls: 1})
	if capacity := CapacityForGroups(got); capacity != (ShaderCapacity{Groups: 1, Walls: 1}) {
		t.Fatalf("bad wall clipping: %+v", capacity)
	}
	r.pools.ensure(1, 0, 0, 5)
	if r.pools.preps.maxCircles != 1 || r.pools.preps.numGroups != 2 || r.pools.preps.maxWalls != 5 {
		t.Fatal("wall growth shrank other prep dimensions")
	}
}
