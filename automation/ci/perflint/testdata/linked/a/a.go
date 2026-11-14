package a

import "slices"

// Sort is linked into cmd/pig: its slices sort is a finding.
func Sort(s []int) {
	slices.SortFunc(s, func(x, y int) int { return x - y })
}

// Sorted is linked into cmd/pig too: collecting a sequence into a sorted slice compiles the same sort.
func Sorted(s []int) []int {
	return slices.SortedFunc(slices.Values(s), func(x, y int) int { return x - y })
}
