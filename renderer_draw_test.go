package metaballs

import (
	"bytes"
	"os"
	"os/exec"
	"sync"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
)

func TestRendererParallelSubdivision(t *testing.T) {
	if os.Getenv("METABALLS_DRAW_TESTS") != "1" {
		t.Skip("METABALLS_DRAW_TESTS not enabled")
	}
	const helper = "METABALLS_SUBDIVISION_TEST"
	if os.Getenv(helper) != "1" {
		// A subprocess timeout reports blocked goroutines without leaving a
		// deadlocked renderer or Ebitengine loop behind in the parent test.
		cmd := exec.Command(os.Args[0], "-test.run=^TestRendererParallelSubdivision$", "-test.timeout=15s", "-test.v")
		cmd.Env = append(os.Environ(), helper+"=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("parallel subdivision: %v\n%s", err, output)
		}
		return
	}
	if err := ebiten.RunGame(&subdivisionTestGame{t: t}); err != nil {
		t.Fatal(err)
	}
}

type subdivisionTestGame struct{ t *testing.T }

func (*subdivisionTestGame) Layout(int, int) (int, int) { return 64, 64 }
func (*subdivisionTestGame) Draw(*ebiten.Image)         {}
func (g *subdivisionTestGame) Update() error {
	g.t.Run("concurrent outline buffers", checkConcurrentOutlineBuffers)
	for _, test := range []struct {
		name                  string
		workers, roots, depth int
		debug, fxaa           bool
	}{
		{name: "two workers", workers: 2, roots: 1, depth: 2},
		{name: "four workers", workers: 4, roots: 1, depth: 4},
		{name: "multiple roots", workers: 4, roots: 2, depth: 2},
		{name: "debug outlines", workers: 6, roots: 1, depth: 3, debug: true},
		{name: "debug and FXAA", workers: 4, roots: 2, depth: 2, debug: true, fxaa: true},
	} {
		g.t.Run(test.name, func(t *testing.T) {
			cfg := RendererConfig{
				Common:   ShaderCommonConfig{SmoothK: 0.04, LightDirX: 1, LightDirY: -1, EdgeThickness: 0.008, FxaaEnabled: test.fxaa},
				Tiers:    []ShaderCapacity{{Groups: 1, Circles: 1}},
				RootCols: test.roots, RootRows: test.roots, MaxDepth: test.depth, MinTileSize: 1.0 / 128, Debug: test.debug,
			}
			// Both circles cover every tile and exceed the tier, forcing each root
			// to recurse all the way to MaxDepth before the clipping fallback.
			groups := []Group{{Circles: []Circle{{X: 0.5, Y: 0.5, Radius: 2}, {X: 0.5, Y: 0.5, Radius: 2}}, Color: NewColorScale(1, 0.5, 0.25, 1)}}
			serial, wantStats := renderSubdivision(t, cfg, groups)
			cfg.Workers = test.workers
			for frame := 0; frame < 3; frame++ {
				parallel, gotStats := renderSubdivision(t, cfg, groups)
				if gotStats != wantStats {
					t.Fatalf("frame %d: parallel stats %v, serial %v", frame, gotStats, wantStats)
				}
				if !test.debug && !bytes.Equal(parallel, serial) {
					t.Fatalf("frame %d: parallel pixels differ from serial", frame)
				}
			}
			wantTiles := int32(test.roots * test.roots * (1 << (2 * test.depth)))
			if wantStats != [3]int32{wantTiles, 0, wantTiles} {
				t.Fatalf("did not exercise full subdivision: stats %v", wantStats)
			}
		})
	}
	return ebiten.Termination
}

func renderSubdivision(t *testing.T, cfg RendererConfig, groups []Group) ([]byte, [3]int32) {
	t.Helper()
	r, err := NewRenderer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range r.shaders {
		defer s.shader.Deallocate()
	}
	if r.fxaa != nil {
		defer r.fxaa.Deallocate()
		defer func() {
			if r.offscreen != nil {
				r.offscreen.Deallocate()
			}
		}()
	}
	dst := ebiten.NewImage(64, 64)
	defer dst.Deallocate()
	xform, _ := NewCenteredUVTransform(64, 64)
	stats, err := r.Draw(dst, groups, xform)
	if err != nil {
		t.Fatal(err)
	}
	pixels := make([]byte, 64*64*4)
	dst.ReadPixels(pixels)
	return pixels, [3]int32{stats.TilesDrawn.Load(), stats.TilesSkipped.Load(), stats.CirclesClipped.Load()}
}

// Exercise outlines without subsequent tile fills hiding corrupted vertices.
func checkConcurrentOutlineBuffers(t *testing.T) {
	const size, cell = 128, 16
	dst := ebiten.NewImage(size, size)
	defer dst.Deallocate()
	xform, _ := NewCenteredUVTransform(size, size)
	r := &Renderer{}
	pixels := make([]byte, size*size*4)
	for frame := 0; frame < 20; frame++ {
		dst.Clear()
		var wg sync.WaitGroup
		start := make(chan struct{})
		for y := 0; y < size; y += cell {
			for x := 0; x < size; x += cell {
				tile := UVBounds{float32(x) / size, float32(y) / size, float32(x+cell) / size, float32(y+cell) / size}
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					r.drawDebugOutline(dst, xform, tile)
				}()
			}
		}
		close(start)
		wg.Wait()
		dst.ReadPixels(pixels)
		visible := false
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				a := pixels[4*(y*size+x)+3]
				visible = visible || a != 0
				if x%cell < 2 || x%cell >= cell-2 || y%cell < 2 || y%cell >= cell-2 {
					continue
				}
				if a != 0 {
					t.Fatalf("frame %d: stray outline at interior pixel (%d, %d)", frame, x, y)
				}
			}
		}
		if !visible {
			t.Fatal("expected visible debug outlines")
		}
	}
}
