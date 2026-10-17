// SPDX-License-Identifier: MIT

//go:build linux

package perfgate

import "testing"

func TestMedianOf(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []float64
		want   float64
	}{
		{"none", nil, 0},
		{"one", []float64{7}, 7},
		{"odd is the middle value, unsorted input", []float64{9, 1, 5}, 5},
		{"even is the mean of the middle two", []float64{4, 1, 3, 100}, 3.5},
		{"an outlier does not move it", []float64{3800, 3810, 3790, 3805, 90000}, 3805},
	} {
		input := append([]float64(nil), tc.values...)
		if got := medianOf(tc.values); got != tc.want {
			t.Errorf("%s: medianOf(%v) = %v, want %v", tc.name, tc.values, got, tc.want)
		}
		for i := range input {
			if input[i] != tc.values[i] {
				t.Errorf("%s: medianOf reordered its argument", tc.name)
			}
		}
	}
}

func TestGatedGrowth(t *testing.T) {
	const ceiling = 3800
	for _, tc := range []struct {
		name          string
		median, floor float64
		want          float64
	}{
		{"below the ceiling passes as measured", 3500, 6000, 3500},
		{"negative growth is zero", -40, 10, 0},
		{"above the ceiling by less than the A/A floor is no measurable difference", 3900, 150, ceiling},
		{"above the ceiling by exactly the floor is no measurable difference", 3950, 150, ceiling},
		{"above the ceiling by more than the floor fails as measured", 4000, 150, 4000},
		{"a zero floor gates the median itself", 3801, 0, 3801},
	} {
		if got := gatedGrowth(tc.median, tc.floor, ceiling); got != tc.want {
			t.Errorf("%s: gatedGrowth(%v, %v, %d) = %v, want %v", tc.name, tc.median, tc.floor, ceiling, got, tc.want)
		}
	}
}
