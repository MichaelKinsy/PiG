package jsarray

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// TestSortMatchesV8 compares Sort with V8's Array.prototype.sort for comparators that, like highlightAuto's supersetOf tie-break, are not a strict weak order (testdata/v8-sort-oracle.mjs, Node 24).
func TestSortMatchesV8(t *testing.T) {
	raw, err := os.ReadFile("testdata/v8-sort-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Items [][3]int
		Order []int
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		items := slices.Clone(c.Items)
		Sort(items, func(a, b [3]int) float64 {
			if a[1] != b[1] {
				return float64(b[1] - a[1])
			}
			if a[2] == b[0]%6 {
				return 1
			}
			if b[2] == a[0]%6 {
				return -1
			}
			return 0
		})
		order := make([]int, len(items))
		for i, item := range items {
			order[i] = item[0]
		}
		if !slices.Equal(order, c.Order) {
			t.Errorf("%d items: order %v, V8 %v", len(c.Items), order, c.Order)
		}
	}
}
