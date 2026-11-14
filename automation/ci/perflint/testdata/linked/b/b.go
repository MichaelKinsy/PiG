package b

import "slices"

// Sort is not linked into cmd/pig: its slices sort is not a finding.
func Sort(s []int) {
	slices.SortFunc(s, func(x, y int) int { return x - y })
}

// Sorted is not linked either.
func Sorted(s []int) []int {
	return slices.SortedFunc(slices.Values(s), func(x, y int) int { return x - y })
}
