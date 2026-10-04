package softbody

import (
	"math"
	"slices"
	"testing"
)

func TestCirclesAt(t *testing.T) {
	s := dragWorld()
	var ids []uint64
	for _, x := range []float32{.53125, .46875, .5, .875} {
		id, err := s.AddCircle(CircleSpec{X: x, Y: .5, InnerRadius: .015625, OuterRadius: .0625, VX: .1, Mass: 2})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := s.AnchorCircle(ids[0], .53125, .5); err != nil {
		t.Fatal(err)
	}
	s.Drag(DragSpec{X: .5, Y: .5, MaxCircles: 1})
	s.Carry(.75, .5) // Queries must not apply the pending target or drop the selection.
	s.rebuildGrid()
	before := s.Snapshot(nil)
	masses, dragged := slices.Clone(s.p.invMass), slices.Clone(s.dragged)
	for _, tc := range []struct {
		name string
		x, y float32
		want []uint64
	}{
		{"overlap, distance and ID order", .5, .5, []uint64{ids[2], ids[0], ids[1]}},
		{"shell and exact boundary", .4375, .5, []uint64{ids[1], ids[2]}},
		{"just outside boundary", math.Nextafter32(.40625, 0), .5, nil},
		{"miss", .1, .1, nil},
		{"pending target", .75, .5, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := s.CirclesAt(tc.x, tc.y)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("CirclesAt(%g, %g) = %v, want %v", tc.x, tc.y, got, tc.want)
			}
			if len(got) == 0 && got != nil {
				t.Fatal("miss did not return nil")
			}
			if len(got) > 0 {
				got[0] = 999 // Returned IDs must not alias state.
			}
		})
	}
	if !slices.Equal(before, s.Snapshot(nil)) || !slices.Equal(masses, s.p.invMass) || !slices.Equal(dragged, s.dragged) || s.dragX != .75 || s.dragY != .5 {
		t.Fatal("query changed circles, masses, or drag selection")
	}
}

func TestPolygonsAt(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		s := polygonWorld()
		points := []Point{{.125, .125}, {.875, .125}, {.875, .375}, {.375, .375}, {.375, .875}, {.125, .875}}
		if reverse {
			slices.Reverse(points)
		}
		a := addTestPolygon(t, s, points)
		b := addTestPolygon(t, s, rectangle(.25, .25, .5, .5))
		for _, tc := range []struct {
			name string
			x, y float32
			want []uint64
		}{
			{"overlapping interiors", .3125, .3125, []uint64{a, b}},
			{"concave arm", .25, .75, []uint64{a}},
			{"concave notch", .75, .75, nil},
			{"horizontal edge", .5, .125, []uint64{a}},
			{"vertical edge", .125, .5, []uint64{a}},
			{"convex vertex", .875, .125, []uint64{a}},
			{"concave vertex", .375, .375, []uint64{a, b}},
			{"just outside edge", math.Nextafter32(.125, 0), .5, nil},
			{"outside box", 0, 0, nil},
		} {
			t.Run(tc.name+map[bool]string{false: "/forward", true: "/reverse"}[reverse], func(t *testing.T) {
				got := s.PolygonsAt(tc.x, tc.y)
				if !slices.Equal(got, tc.want) {
					t.Fatalf("PolygonsAt(%g, %g) = %v, want %v", tc.x, tc.y, got, tc.want)
				}
				if len(got) == 0 && got != nil {
					t.Fatal("miss did not return nil")
				}
				if len(got) > 0 {
					got[0] = 999
					if !slices.Equal(s.PolygonsAt(tc.x, tc.y), tc.want) {
						t.Fatal("result aliases polygon IDs")
					}
				}
			})
		}
		if !s.RemovePolygon(a) || !slices.Equal(s.PolygonsAt(.3125, .3125), []uint64{b}) {
			t.Fatal("query returned a removed polygon")
		}
	}
	// Include the closing sloped edge and its midpoint exactly.
	s := polygonWorld()
	id := addTestPolygon(t, s, []Point{{.125, .125}, {.875, .125}, {.875, .625}})
	if got := s.PolygonsAt(.5, .375); !slices.Equal(got, []uint64{id}) {
		t.Fatalf("sloped edge query = %v, want [%d]", got, id)
	}
}

func TestPointQueriesEmptyAndInvalid(t *testing.T) {
	s := polygonWorld()
	if s.CirclesAt(.5, .5) != nil || s.PolygonsAt(.5, .5) != nil {
		t.Fatal("empty state returned hits")
	}
	addDragCircle(t, s, .75, .75, 1, 0)
	addTestPolygon(t, s, rectangle(.125, .125, .375, .375))
	for _, v := range []float32{float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1)), math.MaxFloat32, -math.MaxFloat32} {
		for _, p := range []Point{{v, .25}, {.25, v}} {
			if s.CirclesAt(p.X, p.Y) != nil || s.PolygonsAt(p.X, p.Y) != nil {
				t.Fatalf("invalid or distant point %+v returned hits", p)
			}
		}
	}
}

func TestPointQueriesFollowGeometryChanges(t *testing.T) {
	s := polygonWorld()
	if err := s.SetBoundaryMode(BoundaryRemove); err != nil {
		t.Fatal(err)
	}
	circle := addDragCircle(t, s, .25, .75, 1, 0)
	poly := addTestPolygon(t, s, rectangle(.125, .125, .375, .375))
	s.rebuildGrid()
	if err := s.TranslateCircles(.25, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.TranslatePolygons(.25, 0); err != nil {
		t.Fatal(err)
	}
	if s.CirclesAt(.25, .75) != nil || s.PolygonsAt(.25, .25) != nil || !slices.Equal(s.CirclesAt(.5, .75), []uint64{circle}) || !slices.Equal(s.PolygonsAt(.5, .25), []uint64{poly}) {
		t.Fatal("query did not reflect translations before a step")
	}
	if err := s.ShiftOrigin(.5, .5); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.CirclesAt(0, .25), []uint64{circle}) || !slices.Equal(s.PolygonsAt(0, -.25), []uint64{poly}) {
		t.Fatal("query did not reflect origin shift")
	}
	if err := s.SetBounds(Bounds{MinX: .25, MinY: -.5, MaxX: .5, MaxY: .5}); err != nil {
		t.Fatal(err)
	}
	if s.CirclesAt(0, .25) != nil || s.PolygonsAt(0, -.25) != nil {
		t.Fatal("query returned entities deleted by boundary removal")
	}
}
