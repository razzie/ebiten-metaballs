package metaballs

import (
	"testing"
)

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
				Groups: 2, Circles: 3, Bridges: 4,
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
