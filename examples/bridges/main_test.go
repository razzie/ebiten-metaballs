package main

import (
	"os"
	"slices"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
	metaballs "github.com/razzie/ebiten-metaballs"
	"github.com/razzie/ebiten-metaballs/softbody"
)

func TestSyncGeometryPreservesBridgeEndpoints(t *testing.T) {
	cfg := softbody.DefaultConfig()
	cfg.Workers = 1
	world := softbody.New(cfg)
	var ids [numGroups][]uint64
	// Interleave groups and reverse spatial order to exercise grid sorting
	// and the conversion from global stable IDs to group-local indices.
	for _, x := range []float32{0.8, 0.2, 0.5} {
		for group := range numGroups {
			id, err := world.AddCircle(softbody.CircleSpec{
				X: x, Y: 0.2 + 0.3*float32(group),
				InnerRadius: 0.01, OuterRadius: 0.02,
				Group: softbody.Group(group),
			})
			if err != nil {
				t.Fatal(err)
			}
			ids[group] = append(ids[group], id)
		}
	}
	var removedID uint64
	for group := range numGroups {
		id, err := world.AddBridge(softbody.BridgeSpec{A: ids[group][2], B: ids[group][0], MaxDistance: 1})
		if err != nil {
			t.Fatal(err)
		}
		if group == 1 {
			removedID = id
		}
	}
	g := &Game{
		world:         world,
		groups:        make([]metaballs.Group, numGroups),
		circleIndices: make(map[uint64]circleLocation),
	}
	g.syncGeometry()
	want := make([][]metaballs.Circle, numGroups)
	for i := range g.groups {
		want[i] = slices.Clone(g.groups[i].Circles)
	}
	before := world.Snapshot(nil)
	world.Step(1.0 / ticksPerSecond)
	if slices.Equal(before, world.Snapshot(nil)) {
		t.Fatal("fixture did not exercise grid reordering")
	}
	g.syncGeometry()
	for i, group := range g.groups {
		if !slices.Equal(group.Circles, want[i]) {
			t.Fatalf("group %d changed circle blend order", i)
		}
		if len(group.Bridges) != 1 || group.Bridges[0] != (metaballs.Bridge{A: 2, B: 0, MiddleRadius: bridgeRadius}) {
			t.Fatalf("group %d has incorrect bridge endpoints: %+v", i, group.Bridges)
		}
	}
	world.RemoveBridge(removedID)
	g.syncGeometry()
	if len(g.bridges) != numGroups-1 || len(g.groups[1].Bridges) != 0 {
		t.Fatal("removed physics bridge is still rendered")
	}
}

func TestNewGameInitializesChainsAndRenderers(t *testing.T) {
	g, err := NewGame()
	if err != nil {
		t.Fatal(err)
	}
	if g.renderers[0] == nil || g.renderers[1] == nil || g.renderers[0] == g.renderers[1] {
		t.Fatal("expected two separate renderers")
	}
	capacity := metaballs.CapacityForGroups(g.groups)
	if capacity.Circles != g.world.Len() || capacity.Bridges != capacity.Circles-numGroups*numClustersPerGroup {
		t.Fatalf("unexpected chain geometry: %+v", capacity)
	}
}

func TestDrawBothRenderers(t *testing.T) {
	if os.Getenv("METABALLS_DRAW_TESTS") != "1" {
		t.Skip("METABALLS_DRAW_TESTS not enabled")
	}
	g, err := NewGame()
	if err != nil {
		t.Fatal(err)
	}
	if err := ebiten.RunGame(&rendererTestGame{Game: g, t: t}); err != nil {
		t.Fatal(err)
	}
}

type rendererTestGame struct {
	*Game
	t *testing.T
}

func (g *rendererTestGame) Draw(*ebiten.Image) {}

func (g *rendererTestGame) Update() error {
	dst := ebiten.NewImage(screenSize, screenSize)
	defer dst.Deallocate()
	for i, renderer := range g.renderers {
		dst.Clear()
		stats, err := renderer.Draw(dst, g.groups, g.xform)
		if err != nil {
			g.t.Errorf("renderer %d: %v", i, err)
			continue
		}
		if stats.TilesDrawn.Load() == 0 || stats.CirclesClipped.Load() != 0 {
			g.t.Errorf("renderer %d: tiles drawn %d, circles clipped %d", i, stats.TilesDrawn.Load(), stats.CirclesClipped.Load())
		}
	}
	return ebiten.Termination
}
