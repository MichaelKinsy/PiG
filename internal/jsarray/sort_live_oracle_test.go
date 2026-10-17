package jsarray

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type liveSortCase struct {
	Items []int `json:"items"`
	Kind  int   `json:"kind"`
	Seed  int   `json:"seed"`
}

type liveSortResult struct {
	Order    []int `json:"order"`
	Compared []int `json:"compared"`
}

// liveSortOutcomes are the comparator results the hash comparator picks from: every class a JavaScript comparator can return that SortCompareUserFn distinguishes (negative, zero, positive, NaN, fractions, infinities).
var liveSortOutcomes = []float64{-1, 0, 1, math.NaN(), -0.5, 2, math.Inf(1), math.Inf(-1), 0.25, -3}

// liveSortCompare is the comparator both sides run: kind 0 is a consistent order, 1 a consistent order that returns NaN for ties, 2 an inconsistent one picked by a hash of the pair, 3 an inconsistent one that is biased towards a consistent order (so runs form and galloping starts), 4 one that returns only 0, 5 one with fractional results of a consistent order.
func liveSortCompare(kind, seed, a, b int) float64 {
	switch kind {
	case 0:
		return float64(a - b)
	case 1:
		if a == b {
			return math.NaN()
		}
		return float64(a - b)
	case 2, 3:
		h := uint32(int32(a))*2654435761 ^ uint32(int32(b))*40503 ^ uint32(int32(seed))*2246822519
		h ^= h >> 15
		h *= 2246822507
		h ^= h >> 13
		if kind == 3 && h%5 != 0 {
			return float64(a - b)
		}
		return liveSortOutcomes[h%uint32(len(liveSortOutcomes))]
	case 4:
		return 0
	}
	return float64(a-b) / 7
}

// V8's Array.prototype.sort (TimSort in array-sort.tq) is not defined by its result alone when the comparator is not a strict weak order: the order depends on which comparisons it makes, in which order. This compares result and the sequence of comparator calls with Node's for sizes around the minimum run (32/64), the merge-collapse invariant, and galloping (7 wins).
func TestSortComparisonSequenceMatchesNode(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))
	var cases []liveSortCase
	sizes := []int{0, 1, 2, 3, 5, 31, 32, 33, 63, 64, 65, 100, 127, 128, 129, 200, 255, 300, 513, 1000}
	for _, n := range sizes {
		for shape := range 8 {
			items := make([]int, n)
			for i := range items {
				switch shape {
				case 0:
					items[i] = rng.IntN(max(1, n*3))
				case 1:
					items[i] = i
				case 2:
					items[i] = n - i
				case 3:
					items[i] = i/10 + rng.IntN(3)
				case 4:
					items[i] = rng.IntN(4)
				case 5:
					items[i] = i % 37
				case 6:
					items[i] = (i/50)*100 - i%50
				default:
					items[i] = rng.IntN(2000) - 1000
				}
			}
			for kind := range 6 {
				if kind == 4 && shape > 1 {
					continue
				}
				cases = append(cases, liveSortCase{Items: items, Kind: kind, Seed: rng.IntN(1 << 20)})
			}
		}
	}
	// Natural ascending runs of the given lengths, each below the one before it: the run stack reaches lengths where one equals the sum of the next two (the larger of those two first, so a lax invariant skips the merge), which is where the merge-collapse invariant (runs[n-2] > runs[n-1] + runs[n]) must be strict.
	for _, lengths := range [][]int{{64, 32, 32}, {100, 50, 50}, {40, 20, 10, 10}, {70, 35, 20, 15}, {80, 40, 20, 20, 10, 10}, {128, 64, 64, 32, 32}, {90, 45, 45}, {60, 30, 30, 15, 15}, {64, 40, 24}, {100, 60, 40}, {96, 60, 36}, {70, 50, 40, 30, 20, 10}, {110, 70, 40, 45, 45}, {120, 80, 40, 50, 50, 50}, {150, 90, 60, 70, 70}, {100, 100, 60, 40, 80, 80}} {
		var items []int
		for run, length := range lengths {
			for i := range length {
				items = append(items, (len(lengths)-run)*1000+i)
			}
		}
		for kind := range 2 {
			cases = append(cases, liveSortCase{Items: items, Kind: kind, Seed: 1})
		}
	}
	var want []liveSortResult
	pioracle.Run(t, `
const outcomes = [-1, 0, 1, NaN, -0.5, 2, Infinity, -Infinity, 0.25, -3];
const cmp = (kind, seed, a, b) => {
  switch (kind) {
    case 0: return a - b;
    case 1: return a === b ? NaN : a - b;
    case 2: case 3: {
      let h = (Math.imul(a, 2654435761) ^ Math.imul(b, 40503) ^ Math.imul(seed, 2246822519)) >>> 0;
      h = (h ^ (h >>> 15)) >>> 0;
      h = Math.imul(h, 2246822507) >>> 0;
      h = (h ^ (h >>> 13)) >>> 0;
      if (kind === 3 && h % 5 !== 0) return a - b;
      return outcomes[h % outcomes.length];
    }
    case 4: return 0;
    default: return (a - b) / 7;
  }
};
emit(input.map(({ items, kind, seed }) => {
  const compared = [];
  const order = [...items].sort((a, b) => { compared.push(a, b); return cmp(kind, seed, a, b); });
  return { order, compared };
}));`, cases, &want)
	if len(want) != len(cases) {
		t.Fatalf("Node returned %d results for %d cases", len(want), len(cases))
	}
	bad := 0
	for i, c := range cases {
		items := slices.Clone(c.Items)
		var compared []int
		Sort(items, func(a, b int) float64 {
			compared = append(compared, a, b)
			return liveSortCompare(c.Kind, c.Seed, a, b)
		})
		if len(compared) == 0 {
			compared = []int{}
		}
		switch {
		case !slices.Equal(items, want[i].Order):
			if bad++; bad <= 6 {
				t.Errorf("case %d (%d items, kind %d): order differs from Node", i, len(c.Items), c.Kind)
			}
		case !slices.Equal(compared, want[i].Compared):
			if bad++; bad <= 6 {
				t.Errorf("case %d (%d items, kind %d): %d comparisons, Node %d (first difference at %s)", i, len(c.Items), c.Kind, len(compared)/2, len(want[i].Compared)/2, firstDifference(compared, want[i].Compared))
			}
		}
	}
	if bad > 6 {
		t.Errorf("%d of %d cases differ from Node", bad, len(cases))
	}
}

func firstDifference(got, want []int) string {
	for i := range min(len(got), len(want)) {
		if got[i] != want[i] {
			return fmt.Sprintf("call %d", i/2)
		}
	}
	return fmt.Sprintf("call %d (length)", min(len(got), len(want))/2)
}
