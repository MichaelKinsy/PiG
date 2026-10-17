package jsstring

import (
	"slices"
	"testing"
)

func TestSortOwnKeysOrdersArrayIndexesFirst(t *testing.T) {
	keys := []string{"b", "10", "a", "2", "01", "4294967295", "4294967294", "-1", "1.5", "", "0", "+1"}
	SortOwnKeys(keys, func(k string) string { return k })
	want := []string{"0", "2", "10", "4294967294", "b", "a", "01", "4294967295", "-1", "1.5", "", "+1"}
	if !slices.Equal(keys, want) {
		t.Fatalf("got %q, want %q", keys, want)
	}
}
