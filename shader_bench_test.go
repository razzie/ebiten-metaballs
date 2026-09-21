package metaballs

import (
	"bytes"
	"fmt"
	"image"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
)

// Run explicitly with:
// METABALLS_SHADER_BENCHMARK=1 go test . -run '^TestMetaballSelectionBenchmark$' -count=1 -timeout=10m -v
//
// This uses paired wall-clock samples inside a game loop, rather than timing
// Draw alone (which only queues GPU work). Each batch ends with pixel readback.
// Results include CPU submission, GPU rendering, and amortized synchronization.
func TestMetaballSelectionBenchmark(t *testing.T) {
	if os.Getenv("METABALLS_SHADER_BENCHMARK") != "1" {
		t.Skip("METABALLS_SHADER_BENCHMARK not enabled")
	}
	const child = "METABALLS_SHADER_BENCHMARK_CHILD"
	if os.Getenv(child) != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestMetaballSelectionBenchmark$", "-test.count=1", "-test.timeout=10m", "-test.v")
		cmd.Env = append(os.Environ(), child+"=1")
		output, err := cmd.CombinedOutput()
		t.Logf("\n%s", output)
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	ebiten.SetVsyncEnabled(false)
	ebiten.SetRunnableOnUnfocused(true)
	if err := ebiten.RunGameWithOptions(&selectionBenchmarkGame{t}, &ebiten.RunGameOptions{InitUnfocused: true}); err != nil {
		t.Fatal(err)
	}
}

type selectionBenchmarkGame struct{ t *testing.T }

func (*selectionBenchmarkGame) Layout(int, int) (int, int) { return 32, 32 }
func (*selectionBenchmarkGame) Draw(*ebiten.Image)         {}

func (g *selectionBenchmarkGame) Update() error {
	var info ebiten.DebugInfo
	ebiten.ReadDebugInfo(&info)
	fmt.Printf("backend=%s go=%s os=%s arch=%s resolution=900x900\n", info.GraphicsLibrary, runtime.Version(), runtime.GOOS, runtime.GOARCH)
	fmt.Println("15 paired samples; alternating order; median ms/frame; ratio < 1 favors branchless")
	fmt.Println("case,frames/batch,branched_ms,branchless_ms,median_ratio,ratio_p10,ratio_p90,pixel_channel_differences")
	scattered := selectionBenchmarkScene(3, 36, false)
	clustered := selectionBenchmarkScene(3, 36, true)
	for _, scene := range []struct {
		name   string
		groups []Group
		edge   bool
		tiled  bool
	}{
		{"physics-scattered-direct-basic", scattered, false, false},
		{"physics-scattered-direct-edge", scattered, true, false},
		{"physics-scattered-tiled-edge-fxaa", scattered, true, true},
		{"physics-clustered-tiled-edge-fxaa", clustered, true, true},
		{"3-groups-3-circles-edge", selectionBenchmarkScene(3, 1, true), true, false},
		{"8-groups-32-circles-basic", selectionBenchmarkScene(8, 4, true), false, false},
		{"8-groups-32-circles-edge", selectionBenchmarkScene(8, 4, true), true, false},
		{"16-groups-64-circles-basic", selectionBenchmarkScene(16, 4, true), false, false},
		{"16-groups-64-circles-edge", selectionBenchmarkScene(16, 4, true), true, false},
	} {
		g.t.Run(scene.name, func(t *testing.T) {
			cfg := ShaderConfig{ShaderCapacity: CapacityForGroups(scene.groups), ShaderCommonConfig: ShaderCommonConfig{SmoothK: 0.06}}
			if scene.edge {
				cfg.LightDirX, cfg.LightDirY, cfg.EdgeThickness = 1, -1, 0.008
			}
			branched := newSelectionBenchmarkFixture(t, cfg, scene.groups, scene.tiled, true)
			branchless := newSelectionBenchmarkFixture(t, cfg, scene.groups, scene.tiled, false)
			// Compile GPU programs, allocate buffers, and populate pools before timing.
			branched.batch(t, 1)
			branchless.batch(t, 1)
			a, b := make([]byte, 900*900*4), make([]byte, 900*900*4)
			branched.dst.ReadPixels(a)
			branchless.dst.ReadPixels(b)
			differences := 0
			for i := range a {
				if a[i] != b[i] {
					differences++
				}
			}
			// Use the same batch length for both variants, aiming for 75 ms.
			duration := max(branched.batch(t, 8), branchless.batch(t, 8)) / 8
			frames := min(512, max(4, int(75*time.Millisecond/duration)))
			for range 3 {
				branched.batch(t, frames)
				branchless.batch(t, frames)
			}
			var times [2][]float64
			ratios := make([]float64, 0, 15)
			fixtures := []*selectionBenchmarkFixture{branched, branchless}
			for round := range 15 {
				var pair [2]float64
				for step := range 2 {
					variant := (round + step) % 2
					pair[variant] = float64(fixtures[variant].batch(t, frames)) / float64(frames) / float64(time.Millisecond)
					times[variant] = append(times[variant], pair[variant])
				}
				ratios = append(ratios, pair[1]/pair[0])
			}
			slices.Sort(times[0])
			slices.Sort(times[1])
			slices.Sort(ratios)
			fmt.Printf("%s,%d,%.4f,%.4f,%.4f,%.4f,%.4f,%d\n", scene.name, frames, times[0][7], times[1][7], ratios[7], ratios[1], ratios[13], differences)
		})
	}
	return ebiten.Termination
}

type selectionBenchmarkFixture struct {
	dst  *ebiten.Image
	draw func() error
	read *ebiten.Image
}

func (f *selectionBenchmarkFixture) batch(t *testing.T, frames int) time.Duration {
	t.Helper()
	var pixel [4]byte
	start := time.Now()
	for range frames {
		f.dst.Clear()
		if err := f.draw(); err != nil {
			t.Fatal(err)
		}
	}
	// Reading the rendered target drains queued commands and waits for the GPU.
	f.read.ReadPixels(pixel[:])
	return time.Since(start)
}

func newSelectionBenchmarkFixture(t *testing.T, cfg ShaderConfig, groups []Group, tiled, branched bool) *selectionBenchmarkFixture {
	t.Helper()
	dst := ebiten.NewImageWithOptions(image.Rect(0, 0, 900, 900), &ebiten.NewImageOptions{Unmanaged: true})
	t.Cleanup(dst.Deallocate)
	f := &selectionBenchmarkFixture{dst: dst, read: dst.SubImage(image.Rect(0, 0, 1, 1)).(*ebiten.Image)}
	xf, _ := NewCenteredUVTransform(900, 900)
	if tiled {
		// Same renderer configuration as examples/physics; physics itself is not timed.
		cfg.FxaaEnabled = true
		r, err := NewRenderer(RendererConfig{
			Common:   cfg.ShaderCommonConfig,
			Tiers:    []ShaderCapacity{{Groups: 3, Circles: 48}, {Groups: 3, Circles: 108}},
			RootCols: 2, RootRows: 2, MaxDepth: 3, MinTileSize: 0.01, Workers: 4,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range r.shaders {
			prepareSelectionBenchmarkShader(t, s, branched)
		}
		t.Cleanup(func() {
			if r.offscreen != nil {
				r.offscreen.Deallocate()
			}
			if r.fxaa != nil {
				r.fxaa.Deallocate()
			}
		})
		f.draw = func() error {
			_, err := r.Draw(dst, groups, xf)
			return err
		}
	} else {
		s, err := NewMetaballShader(cfg)
		if err != nil {
			t.Fatal(err)
		}
		prepareSelectionBenchmarkShader(t, s, branched)
		f.draw = func() error { return s.Draw(dst, groups, xf) }
	}
	return f
}

// Only replace the group-selection block; primitive evaluation, lighting,
// uniforms, capacities, and the single-group optimization remain identical.
func prepareSelectionBenchmarkShader(t *testing.T, s *MetaballShader, branched bool) {
	t.Helper()
	if !branched {
		cfg := s.config
		if length := math.Hypot(float64(cfg.LightDirX), float64(cfg.LightDirY)); length > 0 {
			cfg.LightDirX = float32(float64(cfg.LightDirX) / length)
			cfg.LightDirY = float32(float64(cfg.LightDirY) / length)
		}
		var rendered bytes.Buffer
		if err := kageTemplates.ExecuteTemplate(&rendered, mainShaderTemplate, cfg); err != nil {
			t.Fatal(err)
		}
		source := rendered.String()
		start := strings.Index(source, "\t\tfield := fields[g]\n")
		end := strings.Index(source, "\n\t}\n\tif main.z >= 0.0")
		if start < 0 || end <= start {
			t.Fatal("cannot locate group selection block")
		}
		if !strings.Contains(source[start:end], "if field.w == 0.0") {
			t.Fatal("benchmark expects the branched selection in shader_main.kage.tmpl")
		}
		const branchlessSelection = `		field := fields[g]
		valid := 1.0 - step(field.w, 0.0)
		takeMain := valid * (1.0 - step(main.z, field.z))
		takeOther := (valid-takeMain) * (1.0 - step(other.z, field.z))
		// Exclusive weights avoid the rounding at a=1 possible with mix.
		other = other*(1.0-takeMain-takeOther) + main*takeMain + field*takeOther
		main = main*(1.0-takeMain) + field*takeMain
		selected += (g-selected)*int(takeMain)`
		shader, err := ebiten.NewShader([]byte(source[:start] + branchlessSelection + source[end:]))
		if err != nil {
			t.Fatal(err)
		}
		s.shader.Deallocate()
		s.shader = shader
	}
	t.Cleanup(s.shader.Deallocate)
}

func selectionBenchmarkScene(groupCount, circlesPerGroup int, clustered bool) []Group {
	rng := rand.New(rand.NewPCG(17, 29))
	groups := make([]Group, groupCount)
	for i := range groups {
		groups[i].Color = NewColorScale(0.3+0.7*rng.Float32(), 0.3+0.7*rng.Float32(), 0.3+0.7*rng.Float32(), 1)
	}
	var placed []Circle
	for i := range groupCount * circlesPerGroup {
		radius := 0.02 * (0.5 + rng.Float32())
		var c Circle
		for attempt := range 1000 {
			c = Circle{X: radius + rng.Float32()*(1-2*radius), Y: radius + rng.Float32()*(1-2*radius), Radius: radius}
			if clustered {
				// Overlapping groups create more per-pixel ownership variation.
				c.X, c.Y = 0.5+(c.X-0.5)*0.35, 0.5+(c.Y-0.5)*0.35
				break
			}
			clear := true
			for _, other := range placed {
				dx, dy, separation := c.X-other.X, c.Y-other.Y, c.Radius+other.Radius
				if dx*dx+dy*dy < separation*separation {
					clear = false
					break
				}
			}
			if clear {
				break
			}
			if attempt == 999 {
				panic("could not place benchmark circle")
			}
		}
		placed = append(placed, c)
		group := &groups[i%groupCount]
		group.Circles = append(group.Circles, c)
	}
	return groups
}
