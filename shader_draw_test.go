package metaballs

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
)

// Run the GPU checks in a subprocess: Ebitengine's game loop can only run
// once per process, and pixel readback requires a running loop. Skip if the
// subprocess cannot initialize graphics, but preserve failures after it does.
func TestMetaballShaderDrawAtPixels(t *testing.T) {
	if os.Getenv("METABALLS_DRAW_TESTS") != "1" {
		t.Skip("METABALLS_DRAW_TESTS not enabled")
	}
	const helperEnv = "METABALLS_DRAW_AT_TEST"
	if os.Getenv(helperEnv) != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestMetaballShaderDrawAtPixels$", "-test.v")
		cmd.Env = append(os.Environ(), helperEnv+"=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("pixel tests: %v\n%s", err, output)
		}
		return
	}
	if err := ebiten.RunGame(&drawAtTestGame{t: t}); err != nil {
		t.Fatal(err)
	}
}

type drawAtTestGame struct{ t *testing.T }

func (g *drawAtTestGame) Layout(int, int) (int, int) { return 64, 48 }
func (g *drawAtTestGame) Draw(*ebiten.Image)         {}
func (g *drawAtTestGame) Update() error {
	// Update runs after graphics initialization. Verify GPU readback before
	// marking startup successful; all failures in the actual checks stay fatal.
	probe := ebiten.NewImage(1, 1)
	probe.ReadPixels(make([]byte, 4))
	probe.Deallocate()

	g.t.Run("wall lighting and reuse", checkWallLightingAndReuse)
	for _, edge := range []bool{false, true} {
		for _, fxaa := range []bool{false, true} {
			g.t.Run(fmt.Sprintf("walls/edge=%t/fxaa=%t", edge, fxaa), func(t *testing.T) { checkWallPixels(t, edge, fxaa) })
			g.t.Run(fmt.Sprintf("borders and joints/edge=%t/fxaa=%t", edge, fxaa), func(t *testing.T) {
				checkBorderPixels(t, edge, fxaa)
			})
			for _, bridges := range []bool{false, true} {
				g.t.Run(fmt.Sprintf("continuous influence/edge=%t/fxaa=%t/bridges=%t", edge, fxaa, bridges), func(t *testing.T) {
					checkInfluenceContinuity(t, edge, fxaa, bridges)
				})
			}
			g.t.Run(fmt.Sprintf("grouped scene reuse/edge=%t/fxaa=%t", edge, fxaa), func(t *testing.T) {
				checkGroupedSceneReuse(t, edge, fxaa)
			})
			for _, origin := range []image.Point{{}, {23, 17}} {
				for _, offset := range []image.Point{{}, {100, 0}, {0, 100}, {13, 9}, {-11, -7}} {
					name := fmt.Sprintf("edge=%t/fxaa=%t/origin=%v/offset=%v", edge, fxaa, origin, offset)
					for _, tiled := range []bool{false, true} {
						g.t.Run(fmt.Sprintf("%s/tiled=%t", name, tiled), func(t *testing.T) {
							checkDrawAtPixels(t, edge, fxaa, tiled, origin, offset)
						})
					}
				}
			}
		}
	}
	return ebiten.Termination
}

// Runs inside the existing graphics test loop, covering both shader variants
// and checking the tiled renderer against the direct shader pixel for pixel.
func checkWallPixels(t *testing.T, edge, fxaa bool) {
	cfg := ShaderCommonConfig{SmoothK: 16, FxaaEnabled: fxaa}
	if edge {
		cfg.LightDirX, cfg.LightDirY, cfg.EdgeThickness = -1, -1, 8
	}
	wall := Group{Walls: []Wall{{AX: 128.5, AY: 32.5, BX: 128.5, BY: 224.5, Thickness: 16}}, Color: NewColorScale(0, 1, 0, 1)}
	circle := Group{Circles: []Circle{{X: 138.5, Y: 128.5, Radius: 64}}, Color: NewColorScale(1, 0, 0, 1)}
	pixel := func(p []byte, x, y int) []byte { return p[4*(y*256+x) : 4*(y*256+x)+4] }
	for _, border := range []float32{0, 3} {
		cfg.BorderThickness = border
		for _, scene := range [][]Group{{wall}, {wall, circle}, {circle, wall}} {
			direct := renderBorderPixels(t, scene, cfg, false)
			if got := renderBorderPixels(t, scene, cfg, true); !bytes.Equal(got, direct) {
				t.Fatal("wall tiling differs from direct rendering")
			}
			for _, x := range []int{122, 128, 134} {
				p := pixel(direct, x, 128)
				if p[1] == 0 || p[0] != 0 || p[3] != 255 {
					t.Fatalf("wall squeezed or overwritten at %d: %v", x, p)
				}
			}
			if len(scene) > 1 && pixel(direct, 148, 128)[0] == 0 {
				t.Fatal("foreign circle disappeared outside wall")
			}
		}
	}
	cfg.BorderThickness = 0
	wall.Circles = []Circle{{X: 154.5, Y: 128.5, Radius: 12}}
	merged := renderBorderPixels(t, []Group{wall}, cfg, false)
	if pixel(merged, 140, 128)[1] == 0 {
		t.Fatal("same-group wall and circle did not blend across gap")
	}
	if got := renderBorderPixels(t, []Group{wall}, cfg, true); !bytes.Equal(got, merged) {
		t.Fatal("wall blend differs across tiles")
	}
	wall.Circles = nil
	wall.Walls[0].Thickness = 0
	line := renderBorderPixels(t, []Group{wall}, cfg, false)
	if !bytes.Equal(line, make([]byte, len(line))) {
		t.Fatal("zero-width wall gained filled area")
	}
	if !fxaa {
		cut := renderBorderPixels(t, []Group{wall, circle}, cfg, false)
		if pixel(cut, 128, 128)[3] != 0 || pixel(cut, 132, 128)[0] == 0 {
			t.Fatal("zero-width wall did not cut foreign circle at its centerline")
		}
	}
	// Each wall group excludes only foreign walls, even at an intersection.
	wall.Walls[0].Thickness = 16
	crossing := Group{Walls: []Wall{{AX: 32.5, AY: 128.5, BX: 224.5, BY: 128.5, Thickness: 16}}, Color: NewColorScale(0, 0, 1, 1)}
	scene := []Group{wall, crossing, circle}
	cross := renderBorderPixels(t, scene, cfg, false)
	if pixel(cross, 128, 160)[1] == 0 || pixel(cross, 160, 128)[2] == 0 || pixel(cross, 128, 128)[1] == 0 {
		t.Fatal("foreign wall groups lost their rigid geometry or stable tie ownership")
	}
	if got := renderBorderPixels(t, scene, cfg, true); !bytes.Equal(got, cross) {
		t.Fatal("intersecting wall groups differ across tiles")
	}
	// A zero-length thick wall is a rigid disk, including wall-only scenes.
	wall.Walls[0] = Wall{AX: 128.5, AY: 128.5, BX: 128.5, BY: 128.5, Thickness: 32}
	disk := renderBorderPixels(t, []Group{wall, circle}, cfg, false)
	if pixel(disk, 128, 128)[1] == 0 || pixel(disk, 138, 128)[1] == 0 {
		t.Fatal("degenerate wall lost its rigid disk")
	}
}

func checkWallLightingAndReuse(t *testing.T) {
	cfg := ShaderCommonConfig{SmoothK: 2, LightDirX: -1, EdgeThickness: 48, BorderThickness: 4}
	wall := Group{Walls: []Wall{{AX: 60.5, AY: 0, BX: 60.5, BY: 256, Thickness: 16}}, Color: NewColorScale(0, 1, 0, 1)}
	circle := Group{Circles: []Circle{{X: 128.5, Y: 128.5, Radius: 80}}, Color: NewColorScale(1, 0, 0, 1)}
	direct := renderBorderPixels(t, []Group{wall, circle}, cfg, false)
	if got := renderBorderPixels(t, []Group{wall, circle}, cfg, true); !bytes.Equal(got, direct) {
		t.Fatal("tile culling lost the wide lighting band of a wall cut")
	}
	// The new edge facing the wall lights the circle more brightly than its
	// original surface, well outside the blend radius.
	without := renderBorderPixels(t, []Group{circle}, cfg, false)
	i := 4 * (128*256 + 90)
	if direct[i] <= without[i]+10 {
		t.Fatalf("wall contact lighting ended at blend radius: with=%v without=%v", direct[i:i+4], without[i:i+4])
	}

	capacity := ShaderCapacity{Groups: 3, Circles: 2, Walls: 3}
	s, err := NewMetaballShader(ShaderConfig{ShaderCapacity: capacity, ShaderCommonConfig: cfg})
	if err != nil {
		t.Fatal(err)
	}
	defer s.shader.Deallocate()
	r, err := NewRenderer(RendererConfig{Common: cfg, Tiers: []ShaderCapacity{capacity}, RootCols: 4, RootRows: 4, MinTileSize: 1, Workers: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer r.shaders[0].shader.Deallocate()
	dst := ebiten.NewImage(256, 256)
	defer dst.Deallocate()
	var xf UVTransform
	xf.SetScale(1, 1)
	pixels := make([]byte, 256*256*4)
	for _, scene := range [][]Group{{wall, circle}, {circle}, {{}, wall}, {circle, wall}, {}} {
		want := make([]byte, len(pixels))
		if len(scene) > 0 {
			want = renderBorderPixels(t, scene, cfg, false)
		}
		dst.Clear()
		if err := s.Draw(dst, scene, xf); err != nil {
			t.Fatal(err)
		}
		dst.ReadPixels(pixels)
		if !bytes.Equal(pixels, want) {
			t.Fatal("shader retained stale wall uniforms or changed a circle-only scene")
		}
		dst.Clear()
		if _, err := r.Draw(dst, scene, xf); err != nil {
			t.Fatal(err)
		}
		dst.ReadPixels(pixels)
		if !bytes.Equal(pixels, want) {
			t.Fatal("parallel renderer retained stale wall geometry")
		}
	}
}

// Called inside the existing GPU test game loop.
func checkBorderPixels(t *testing.T, lighting, fxaa bool) {
	t.Helper()
	cfg := ShaderCommonConfig{SmoothK: 8, BorderThickness: 6, FxaaEnabled: fxaa}
	if lighting {
		cfg.LightDirX, cfg.LightDirY, cfg.EdgeThickness = -1, -1, 8
	}
	color := NewColorScale(0.7, 0.85, 0.8, 1)
	circle := []Group{{Circles: []Circle{{X: 64.5, Y: 64.5, Radius: 32}}, Color: color}}
	pixels := renderBorderPixels(t, circle, cfg, false)
	pixel := func(p []byte, x, y int) []byte { return p[4*(y*256+x) : 4*(y*256+x)+4] }
	center, border, outside := pixel(pixels, 64, 64), pixel(pixels, 94, 64), pixel(pixels, 100, 64)
	if center[3] != 255 || border[3] != 255 || outside[3] != 0 || center[0] <= border[0]*2 {
		t.Fatalf("expected colored fill, dark inset border and unchanged silhouette: center=%v border=%v outside=%v", center, border, outside)
	}
	// Lighting must start at the inset boundary and leave the border flat.
	if lighting {
		bright, dark := pixel(pixels, 40, 64), pixel(pixels, 88, 64)
		if bright[0] <= dark[0]+10 {
			t.Fatalf("inset fill is not lit: bright=%v dark=%v", bright, dark)
		}
	}
	if !fxaa {
		// A translucent border must retain the same alpha as its fill.
		circle[0].Color = NewColorScale(0.35, 0.425, 0.4, 0.5)
		transparent := renderBorderPixels(t, circle, cfg, false)
		if a, b := pixel(transparent, 64, 64)[3], pixel(transparent, 94, 64)[3]; a != b || a < 127 || a > 128 {
			t.Fatalf("border changed group alpha: fill=%d border=%d", a, b)
		}
	}

	// Three bridges share a zero-radius joint, with middle radii smaller
	// than the border thickness producing solid, narrow connectors.
	groups := []Group{{
		Circles: []Circle{
			{X: 128.5, Y: 128.5},
			{X: 40.5, Y: 128.5, Radius: 24},
			{X: 200.5, Y: 40.5, Radius: 20},
			{X: 200.5, Y: 216.5, Radius: 16},
		},
		Bridges: []Bridge{{A: 0, B: 1, MiddleRadius: 3}, {A: 0, B: 2, MiddleRadius: 3}, {A: 3, B: 0, MiddleRadius: 6}},
		Color:   color,
	}}
	want := renderBorderPixels(t, groups, cfg, false)
	joint := pixel(want, 128, 128)
	if joint[3] != 255 || joint[0] > 70 {
		t.Fatalf("joint should be solid border without a colored dot: %v", joint)
	}
	if !fxaa && pixel(want, 84, 133)[3] != 0 {
		t.Fatal("border thickness widened the requested bridge middle radius")
	}
	for _, end := range groups[0].Circles[1:] {
		for step := 0; step <= 32; step++ {
			x := int(128 + (end.X-128.5)*float32(step)/32)
			y := int(128 + (end.Y-128.5)*float32(step)/32)
			if p := pixel(want, x, y); p[3] != 255 {
				t.Fatalf("bridge to %v has a hole at (%d,%d): %v", end, x, y, p)
			}
		}
	}
	groups[0].Circles[0].Radius = cfg.BorderThickness
	if got := renderBorderPixels(t, groups, cfg, false); !bytes.Equal(got, want) {
		t.Fatal("zero-radius and border-radius joints render differently")
	}
	groups[0].Circles[0].Radius = 0
	if got := renderBorderPixels(t, groups, cfg, true); !bytes.Equal(got, want) {
		t.Fatal("tiled renderer differs from direct bordered rendering")
	}

	// Make border expansion much larger than the blend padding, to catch
	// tiles dropping a zero-radius joint or a narrow bridge outside its AABB.
	cfg.SmoothK, cfg.BorderThickness = 0.01, 12
	groups = []Group{{
		Circles: []Circle{{X: 58.5, Y: 58.5}, {X: 58.5, Y: 200.5}},
		Bridges: []Bridge{{A: 0, B: 1, MiddleRadius: 3}}, Color: color,
	}, {Circles: []Circle{{X: 76.5, Y: 130.5, Radius: 20}}, Color: NewColorScale(1, 0.5, 0.25, 1)}}
	want = renderBorderPixels(t, groups, cfg, false)
	if got := renderBorderPixels(t, groups, cfg, true); !bytes.Equal(got, want) {
		t.Fatal("tile culling clipped border-expanded joints, bridges, or group contacts")
	}
}

func renderBorderPixels(t *testing.T, groups []Group, cfg ShaderCommonConfig, tiled bool) []byte {
	t.Helper()
	dst := ebiten.NewImage(256, 256)
	defer dst.Deallocate()
	var xf UVTransform
	xf.SetScale(1, 1)
	if tiled {
		r, err := NewRenderer(RendererConfig{Common: cfg, Tiers: []ShaderCapacity{CapacityForGroups(groups)}, RootCols: 4, RootRows: 4, MinTileSize: 1})
		if err != nil {
			t.Fatal(err)
		}
		defer r.shaders[0].shader.Deallocate()
		if _, err := r.Draw(dst, groups, xf); err != nil {
			t.Fatal(err)
		}
		if r.fxaa != nil {
			defer r.fxaa.Deallocate()
			defer r.offscreen.Deallocate()
		}
	} else {
		s, err := NewMetaballShader(ShaderConfig{ShaderCapacity: CapacityForGroups(groups), ShaderCommonConfig: cfg})
		if err != nil {
			t.Fatal(err)
		}
		defer s.shader.Deallocate()
		if err := s.Draw(dst, groups, xf); err != nil {
			t.Fatal(err)
		}
		if s.fxaa != nil {
			defer s.fxaa.Deallocate()
			defer s.offscreen.Deallocate()
		}
	}
	pixels := make([]byte, 256*256*4)
	dst.ReadPixels(pixels)
	return pixels
}

// At the center, the three primitive distances are K, 0.4K, and 0.15K.
// Their ordered smooth union is negative, but dropping the first primitive
// makes it positive. Sample a tiny region straddling that old cutoff: every
// pixel must stay inside, including tiles that used to cull the first primitive.
func checkInfluenceContinuity(t *testing.T, edge, fxaa, bridges bool) {
	t.Helper()
	group := Group{Color: NewColorScale(1, 0.5, 0.25, 1)}
	for _, x := range []float32{-50, -26, -16} {
		if bridges {
			a := len(group.Circles)
			group.Circles = append(group.Circles, Circle{X: x, Y: -100, Radius: 10}, Circle{X: x, Y: 100, Radius: 10})
			group.Bridges = append(group.Bridges, Bridge{A: a, B: a + 1, MiddleRadius: 10})
		} else {
			group.Circles = append(group.Circles, Circle{X: x, Radius: 10})
		}
	}
	groups := []Group{group}
	cfg := ShaderConfig{ShaderCapacity: CapacityForGroups(groups), ShaderCommonConfig: ShaderCommonConfig{SmoothK: 40, FxaaEnabled: fxaa}}
	if edge {
		cfg.LightDirX, cfg.LightDirY, cfg.EdgeThickness = 1, -1, 8
	}
	shader, err := NewMetaballShader(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer shader.shader.Deallocate()
	if shader.fxaa != nil {
		defer shader.fxaa.Deallocate()
		defer func() { shader.offscreen.Deallocate() }()
	}
	renderer, err := NewRenderer(RendererConfig{Common: cfg.ShaderCommonConfig, Tiers: []ShaderCapacity{cfg.ShaderCapacity}, RootCols: 2, RootRows: 2, MinTileSize: 0.001})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range renderer.shaders {
		defer s.shader.Deallocate()
	}
	if renderer.fxaa != nil {
		defer renderer.fxaa.Deallocate()
		defer func() { renderer.offscreen.Deallocate() }()
	}
	var xf UVTransform
	xf.SetScale(1.0/4096, 1.0/4096)
	xf.SetOffset(-1.0/256, -1.0/256)
	direct, tiled := ebiten.NewImage(32, 32), ebiten.NewImage(32, 32)
	defer direct.Deallocate()
	defer tiled.Deallocate()
	if err := shader.Draw(direct, groups, xf); err != nil {
		t.Fatal(err)
	}
	stats, err := renderer.Draw(tiled, groups, xf)
	if err != nil {
		t.Fatal(err)
	}
	if stats.CirclesClipped.Load() != 0 {
		t.Fatal("continuity scene exceeded renderer capacity")
	}
	want, got := make([]byte, 32*32*4), make([]byte, 32*32*4)
	direct.ReadPixels(want)
	tiled.ReadPixels(got)
	for i := 3; i < len(want); i += 4 {
		if want[i] != 255 {
			t.Fatalf("influence cutoff opened a hole at (%d,%d): alpha=%d", i/4%32, i/4/32, want[i])
		}
	}
	if !bytes.Equal(got, want) {
		t.Fatal("tile culling changed the smooth union near a primitive's influence cutoff")
	}
}

// Reuse the same uniform pools across changing counts, empty groups, and
// reordered groups. Compare both direct and tiled output with a fresh shader.
func checkGroupedSceneReuse(t *testing.T, edge, fxaa bool) {
	t.Helper()
	cfg := ShaderConfig{ShaderCapacity: ShaderCapacity{Groups: 3, Circles: 8, Bridges: 2}, ShaderCommonConfig: ShaderCommonConfig{SmoothK: 12, FxaaEnabled: fxaa}}
	if edge {
		cfg.LightDirX, cfg.LightDirY, cfg.EdgeThickness = 1, -1, 4
	}
	shader, err := NewMetaballShader(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer shader.shader.Deallocate()
	if shader.fxaa != nil {
		defer shader.fxaa.Deallocate()
		defer func() {
			if shader.offscreen != nil {
				shader.offscreen.Deallocate()
			}
		}()
	}
	renderer, err := NewRenderer(RendererConfig{Common: cfg.ShaderCommonConfig, Tiers: []ShaderCapacity{cfg.ShaderCapacity}, RootCols: 4, RootRows: 3, MinTileSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range renderer.shaders {
		defer s.shader.Deallocate()
	}
	if renderer.fxaa != nil {
		defer renderer.fxaa.Deallocate()
		defer func() {
			if renderer.offscreen != nil {
				renderer.offscreen.Deallocate()
			}
		}()
	}
	red := Group{Circles: []Circle{{X: 35.5, Y: 48.5, Radius: 24}, {X: 85.5, Y: 48.5, Radius: 20}}, Bridges: []Bridge{{A: 0, B: 1, MiddleRadius: 14}}, Color: NewColorScale(1, 0, 0, 1)}
	blue := Group{Circles: []Circle{{X: 62.5, Y: 32.5, Radius: 18}}, Color: NewColorScale(0, 0, 1, 1)}
	green := Group{Circles: []Circle{{X: 112.5, Y: 72.5, Radius: 14}}, Color: NewColorScale(0, 1, 0, 1)}
	var xf UVTransform
	xf.SetScale(1, 1)
	for frame, groups := range [][]Group{{{}, red, blue, {}, green}, {blue}, {}, {green, blue, red}, {red, blue}} {
		fresh, err := NewMetaballShader(cfg)
		if err != nil {
			t.Fatal(err)
		}
		want, got, tiled := ebiten.NewImage(144, 96), ebiten.NewImage(144, 96), ebiten.NewImage(144, 96)
		if err := fresh.Draw(want, groups, xf); err != nil {
			t.Fatal(err)
		}
		if err := shader.Draw(got, groups, xf); err != nil {
			t.Fatal(err)
		}
		if _, err := renderer.Draw(tiled, groups, xf); err != nil {
			t.Fatal(err)
		}
		expected, actual := make([]byte, 144*96*4), make([]byte, 144*96*4)
		want.ReadPixels(expected)
		for name, dst := range map[string]*ebiten.Image{"reused": got, "tiled": tiled} {
			dst.ReadPixels(actual)
			if !bytes.Equal(expected, actual) {
				t.Fatalf("frame %d: %s output differs from fresh single-pass shader", frame, name)
			}
		}
		want.Deallocate()
		got.Deallocate()
		tiled.Deallocate()
		fresh.shader.Deallocate()
		if fresh.fxaa != nil {
			fresh.fxaa.Deallocate()
			if fresh.offscreen != nil {
				fresh.offscreen.Deallocate()
			}
		}
	}
}

func checkDrawAtPixels(t *testing.T, edge, fxaa, tiled bool, origin, offset image.Point) {
	t.Helper()
	const w, h = 192, 128
	cfg := ShaderConfig{
		ShaderCapacity:     ShaderCapacity{Groups: 1, Circles: 3},
		ShaderCommonConfig: ShaderCommonConfig{SmoothK: 0.03125, FxaaEnabled: fxaa},
	}
	if edge {
		cfg.LightDirX, cfg.LightDirY, cfg.EdgeThickness = 1, -1, 0.0625
	}
	s, err := NewMetaballShader(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.shader.Deallocate()
	if s.fxaa != nil {
		defer s.fxaa.Deallocate()
		defer func() { s.offscreen.Deallocate() }()
	}
	// Binary fractions keep equivalent transforms exact, including the
	// non-uniform scale and nonzero UV offset.
	var xform UVTransform
	xform.SetScale(1.0/64, 1.0/32)
	xform.SetOffset(-0.25, 0.125)
	groups := []Group{{
		Circles: []Circle{
			{X: 0.0625, Y: 0.75, Radius: 0.1875},
			{X: -0.203125, Y: 0.21875, Radius: 0.15625},
			{X: 0.6875, Y: 1.46875, Radius: 0.15625},
		},
		Color: NewColorScale(1, 0.5, 0.25, 1),
	}}
	// The reference translates circle coordinates themselves. DrawAt must
	// produce that same right/down movement using a pixel offset instead.
	originalCircles := append([]Circle(nil), groups[0].Circles...)
	for i := range groups[0].Circles {
		groups[0].Circles[i].X += float32(offset.X) * xform.scale[0]
		groups[0].Circles[i].Y += float32(offset.Y) * xform.scale[1]
	}
	want := ebiten.NewImage(w, h)
	defer want.Deallocate()
	if err := s.Draw(want, groups, xform); err != nil {
		t.Fatal(err)
	}
	wantPixels := make([]byte, 4*w*h)
	want.ReadPixels(wantPixels)
	if bytes.Equal(wantPixels, make([]byte, len(wantPixels))) {
		t.Fatal("reference image is empty")
	}

	// Place the untranslated scene in the parent's coordinate system.
	// Sub-images should only clip it, never introduce another translation.
	groups[0].Circles = originalCircles
	for i := range groups[0].Circles {
		groups[0].Circles[i].X += float32(origin.X) * xform.scale[0]
		groups[0].Circles[i].Y += float32(origin.Y) * xform.scale[1]
	}
	parent := ebiten.NewImage(w+origin.X+8, h+origin.Y+8)
	defer parent.Deallocate()
	bounds := image.Rectangle{Min: origin, Max: origin.Add(image.Pt(w, h))}
	dst := parent.SubImage(bounds).(*ebiten.Image)
	if tiled {
		r, err := NewRenderer(RendererConfig{
			Common:   cfg.ShaderCommonConfig,
			Tiers:    []ShaderCapacity{{Groups: 1, Circles: 3}},
			RootCols: 4, RootRows: 4, MinTileSize: 0.03125,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, shader := range r.shaders {
			defer shader.shader.Deallocate()
		}
		if r.fxaa != nil {
			defer r.fxaa.Deallocate()
			defer func() { r.offscreen.Deallocate() }()
		}
		if _, err := r.DrawAt(dst, groups, xform, offset); err != nil {
			t.Fatal(err)
		}
	} else if err := s.DrawAt(dst, groups, xform, offset); err != nil {
		t.Fatal(err)
	}
	gotPixels := make([]byte, len(wantPixels))
	dst.ReadPixels(gotPixels)
	if !bytes.Equal(gotPixels, wantPixels) {
		for i := range gotPixels {
			if gotPixels[i] != wantPixels[i] {
				t.Fatalf("pixel (%d,%d) channel %d = %d, want %d", i/4%w, i/4/w, i%4, gotPixels[i], wantPixels[i])
			}
		}
	}
	// Drawing the sub-image must leave the rest of the parent untouched.
	pixels := make([]byte, 4*parent.Bounds().Dx()*parent.Bounds().Dy())
	parent.ReadPixels(pixels)
	for y := 0; y < parent.Bounds().Dy(); y++ {
		for x := 0; x < parent.Bounds().Dx(); x++ {
			if !image.Pt(x, y).In(bounds) && pixels[4*(y*parent.Bounds().Dx()+x)+3] != 0 {
				t.Fatalf("draw escaped destination at (%d,%d)", x, y)
			}
		}
	}
}

// Like the other draw tests, these need a working display and a game loop.
func TestMetaballOverlapPixels(t *testing.T) {
	if os.Getenv("METABALLS_DRAW_TESTS") != "1" {
		t.Skip("METABALLS_DRAW_TESTS not enabled")
	}
	const helper = "METABALLS_OVERLAP_TEST"
	if os.Getenv(helper) != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestMetaballOverlapPixels$", "-test.v")
		cmd.Env = append(os.Environ(), helper+"=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("overlap pixel tests: %v\n%s", err, output)
		}
		return
	}
	if err := ebiten.RunGame(&overlapTestGame{t: t}); err != nil {
		t.Fatal(err)
	}
}

type overlapTestGame struct{ t *testing.T }

func (*overlapTestGame) Layout(int, int) (int, int) { return 64, 64 }
func (*overlapTestGame) Draw(*ebiten.Image)         {}
func (g *overlapTestGame) Update() error {
	g.t.Run("field gradients", checkOverlapFieldGradients)
	red, green, blue := NewColorScale(1, 0, 0, 1), NewColorScale(0, 1, 0, 1), NewColorScale(0, 0, 1, 1)
	scenes := map[string][]Group{
		"surrounded": {
			{Circles: []Circle{{X: 128.5, Y: 128.5, Radius: 16}}, Color: red},
			{Circles: []Circle{{X: 80.5, Y: 128.5, Radius: 52}}, Color: green},
			{Circles: []Circle{{X: 176.5, Y: 128.5, Radius: 52}}, Color: blue},
			{Circles: []Circle{{X: 128.5, Y: 80.5, Radius: 52}}, Color: green},
			{Circles: []Circle{{X: 128.5, Y: 176.5, Radius: 52}}, Color: blue},
		},
		"surrounded by blended groups": {
			{Circles: []Circle{{X: 128.5, Y: 128.5, Radius: 16}}, Color: red},
			{Circles: []Circle{{X: 80.5, Y: 128.5, Radius: 52}, {X: 128.5, Y: 80.5, Radius: 52}}, Color: green},
			{Circles: []Circle{{X: 176.5, Y: 128.5, Radius: 52}, {X: 128.5, Y: 176.5, Radius: 52}}, Color: blue},
		},
		"bulge": {{Circles: []Circle{{X: 96.5, Y: 128.5, Radius: 24}, {X: 160.5, Y: 128.5, Radius: 24}}, Color: red}},
		"bridge": {
			{Circles: []Circle{{X: 64.5, Y: 128.5, Radius: 22}, {X: 192.5, Y: 128.5, Radius: 30}}, Bridges: []Bridge{{A: 0, B: 1, MiddleRadius: 18}}, Color: red},
			{Circles: []Circle{{X: 128.5, Y: 108.5, Radius: 25}}, Color: green},
		},
		"equal seam": {
			{Circles: []Circle{{X: 112.5, Y: 128.5, Radius: 32}}, Color: red},
			{Circles: []Circle{{X: 144.5, Y: 128.5, Radius: 32}}, Color: green},
		},
	}
	// Deep overlaps also exercise silhouettes when a group is fully squeezed out.
	dense := []Group{{Circles: []Circle{{X: 128.5, Y: 128.5, Radius: 8}}, Color: red}}
	for i := range 8 {
		angle := float64(i) * math.Pi / 4
		color := green
		if i%2 != 0 {
			color = blue
		}
		dense = append(dense, Group{Circles: []Circle{{
			X: 128.5 + 40*float32(math.Cos(angle)),
			Y: 128.5 + 40*float32(math.Sin(angle)), Radius: 52,
		}}, Color: color})
	}
	scenes["dense overlap"] = dense
	for name, groups := range scenes {
		g.t.Run(name, func(t *testing.T) {
			var basic []byte
			for _, edge := range []bool{false, true} {
				pixels := renderOverlapPixels(t, groups, edge)
				if edge {
					for i := 3; i < len(pixels); i += 4 {
						if pixels[i] != basic[i] {
							t.Fatalf("basic/edge silhouettes differ at pixel %d", i/4)
						}
					}
				} else {
					basic = pixels
				}
				if name == "surrounded" || name == "surrounded by blended groups" {
					if pixels[4*(128*256+128)] == 0 {
						t.Fatalf("edge=%t: the deepest group at the center disappeared", edge)
					}
				}
				if name == "dense overlap" && pixels[4*(128*256+128)] != 0 {
					t.Fatalf("edge=%t: a shallower circle won over the surrounding group fields", edge)
				}
				if name == "equal seam" {
					// The contact rolls inward near its ends; hard ownership cuts
					// leave this point filled and make the transition angular.
					if pixels[4*(100*256+126)+3] != 0 {
						t.Fatal("contact endpoint lost its rounded inset")
					}
					if pixels[4*(128*256+120)+3] != 255 {
						t.Fatal("contact removed the circle interior")
					}
				}
				if name == "bulge" && pixels[4*(128*256+128)+3] != 255 {
					t.Fatal("long-distance bridge between separated circles disappeared")
				}
			}
		})
	}
	return ebiten.Termination
}

func renderOverlapPixels(t *testing.T, groups []Group, edge bool) []byte {
	t.Helper()
	cfg := ShaderConfig{ShaderCapacity: CapacityForGroups(groups), ShaderCommonConfig: ShaderCommonConfig{SmoothK: 40}}
	if edge {
		cfg.LightDirX, cfg.LightDirY, cfg.EdgeThickness = 1, -1, 8
	}
	shader, err := NewMetaballShader(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer shader.shader.Deallocate()
	dst := ebiten.NewImage(256, 256)
	defer dst.Deallocate()
	var transform UVTransform
	transform.SetScale(1, 1)
	if err := shader.Draw(dst, groups, transform); err != nil {
		t.Fatal(err)
	}
	pixels := make([]byte, 256*256*4)
	dst.ReadPixels(pixels)
	return pixels
}

// Exercise the actual shader helpers: distance must increase monotonically,
// and analytic distance gradients must agree with finite differences.
func checkOverlapFieldGradients(t *testing.T) {
	t.Helper()
	cfg := ShaderConfig{ShaderCapacity: ShaderCapacity{Groups: 1, Circles: 1}, ShaderCommonConfig: ShaderCommonConfig{SmoothK: 1, LightDirX: 1, EdgeThickness: 0.1}}
	var basic, edge bytes.Buffer
	if err := kageTemplates.ExecuteTemplate(&basic, "basic.fields", cfg); err != nil {
		t.Fatal(err)
	}
	if err := kageTemplates.ExecuteTemplate(&edge, "edge.fields", cfg); err != nil {
		t.Fatal(err)
	}
	helper := func(src string) string {
		start, end := strings.Index(src, "func sminField("), strings.Index(src, "func sdCircleField(")
		if start < 0 || end <= start {
			t.Fatal("cannot locate smooth-union shader helper")
		}
		return src[start:end]
	}
	src := "//kage:unit pixels\npackage main\n" + strings.ReplaceAll(helper(basic.String()), "sminField", "basicSminField") + helper(edge.String()) + `
func sample(a, b float) vec2 {
 return basicSminField(vec4(0.0, 0.0, a, 3.0+0.1*a), vec4(0.0, 0.0, b, 4.0+0.2*b), 1.0).zw
}
func Fragment(pos vec4) vec4 {
 p := (pos.xy-imageDstOrigin())/32.0 - 2.0
 a, b := p.x, p.y
 field := sminField(
  vec4(1.0, 0.0, a, 3.0+0.1*a),
  vec4(0.0, 1.0, b, 4.0+0.2*b), 1.0,
 )
 if min(field.x, field.y) < -0.0001 {
  return vec4(1.0, 0.0, 0.0, 1.0)
 }
 if field.z < -field.w-0.0001 {
  return vec4(0.0, 1.0, 0.0, 1.0)
 }
 e := 0.001
 dx := (sample(a+e, b)-sample(a-e, b))/(2.0*e)
 dy := (sample(a, b+e)-sample(a, b-e))/(2.0*e)
 if abs(dx.x-field.x) > 0.005 || abs(dy.x-field.y) > 0.005 {
  return vec4(0.0, 0.0, 1.0, 1.0)
 }
 if length(sample(a, b)-field.zw) > 0.0001 {
  return vec4(1.0, 1.0, 0.0, 1.0)
 }
 return vec4(0.0, 0.0, 0.0, 1.0)
}
`
	shader, err := ebiten.NewShader([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	defer shader.Deallocate()
	dst := ebiten.NewImage(128, 128)
	defer dst.Deallocate()
	dst.DrawRectShader(128, 128, shader, nil)
	pixels := make([]byte, 4*128*128)
	dst.ReadPixels(pixels)
	for i := 0; i < len(pixels); i += 4 {
		if pixels[i] != 0 || pixels[i+1] != 0 || pixels[i+2] != 0 || pixels[i+3] != 255 {
			t.Fatalf("field check at (%d,%d): RGBA %v (red=nonmonotone, green=depth below -1, blue=gradient mismatch, yellow=basic/edge mismatch)", i/4%128, i/4/128, pixels[i:i+4])
		}
	}
}

func TestMetaballOwnershipPixels(t *testing.T) {
	if os.Getenv("METABALLS_DRAW_TESTS") != "1" {
		t.Skip("METABALLS_DRAW_TESTS not enabled")
	}
	const helper = "METABALLS_OWNERSHIP_TEST"
	if os.Getenv(helper) != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestMetaballOwnershipPixels$", "-test.timeout=60s", "-test.v")
		cmd.Env = append(os.Environ(), helper+"=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("ownership pixel tests: %v\n%s", err, output)
		}
		return
	}
	if err := ebiten.RunGame(&ownershipTestGame{t: t}); err != nil {
		t.Fatal(err)
	}
}

type ownershipTestGame struct{ t *testing.T }

func (*ownershipTestGame) Layout(int, int) (int, int) { return 64, 64 }
func (*ownershipTestGame) Draw(*ebiten.Image)         {}
func (g *ownershipTestGame) Update() error {
	for _, edge := range []bool{false, true} {
		g.t.Run(fmt.Sprintf("edge=%t", edge), func(t *testing.T) {
			t.Run("inactive group fields", func(t *testing.T) { checkInactiveGroupFields(t, edge) })
			t.Run("tied competitors", func(t *testing.T) { checkTiedCompetitors(t, edge) })
			t.Run("duplicate competitors", func(t *testing.T) { checkOwnershipDuplicates(t, edge) })
			t.Run("disjoint groups", func(t *testing.T) { checkOwnershipPartition(t, edge) })
			t.Run("separate competing groups", func(t *testing.T) { checkSeparateGroupFields(t, edge) })
			t.Run("long range bulge", func(t *testing.T) { checkOwnershipBulge(t, edge) })
			t.Run("distant competitor", func(t *testing.T) { checkOwnershipDistantCompetitor(t, edge) })
			t.Run("equal depth contact", func(t *testing.T) { checkContactDepth(t, edge) })
		})
	}
	return ebiten.Termination
}

// Single-group specialization and neutral competitors must agree, including
// groups whose primitives produce no field at some or all pixels.
func checkInactiveGroupFields(t *testing.T, edge bool) {
	t.Helper()
	inactive := Group{Circles: []Circle{{X: 32, Y: 32}, {X: 32, Y: 32, Radius: -8}}, Color: NewColorScale(0, 1, 0, 1)}
	scenes := map[string]Group{
		"circle":          {Circles: []Circle{{X: 32, Y: 32, Radius: 16}}},
		"zero radius":     {Circles: []Circle{{X: 32, Y: 32}}},
		"negative radius": inactive,
		"bridge with zero endpoints": {
			Circles: []Circle{{X: 8, Y: 32}, {X: 56, Y: 32}},
			Bridges: []Bridge{{A: 0, B: 1, MiddleRadius: 12}},
		},
		"bridge with negative middle": {
			Circles: []Circle{{X: 8, Y: 32, Radius: 8}, {X: 56, Y: 32, Radius: 8}},
			Bridges: []Bridge{{A: 0, B: 1, MiddleRadius: -8}},
		},
	}
	var xf UVTransform
	xf.SetScale(1, 1)
	for name, scene := range scenes {
		t.Run(name, func(t *testing.T) {
			scene.Color = NewColorScale(1, 0, 0, 1)
			var want []byte
			for variant := range 3 {
				cfg := ShaderConfig{ShaderCapacity: ShaderCapacity{Groups: 1, Circles: 6, Bridges: 1}, ShaderCommonConfig: ShaderCommonConfig{SmoothK: 12}}
				groups := []Group{scene}
				if variant > 0 {
					cfg.Groups = 3
				}
				if variant == 2 {
					groups = []Group{inactive, scene, inactive}
				}
				if edge {
					cfg.LightDirX, cfg.LightDirY, cfg.EdgeThickness = 1, -1, 4
				}
				shader, err := NewMetaballShader(cfg)
				if err != nil {
					t.Fatal(err)
				}
				defer shader.shader.Deallocate()
				dst := ebiten.NewImage(64, 64)
				defer dst.Deallocate()
				if err := shader.Draw(dst, groups, xf); err != nil {
					t.Fatal(err)
				}
				pixels := make([]byte, 64*64*4)
				dst.ReadPixels(pixels)
				if variant == 0 {
					want = pixels
				} else if !bytes.Equal(pixels, want) {
					t.Fatalf("variant %d differs from the single-group shader", variant)
				}
			}
		})
	}
}

// Equal-distance runners-up have different gradients. Keep the earlier one's
// lighting regardless of when the winning group enters the selection scan.
func checkTiedCompetitors(t *testing.T, edge bool) {
	t.Helper()
	main := Group{Circles: []Circle{{X: 128.5, Y: 128.5, Radius: 12}}, Color: NewColorScale(1, 0, 0, 1)}
	left := Group{Circles: []Circle{{X: 112.5, Y: 128.5, Radius: 26}}, Color: NewColorScale(0, 1, 0, 1)}
	right := Group{Circles: []Circle{{X: 144.5, Y: 128.5, Radius: 26}}, Color: NewColorScale(0, 0, 1, 1)}
	const center = 4 * (128*256 + 128)
	for _, competitors := range [][2]Group{{left, right}, {right, left}} {
		want := renderOwnershipPixels(t, []Group{main, competitors[0]}, edge, 8, -1)
		if want[center] == 0 || want[center+3] != 255 {
			t.Fatal("test scene has no visible winner at the tied pixel")
		}
		for winner := range 3 {
			groups := append([]Group(nil), competitors[:winner]...)
			groups = append(groups, main)
			groups = append(groups, competitors[winner:]...)
			got := renderOwnershipPixels(t, groups, edge, 8, -1)
			if !bytes.Equal(got[center:center+4], want[center:center+4]) {
				t.Fatalf("winner at index %d: tied pixel = %v, want %v", winner, got[center:center+4], want[center:center+4])
			}
		}
	}
	pixels := renderOwnershipPixels(t, []Group{main, main}, edge, 8, -1)
	for i := 3; i < len(pixels); i += 4 {
		if pixels[i] != 0 {
			t.Fatalf("coincident tied winners left a visible pixel at (%d,%d)", i/4%256, i/4/256)
		}
	}
}

func checkContactDepth(t *testing.T, edge bool) {
	groups := []Group{
		{Circles: []Circle{{X: 80.5, Y: 128.5, Radius: 80}}, Color: NewColorScale(1, 0, 0, 1)},
		{Circles: []Circle{{X: 160.5, Y: 128.5, Radius: 20}}, Color: NewColorScale(0, 0, 1, 1)},
	}
	for _, k := range []float32{8, 40} {
		pixels := renderOwnershipPixels(t, groups, edge, k, -1)
		// The 20-unit overlap splits equally at x=150.5. Contact follows
		// the group distances, without a separate radius-based pressure bias.
		for _, p := range []struct{ x, channel int }{{80, 0}, {149, 0}, {151, 2}, {160, 2}} {
			i := 4 * (128*256 + p.x)
			if pixels[i+p.channel] == 0 || pixels[i+3] != 255 || pixels[i+2-p.channel] != 0 {
				t.Fatalf("K=%g: pixel (%d,128) = %v, want color channel %d", k, p.x, pixels[i:i+4], p.channel)
			}
		}
	}
}

func checkOwnershipDuplicates(t *testing.T, edge bool) {
	main := Group{Circles: []Circle{{X: 112.5, Y: 128.5, Radius: 28}}, Color: NewColorScale(1, 0, 0, 1)}
	other := Group{
		Circles: []Circle{{X: 130.5, Y: 82.5, Radius: 32}, {X: 158.5, Y: 172.5, Radius: 40}},
		Bridges: []Bridge{{A: 0, B: 1, MiddleRadius: 24}},
		Color:   NewColorScale(0, 1, 0, 1),
	}
	want := renderOwnershipPixels(t, []Group{main, other}, edge, 80, 0)
	groups := []Group{main}
	for range 8 {
		groups = append(groups, other)
	}
	got := renderOwnershipPixels(t, groups, edge, 80, 0)
	if !bytes.Equal(got, want) {
		t.Fatal("duplicating competing circles and bridges changed the main pass")
	}
	visible := false
	for i := 3; i < len(want); i += 4 {
		visible = visible || want[i] != 0
	}
	if !visible {
		t.Fatal("duplicate-competitor scene has no visible main pixels")
	}
}

func checkOwnershipPartition(t *testing.T, edge bool) {
	groups := []Group{
		{
			Circles: []Circle{{X: 54.5, Y: 114.5, Radius: 22}, {X: 200.5, Y: 114.5, Radius: 30}},
			Bridges: []Bridge{{A: 0, B: 1, MiddleRadius: 18}},
			Color:   NewColorScale(1, 0, 0, 1),
		},
		{
			Circles: []Circle{{X: 130.5, Y: 76.5, Radius: 38}, {X: 168.5, Y: 164.5, Radius: 17}},
			Color:   NewColorScale(0, 1, 0, 1),
		},
		{
			Circles: []Circle{{X: 90.5, Y: 168.5, Radius: 25}, {X: 184.5, Y: 76.5, Radius: 16}},
			Bridges: []Bridge{{A: 0, B: 1, MiddleRadius: 12}},
			Color:   NewColorScale(0, 0, 1, 1),
		},
	}
	for _, k := range []float32{40, 400} {
		t.Run(fmt.Sprintf("K=%g", k), func(t *testing.T) {
			occupied := make([]bool, 256*256)
			for pass := range groups {
				pixels := renderOwnershipPixels(t, groups, edge, k, pass)
				visible := 0
				for pixel := range occupied {
					if pixels[4*pixel+3] == 0 {
						continue
					}
					if occupied[pixel] {
						t.Fatalf("group %d overlaps an earlier pass at (%d,%d)", pass, pixel%256, pixel/256)
					}
					occupied[pixel] = true
					visible++
				}
				if visible == 0 && k == 40 {
					t.Fatalf("group %d disappeared from the partition scene", pass)
				}
			}
			forward := renderOwnershipPixels(t, groups, edge, k, -1)
			reversed := []Group{groups[2], groups[1], groups[0]}
			backward := renderOwnershipPixels(t, reversed, edge, k, -1)
			if !bytes.Equal(forward, backward) {
				t.Fatal("reversing group draw order changed pixels")
			}
		})
	}
}

// Keeping competing colors separate must avoid the artificial extra depth
// produced by smooth-unioning all their circles into a single opposing blob.
func checkSeparateGroupFields(t *testing.T, edge bool) {
	main := Group{Circles: []Circle{{X: 128.5, Y: 128.5, Radius: 16}}, Color: NewColorScale(1, 0, 0, 1)}
	groups := []Group{main}
	merged := Group{Color: NewColorScale(0, 1, 0, 1)}
	for i := range 8 {
		angle := float64(i) * math.Pi / 4
		c := Circle{X: 128.5 + 48*float32(math.Cos(angle)), Y: 128.5 + 48*float32(math.Sin(angle)), Radius: 52}
		groups = append(groups, Group{Circles: []Circle{c}, Color: merged.Color})
		merged.Circles = append(merged.Circles, c)
	}
	separatePixels := renderOwnershipPixels(t, groups, edge, 40, 0)
	mergedPixels := renderOwnershipPixels(t, []Group{main, merged}, edge, 40, 0)
	center := 4*(128*256+128) + 3
	if separatePixels[center] != 255 {
		t.Fatal("separate competing groups erased the central circle")
	}
	if mergedPixels[center] != 0 {
		t.Fatal("test scene did not distinguish separate groups from a combined opposing field")
	}
}

func checkOwnershipBulge(t *testing.T, edge bool) {
	groups := []Group{{
		Circles: []Circle{{X: 80.5, Y: 128.5, Radius: 12}, {X: 176.5, Y: 128.5, Radius: 12}},
		Color:   NewColorScale(1, 0, 0, 1),
	}}
	for _, tc := range []struct {
		k     float32
		alpha byte
	}{{8, 0}, {160, 255}} {
		pixels := renderOwnershipPixels(t, groups, edge, tc.k, -1)
		if got := pixels[4*(128*256+128)+3]; got != tc.alpha {
			t.Fatalf("K=%g: midpoint alpha = %d, want %d", tc.k, got, tc.alpha)
		}
	}
}

func checkOwnershipDistantCompetitor(t *testing.T, edge bool) {
	main := Group{
		Circles: []Circle{{X: 80.5, Y: 128.5, Radius: 12}, {X: 176.5, Y: 128.5, Radius: 12}},
		Color:   NewColorScale(1, 0, 0, 1),
	}
	// Its surface is more than K away from every destination pixel, but its
	// huge radius makes d/r small. A renderer tile can safely omit this circle.
	distant := Group{Circles: []Circle{{X: 11000, Y: 128.5, Radius: 10000}}}
	want := renderOwnershipPixels(t, []Group{main}, edge, 160, 0)
	got := renderOwnershipPixels(t, []Group{main, distant}, edge, 160, 0)
	if !bytes.Equal(got, want) {
		t.Fatal("a competing surface farther than K changed the visible main field")
	}
	// A crowded union can bulge almost K beyond its primitives. A distant
	// competitor must not shave off that bulge through contact rounding.
	main.Circles = make([]Circle, 32)
	for i := range main.Circles {
		main.Circles[i] = Circle{X: -25.5, Y: 128.5, Radius: 12}
	}
	distant.Circles[0].X = 10289.5
	want = renderOwnershipPixels(t, []Group{main}, edge, 160, 0)
	got = renderOwnershipPixels(t, []Group{main, distant}, edge, 160, 0)
	if want[4*(128*256+128)+3] != 255 {
		t.Fatal("dense union did not exercise its near-K exterior bulge")
	}
	if !bytes.Equal(got, want) {
		t.Fatal("a competing surface beyond K trimmed the dense union's bulge")
	}
}

// A nonnegative pass makes competing groups transparent while preserving
// their fields, so a single draw yields just the selected group's coverage.
func renderOwnershipPixels(t *testing.T, groups []Group, edge bool, k float32, pass int) []byte {
	t.Helper()
	cfg := ShaderConfig{ShaderCapacity: CapacityForGroups(groups), ShaderCommonConfig: ShaderCommonConfig{SmoothK: k}}
	if edge {
		cfg.LightDirX, cfg.LightDirY, cfg.EdgeThickness = 1, -1, 8
	}
	shader, err := NewMetaballShader(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer shader.shader.Deallocate()
	dst := ebiten.NewImage(256, 256)
	defer dst.Deallocate()
	var transform UVTransform
	transform.SetScale(1, 1)
	if pass >= 0 {
		groups = append([]Group(nil), groups...)
		for i := range groups {
			if i != pass {
				groups[i].Color = NewColorScale(0, 0, 0, 0)
			}
		}
	}
	err = shader.Draw(dst, groups, transform)
	if err != nil {
		t.Fatal(err)
	}
	pixels := make([]byte, 256*256*4)
	dst.ReadPixels(pixels)
	return pixels
}

const squeezeGPUReady = "METABALLS_SQUEEZE_GPU_READY"

// Check the gradients used for edge lighting against finite differences of
// the actual shader distance functions. A separate process owns the game loop.
func TestMetaballSqueezeGradients(t *testing.T) {
	if os.Getenv("METABALLS_DRAW_TESTS") != "1" {
		t.Skip("METABALLS_DRAW_TESTS not enabled")
	}
	const helper = "METABALLS_SQUEEZE_TEST"
	if os.Getenv(helper) != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestMetaballSqueezeGradients$", "-test.v")
		cmd.Env = append(os.Environ(), helper+"=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && !bytes.Contains(output, []byte(squeezeGPUReady)) {
				t.Skipf("GPU unavailable: %v\n%s", err, output)
			}
			t.Fatalf("squeeze gradient tests: %v\n%s", err, output)
		}
		return
	}
	if err := ebiten.RunGame(&squeezeGradientGame{t: t}); err != nil {
		t.Fatal(err)
	}
}

type squeezeGradientGame struct{ t *testing.T }

func (*squeezeGradientGame) Layout(int, int) (int, int) { return 64, 64 }
func (*squeezeGradientGame) Draw(*ebiten.Image)         {}
func (g *squeezeGradientGame) Update() error {
	probe := ebiten.NewImage(1, 1)
	probe.ReadPixels(make([]byte, 4))
	probe.Deallocate()
	fmt.Println(squeezeGPUReady)

	const squeezeSample = `
func sample(p vec2, k float) vec3 {
 main := vec4(0.3,-0.2,0.3*p.x-0.2*p.y-0.15,0.5)
 other := vec4(-0.4,0.6,-0.4*p.x+0.6*p.y,0.4)
 return squeezeField(main,other,k).xyz
}
`
	const bridgeSample = `
func sample(p vec2, k float) vec3 {
 return sdSplineCapsuleField(p,vec4(-1.0,0.0,1.0,0.0),vec4(0.2,0.5,0.3,0.0)).xyz
}
`
	const groupSample = `
func sample(p vec2, k float) vec3 {
 a := sdCircleField(p,vec4(-0.4,0.0,0.6,0.0))
 b := sdCircleField(p,vec4(0.4,0.0,0.6,0.0))
 return sminField(a,b,k).xyz
}
`
	const bridgeContactSample = `
func sample(p vec2, k float) vec3 {
 field := sdSplineCapsuleField(p,vec4(-1.0,0.0,1.0,0.0),vec4(0.2,0.5,0.3,0.0))
 other := sdCircleField(p,vec4(0.3,0.4,0.5,0.0))
 return squeezeField(field,other,k).xyz
}
`
	for _, test := range []struct {
		name, sample, position string
		k                      float32
	}{
		{
			name: "squeeze transition and both clamped regions", sample: squeezeSample,
			position: "(pos.xy-imageDstOrigin())/32.0-2.0", k: 0.6,
		},
		{
			name: "wide squeeze transition", sample: squeezeSample,
			position: "(pos.xy-imageDstOrigin())/32.0-2.0", k: 2,
		},
		{
			name: "tapered bridge contact", sample: bridgeContactSample,
			position: "(pos.xy-imageDstOrigin())*vec2(3.0,1.6)/128.0+vec2(-1.5,0.03)", k: 0.6,
		},
		{
			name: "tapered bridge distance", sample: bridgeSample,
			position: "(pos.xy-imageDstOrigin())*vec2(3.0,1.2)/128.0+vec2(-1.5,0.12)", k: 0.6,
		},
		{
			name: "smooth group distance across primitive bisector", sample: groupSample,
			position: "(pos.xy-imageDstOrigin())*vec2(0.00002,0.006)-vec2(0.00128,-0.15)", k: 0.6,
		},
	} {
		g.t.Run(test.name, func(t *testing.T) {
			checkSqueezeShaderGradient(t, test.sample, test.position, test.k)
		})
	}
	return ebiten.Termination
}

func checkSqueezeShaderGradient(t *testing.T, sample, position string, k float32) {
	t.Helper()
	cfg := ShaderConfig{
		ShaderCapacity:     ShaderCapacity{Groups: 2, Circles: 2, Bridges: 1},
		ShaderCommonConfig: ShaderCommonConfig{SmoothK: 1, LightDirX: 1, EdgeThickness: 0.1},
	}
	var rendered bytes.Buffer
	if err := kageTemplates.ExecuteTemplate(&rendered, mainShaderTemplate, cfg); err != nil {
		t.Fatal(err)
	}
	template := rendered.String()
	start, end := strings.Index(template, "func sminField("), strings.Index(template, "func edge(")
	if start < 0 || end <= start {
		t.Fatal("cannot locate edge shader field helpers")
	}
	source := "//kage:unit pixels\npackage main\n" + template[start:end] + sample + fmt.Sprintf(`
func Fragment(pos vec4) vec4 {
 p := %s
 k := %.8f
 sampled := sample(p, k)
 e := 0.001
 dx := (sample(p+vec2(e, 0.0), k).z-sample(p-vec2(e, 0.0), k).z)/(2.0*e)
 dy := (sample(p+vec2(0.0, e), k).z-sample(p-vec2(0.0, e), k).z)/(2.0*e)
 if abs(sampled.x-dx) > 0.005+0.002*abs(dx) { return vec4(1.0, 0.0, 0.0, 1.0) }
 if abs(sampled.y-dy) > 0.005+0.002*abs(dy) { return vec4(0.0, 1.0, 0.0, 1.0) }
 return vec4(0.0, 0.0, 0.0, 1.0)
}
`, position, k)
	shader, err := ebiten.NewShader([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	defer shader.Deallocate()
	dst := ebiten.NewImage(128, 128)
	defer dst.Deallocate()
	dst.DrawRectShader(128, 128, shader, nil)
	pixels := make([]byte, 4*128*128)
	dst.ReadPixels(pixels)
	for i := 0; i < len(pixels); i += 4 {
		if pixels[i] != 0 || pixels[i+1] != 0 || pixels[i+2] != 0 || pixels[i+3] != 255 {
			t.Fatalf("gradient mismatch at (%d,%d): RGBA %v (red=x derivative, green=y derivative)", i/4%128, i/4/128, pixels[i:i+4])
		}
	}
}

func TestFXAATransparency(t *testing.T) {
	if os.Getenv("METABALLS_DRAW_TESTS") != "1" {
		t.Skip("METABALLS_DRAW_TESTS not enabled")
	}
	// Pixel readback needs a game loop, which can only run once per process.
	const helperEnv = "METABALLS_FXAA_TEST"
	if os.Getenv(helperEnv) != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestFXAATransparency$", "-test.v")
		cmd.Env = append(os.Environ(), helperEnv+"=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("FXAA pixel tests: %v\n%s", err, output)
		}
		return
	}
	if err := ebiten.RunGame(&fxaaTestGame{t: t}); err != nil {
		t.Fatal(err)
	}
}

type fxaaTestGame struct{ t *testing.T }

func (*fxaaTestGame) Layout(int, int) (int, int) { return 32, 32 }
func (*fxaaTestGame) Draw(*ebiten.Image)         {}
func (g *fxaaTestGame) Update() error {
	var source bytes.Buffer
	if err := kageTemplates.ExecuteTemplate(&source, fxaaShaderTemplate, ShaderCommonConfig{}); err != nil {
		g.t.Fatal(err)
	}
	shader, err := ebiten.NewShader(source.Bytes())
	if err != nil {
		g.t.Fatal(err)
	}
	defer shader.Deallocate()
	for _, alpha := range []byte{0, 128, 255} {
		g.t.Run(fmt.Sprintf("alpha=%d", alpha), func(t *testing.T) {
			const size = 32
			pixels := make([]byte, size*size*4)
			// Premultiplied red with a sloped boundary exercises filtered edges.
			for y := 0; y < size; y++ {
				for x := 0; x < size; x++ {
					if x < 8+y/2 {
						i := 4 * (y*size + x)
						pixels[i], pixels[i+3] = alpha, alpha
					}
				}
			}
			src, dst := ebiten.NewImage(size, size), ebiten.NewImage(size, size)
			defer src.Deallocate()
			defer dst.Deallocate()
			src.WritePixels(pixels)
			opts := &ebiten.DrawRectShaderOptions{Images: [4]*ebiten.Image{src}}
			dst.DrawRectShader(size, size, shader, opts)
			dst.ReadPixels(pixels)
			for i := 0; i < len(pixels); i += 4 {
				if pixels[i] != pixels[i+3] || pixels[i+1] != 0 || pixels[i+2] != 0 || pixels[i+3] > alpha {
					t.Fatalf("pixel %d lost premultiplied alpha: %v", i/4, pixels[i:i+4])
				}
			}
			for _, index := range []int{0, 31 * size, 16*size + 4} {
				if got := pixels[4*index+3]; got != alpha {
					t.Fatalf("solid pixel %d alpha = %d, want %d", index, got, alpha)
				}
			}
			if got := pixels[4*(16*size+28)+3]; got != 0 {
				t.Fatalf("background alpha = %d, want 0", got)
			}

			// Drawing over blue must preserve the background and blend edges.
			dst.Fill(color.RGBA{B: 255, A: 255})
			dst.DrawRectShader(size, size, shader, opts)
			composited := make([]byte, len(pixels))
			dst.ReadPixels(composited)
			for i := 0; i < len(pixels); i += 4 {
				want := []byte{pixels[i], 0, 255 - pixels[i+3], 255}
				for c := range want {
					if diff := int(composited[i+c]) - int(want[c]); diff < -1 || diff > 1 {
						t.Fatalf("composited pixel %d = %v, want %v", i/4, composited[i:i+4], want)
					}
				}
			}
		})
	}
	return ebiten.Termination
}
