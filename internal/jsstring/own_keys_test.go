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

func TestArrayIndexTable(t *testing.T) {
	for _, test := range []struct {
		key  string
		want uint32
		ok   bool
	}{
		{"0", 0, true}, {"1", 1, true}, {"9", 9, true}, {"10", 10, true}, {"4294967293", 4294967293, true}, {"4294967294", 4294967294, true},
		{"4294967295", 0, false}, {"4294967296", 0, false}, {"9999999999", 0, false}, {"10000000000", 0, false}, {"99999999999999999999", 0, false},
		{"", 0, false}, {"00", 0, false}, {"01", 0, false}, {"-0", 0, false}, {"-1", 0, false}, {"+1", 0, false}, {"1.0", 0, false}, {"1e3", 0, false},
		{" 1", 0, false}, {"1 ", 0, false}, {"0x10", 0, false}, {"a", 0, false}, {"1a", 0, false}, {"pi.ai.request", 0, false}, {"٣", 0, false}, {"1_0", 0, false},
	} {
		got, ok := ArrayIndex(test.key)
		if got != test.want || ok != test.ok {
			t.Errorf("ArrayIndex(%q) = %d, %v; want %d, %v", test.key, got, ok, test.want, test.ok)
		}
	}
}

var arrayIndexSink uint32

func TestArrayIndexNonNumericNamesDoNotAllocate(t *testing.T) {
	for _, key := range []string{"provider", "usage.input", "x1", "12345678901", "0123"} {
		if allocs := testing.AllocsPerRun(100, func() { arrayIndexSink, _ = ArrayIndex(key) }); allocs != 0 {
			t.Errorf("ArrayIndex(%q) allocates %v", key, allocs)
		}
	}
}
