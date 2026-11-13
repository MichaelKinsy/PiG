package a

import "slices"

// Package-level initializers run at init: their sorts are linked too.
var stable = func() []int {
	s := []int{2, 1}
	slices.SortStableFunc(s, func(x, y int) int { return x - y })
	return s
}()

var direct = func(s []int) int { slices.SortFunc(s, func(x, y int) int { return x - y }); return len(s) }(nil)

var sorted = slices.SortedStableFunc(slices.Values([]int{2, 1}), func(x, y int) int { return x - y })
