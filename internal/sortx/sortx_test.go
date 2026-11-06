package sortx

import (
	"math/rand/v2"
	"slices"
	"testing"
)

type tied struct {
	key, id int
}

func compareKey(a, b tied) int { return a.key - b.key }

// data returns n elements whose keys take few values, so most compare equal and the order of equals shows; id records the input position.
func data(rng *rand.Rand, n, keys int) []tied {
	out := make([]tied, n)
	for i := range out {
		out[i] = tied{rng.IntN(keys), i}
	}
	return out
}

func TestFuncMatchesSlicesSortFunc(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for _, n := range []int{0, 1, 2, 3, 11, 12, 13, 50, 51, 100, 1000, 5000, 20000} {
		for _, keys := range []int{1, 2, 3, 10, 1 << 30} {
			input := data(rng, n, keys)
			want := slices.Clone(input)
			slices.SortFunc(want, compareKey)
			got := slices.Clone(input)
			Func(got, compareKey)
			if !slices.Equal(got, want) {
				t.Fatalf("n=%d keys=%d: Func leaves a different order than slices.SortFunc", n, keys)
			}
		}
	}
}

func TestFuncMatchesSlicesSortFuncOnPatterns(t *testing.T) {
	for _, n := range []int{100, 1000, 10000} {
		patterns := map[string][]tied{"ascending": make([]tied, n), "descending": make([]tied, n), "sawtooth": make([]tied, n), "plateaus": make([]tied, n)}
		for i := range n {
			patterns["ascending"][i] = tied{i, i}
			patterns["descending"][i] = tied{n - i, i}
			patterns["sawtooth"][i] = tied{i % 37, i}
			patterns["plateaus"][i] = tied{i / 50, i}
		}
		for name, input := range patterns {
			want := slices.Clone(input)
			slices.SortFunc(want, compareKey)
			got := slices.Clone(input)
			Func(got, compareKey)
			if !slices.Equal(got, want) {
				t.Fatalf("%s n=%d: Func leaves a different order than slices.SortFunc", name, n)
			}
		}
	}
}

func TestStableMatchesSlicesSortStableFunc(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	for _, n := range []int{0, 1, 2, 19, 20, 21, 41, 100, 1000, 20000} {
		for _, keys := range []int{1, 3, 10, 1 << 30} {
			input := data(rng, n, keys)
			want := slices.Clone(input)
			slices.SortStableFunc(want, compareKey)
			got := slices.Clone(input)
			Stable(got, compareKey)
			if !slices.Equal(got, want) {
				t.Fatalf("n=%d keys=%d: Stable leaves a different order than slices.SortStableFunc", n, keys)
			}
		}
	}
}

type names []string

func TestNamedSliceTypeSorts(t *testing.T) {
	got := names{"b", "c", "a"}
	Func(got, func(a, b string) int {
		switch {
		case a < b:
			return -1
		case a > b:
			return 1
		}
		return 0
	})
	if !slices.Equal(got, names{"a", "b", "c"}) {
		t.Fatalf("got %v", got)
	}
}

// A sort must allocate nothing, as slices.SortFunc does: callers sort on paths that tests hold to an allocation budget.
func TestSortsDoNotAllocate(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	input := data(rng, 500, 20)
	work := make([]tied, len(input))
	limit := 3 // captured, so the comparison is a closure and not a plain function
	capturing := func(a, b tied) int { return limit*a.key - limit*b.key }
	if got := testing.AllocsPerRun(20, func() { copy(work, input); Func(work, capturing) }); got != 0 {
		t.Errorf("Func allocates %v times", got)
	}
	if got := testing.AllocsPerRun(20, func() { copy(work, input); Stable(work, capturing) }); got != 0 {
		t.Errorf("Stable allocates %v times", got)
	}
}
