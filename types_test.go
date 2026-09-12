package metaballs

import (
	"reflect"
	"testing"
)

func TestCombineGroups(t *testing.T) {
	groups := []Group{
		{
			Circles: []Circle{{X: 1}, {X: 2}},
			Bridges: []Bridge{{A: 0, B: 1, MiddleRadius: 0.1}},
		},
		{
			Circles: []Circle{{X: 3}},
			Bridges: []Bridge{{A: 0, B: 0, MiddleRadius: 0.2}},
		},
		{
			Circles: []Circle{{X: 4}, {X: 5}},
			Bridges: []Bridge{{A: 0, B: 1, MiddleRadius: 0.3}},
		},
	}

	got := combineGroups(groups, 1)
	want := Group{
		Circles: []Circle{{X: 1}, {X: 2}, {X: 4}, {X: 5}},
		Bridges: []Bridge{
			{A: 0, B: 1, MiddleRadius: 0.1},
			{A: 2, B: 3, MiddleRadius: 0.3},
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("combineGroups mismatch: got %+v, want %+v", got, want)
	}
}

func TestCapacityForGroups(t *testing.T) {
	tests := []struct {
		name   string
		groups []Group
		want   ShaderCapacity
	}{
		{
			name: "empty",
			want: ShaderCapacity{},
		},
		{
			name: "mixed groups",
			groups: []Group{
				{
					Circles: []Circle{{}, {}},
					Bridges: []Bridge{{}},
				},
				{
					Circles: []Circle{{}},
					Bridges: []Bridge{{}, {}, {}},
				},
			},
			want: ShaderCapacity{
				MainCircles:  2,
				MainBridges:  3,
				OtherCircles: 3,
				OtherBridges: 4,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := CapacityForGroups(test.groups); got != test.want {
				t.Fatalf("CapacityForGroups() = %+v, want %+v", got, test.want)
			}
		})
	}
}
