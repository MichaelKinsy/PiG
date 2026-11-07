package b

import "slices"

// Sort is not linked into cmd/pig: its slices sort is not a finding.
func Sort(s []int) {
	slices.SortFunc(s, func(x, y int) int { return x - y })
}
