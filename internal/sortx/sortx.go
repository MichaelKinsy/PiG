// Package sortx sorts slices with the algorithms of package slices, without package slices' per-type copies of them.
//
// slices.SortFunc and slices.SortStableFunc compile a full copy of the pattern-defeating quicksort (or of the stable insertion and merge sort) for every distinct element shape that calls them, 6 to 27 KB of machine code per shape. [Func] and [Stable] run the same algorithms through one non-generic copy (zsortfunc.go, from package sort) and add two small closures per shape. The closures do not escape, so a sort allocates nothing, as slices.SortFunc does.
//
// The order they produce is the order package slices produces, ties included: package sort's func variants and package slices' CmpFunc variants come from one template (sort/gen_sort_variants.go), and cmp(a, b) < 0 is the Less of the pair. TestFuncMatchesSlicesSortFunc and TestStableMatchesSlicesSortStableFunc check that against package slices on tied data.
//
// # Cost
//
// The shared copy calls the element comparison through two indirect calls and each swap through one, where package slices calls the comparison once and swaps inline. BenchmarkFunc and BenchmarkStable measure it on xd670: above the short-slice limit a sort takes 13 to 68% more CPU than slices.SortFunc and slices.SortStableFunc (median of 10 runs; pointers and strings are the worst). Func sorts up to 12 elements and Stable up to 20 by a direct insertion sort, the code package slices runs for that length, so a short sort costs what it costs with slices. A sort allocates nothing in both.
//
// Use sortx where the slice is short, or the path runs once per command and nobody times it. Use slices.SortFunc or slices.SortStableFunc where n can be large on a timed path (a keystroke, a frame, a session or file listing, a request body), and where a sort of the same element shape is already linked (strings, pointers and ints are): then package slices adds no code and costs no CPU. The perf-lint pattern sort-instance (automation/ci/perflint) lists every use of package slices for sorting in a package that cmd/pig links, with the reason, so a new one needs a reason in the baseline.
package sortx

import "math/bits"

const (
	// insertionLimit is the length up to which pdqsort sorts by insertion, as slices.SortFunc does.
	insertionLimit = 12
	// stableBlock is the block length stable_func sorts by insertion before it merges, as slices.SortStableFunc does.
	stableBlock = 20
)

// insertion sorts s by insertion sort, as package slices' insertionSortCmpFunc does. Only this function is instantiated for each element shape, and it is small.
func insertion[E any](s []E, cmp func(a, b E) int) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && cmp(s[j], s[j-1]) < 0; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// Func sorts s in ascending order as cmp defines it, like slices.SortFunc: not stable, and equal elements end in the order slices.SortFunc leaves them.
func Func[S ~[]E, E any](s S, cmp func(a, b E) int) {
	n := len(s)
	if n <= insertionLimit {
		insertion(s, cmp)
		return
	}
	pdqsort_func(lessSwap{
		func(i, j int) bool { return cmp(s[i], s[j]) < 0 },
		func(i, j int) { s[i], s[j] = s[j], s[i] },
	}, 0, n, bits.Len(uint(n)))
}

// Stable sorts s in ascending order as cmp defines it, keeping the original order of equal elements, like slices.SortStableFunc.
func Stable[S ~[]E, E any](s S, cmp func(a, b E) int) {
	n := len(s)
	if n <= stableBlock {
		insertion(s, cmp)
		return
	}
	stable_func(lessSwap{
		func(i, j int) bool { return cmp(s[i], s[j]) < 0 },
		func(i, j int) { s[i], s[j] = s[j], s[i] },
	}, n)
}
