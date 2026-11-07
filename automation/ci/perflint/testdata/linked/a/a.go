package a

import "slices"

// Sort is linked into cmd/pig: its slices sort is a finding.
func Sort(s []int) {
	slices.SortFunc(s, func(x, y int) int { return x - y })
}
