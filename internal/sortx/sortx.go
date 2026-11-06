// Package sortx sorts slices with the algorithms of package slices, without package slices' per-type copies of them.
//
// slices.SortFunc and slices.SortStableFunc compile a full copy of the pattern-defeating quicksort (or of the stable insertion and merge sort) for every distinct element shape that calls them, about 16 KB of machine code per shape. A program with many sorted struct types carries about a megabyte of that. [Func] and [Stable] run the same algorithms through one non-generic copy (zsortfunc.go, from package sort) and add two small closures per shape. The closures do not escape, so a sort allocates nothing, as slices.SortFunc does.
//
// The order they produce is the order package slices produces, ties included: package sort's func variants and package slices' CmpFunc variants come from one template (sort/gen_sort_variants.go), and cmp(a, b) < 0 is the Less of the pair. TestFuncMatchesSlicesSortFunc and TestStableMatchesSlicesSortStableFunc check that against package slices on tied data.
package sortx

import "math/bits"

// Func sorts s in ascending order as cmp defines it, like slices.SortFunc: not stable, and equal elements end in the order slices.SortFunc leaves them.
func Func[S ~[]E, E any](s S, cmp func(a, b E) int) {
	n := len(s)
	pdqsort_func(lessSwap{
		func(i, j int) bool { return cmp(s[i], s[j]) < 0 },
		func(i, j int) { s[i], s[j] = s[j], s[i] },
	}, 0, n, bits.Len(uint(n)))
}

// Stable sorts s in ascending order as cmp defines it, keeping the original order of equal elements, like slices.SortStableFunc.
func Stable[S ~[]E, E any](s S, cmp func(a, b E) int) {
	stable_func(lessSwap{
		func(i, j int) bool { return cmp(s[i], s[j]) < 0 },
		func(i, j int) { s[i], s[j] = s[j], s[i] },
	}, len(s))
}
