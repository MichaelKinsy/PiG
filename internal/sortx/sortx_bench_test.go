package sortx

import (
	"cmp"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"testing"
)

type pair struct { // 24 bytes
	name string
	id   int
}

type wide struct { // 80 bytes
	key int
	_   [9]int
}

type node struct{ key int }

// benchSort times sorting the same unsorted input with package slices and with sortx, for one element shape and four lengths. The copy of the input into the work slice is in every figure; the "copy" rows give it.
func benchSort[E any](b *testing.B, gen func(r *rand.Rand, i int) E, compare func(a, b E) int, slicesSort, sortxSort func([]E, func(a, b E) int)) {
	for _, n := range []int{4, 16, 100, 10000} {
		r := rand.New(rand.NewPCG(7, uint64(n)))
		input := make([]E, n)
		for i := range input {
			input[i] = gen(r, i)
		}
		work := make([]E, n)
		b.Run("n="+strconv.Itoa(n)+"/copy", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				copy(work, input)
			}
		})
		b.Run("n="+strconv.Itoa(n)+"/slices", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				copy(work, input)
				slicesSort(work, compare)
			}
		})
		b.Run("n="+strconv.Itoa(n)+"/sortx", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				copy(work, input)
				sortxSort(work, compare)
			}
		})
	}
}

func slicesFunc[E any](s []E, c func(a, b E) int) { slices.SortFunc(s, c) }

func slicesStable[E any](s []E, c func(a, b E) int) { slices.SortStableFunc(s, c) }

func sortxFunc[E any](s []E, c func(a, b E) int) { Func(s, c) }

func sortxStable[E any](s []E, c func(a, b E) int) { Stable(s, c) }

func genString(r *rand.Rand, i int) string {
	return "name-" + strconv.Itoa(r.IntN(1<<20)) + "-" + strconv.Itoa(i)
}

func genPair(r *rand.Rand, _ int) pair { return pair{"n", r.IntN(1 << 20)} }

func genWide(r *rand.Rand, _ int) wide { return wide{key: r.IntN(1 << 20)} }

func genNode(r *rand.Rand, _ int) *node { return &node{key: r.IntN(1 << 20)} }

func comparePair(a, b pair) int { return cmp.Compare(a.id, b.id) }

func compareWide(a, b wide) int { return cmp.Compare(a.key, b.key) }

func compareNode(a, b *node) int { return cmp.Compare(a.key, b.key) }

func BenchmarkFunc(b *testing.B) {
	b.Run("string", func(b *testing.B) { benchSort(b, genString, strings.Compare, slicesFunc, sortxFunc) })
	b.Run("struct24", func(b *testing.B) { benchSort(b, genPair, comparePair, slicesFunc, sortxFunc) })
	b.Run("struct80", func(b *testing.B) { benchSort(b, genWide, compareWide, slicesFunc, sortxFunc) })
	b.Run("pointer", func(b *testing.B) { benchSort(b, genNode, compareNode, slicesFunc, sortxFunc) })
}

func BenchmarkStable(b *testing.B) {
	b.Run("string", func(b *testing.B) { benchSort(b, genString, strings.Compare, slicesStable, sortxStable) })
	b.Run("struct24", func(b *testing.B) { benchSort(b, genPair, comparePair, slicesStable, sortxStable) })
	b.Run("struct80", func(b *testing.B) { benchSort(b, genWide, compareWide, slicesStable, sortxStable) })
	b.Run("pointer", func(b *testing.B) { benchSort(b, genNode, compareNode, slicesStable, sortxStable) })
}
