package a

import "slices"

// SortWindows is linked on windows only.
func SortWindows(s []int) {
	slices.SortFunc(s, func(x, y int) int { return x - y })
}
