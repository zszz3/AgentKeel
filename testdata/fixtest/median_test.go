package fixtest

import (
	"slices"
	"testing"
)

func TestMedian(t *testing.T) {
	cases := []struct {
		in   []float64
		want float64
	}{
		{nil, 0},
		{[]float64{3}, 3},
		{[]float64{3, 1, 2}, 2},
		{[]float64{4, 1, 3, 2}, 2.5},
		{[]float64{5, 5, 1, 9}, 5},
	}
	for _, c := range cases {
		in := slices.Clone(c.in)
		if got := Median(in); got != c.want {
			t.Errorf("Median(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestMedianDoesNotModifyInput(t *testing.T) {
	in := []float64{3, 1, 2}
	Median(in)
	if !slices.Equal(in, []float64{3, 1, 2}) {
		t.Errorf("input was modified: %v", in)
	}
}
