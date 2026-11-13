// SPDX-License-Identifier: MIT

package sharedtext

import (
	"hash/maphash"
	"runtime"
	"strings"
	"testing"
	"unsafe"
)

func dataOf(s string) *byte { return unsafe.StringData(s) } //nolint:gosec // G103: the test compares the addresses of two strings.

func largeText(seed string) string { return strings.Repeat(seed+" line of a file\n", MinBytes/8) }

func TestAnEqualTextIsReturnedAsTheEarlierString(t *testing.T) {
	table := newTable()
	first := largeText("a")
	if got := table.share(first); dataOf(got) != dataOf(first) {
		t.Fatal("the first text was not returned itself")
	}
	second := strings.Clone(first)
	got := table.share(second)
	if got != first || dataOf(got) != dataOf(first) {
		t.Fatalf("an equal text was not returned as the earlier string: same bytes %v", dataOf(got) == dataOf(first))
	}
	other := largeText("b")
	if got := table.share(other); dataOf(got) != dataOf(other) {
		t.Fatal("a different text was returned as another string")
	}
}

func TestASmallTextIsNotShared(t *testing.T) {
	table := newTable()
	first := strings.Repeat("x", MinBytes-1)
	table.share(first)
	second := strings.Clone(first)
	if got := table.share(second); dataOf(got) != dataOf(second) {
		t.Fatal("a text under MinBytes was replaced")
	}
	if len(table.entries) != 0 {
		t.Fatalf("a text under MinBytes took %d entries", len(table.entries))
	}
}

func TestATextInsideALargerStringIsShared(t *testing.T) {
	table := newTable()
	whole := "head\n" + largeText("c")
	part := whole[len("head\n"):]
	table.share(part)
	got := table.share(strings.Clone(part))
	if dataOf(got) != dataOf(part) {
		t.Fatal("an equal text was not returned as the earlier substring")
	}
	runtime.KeepAlive(whole)
}

func TestTextsWithOneHashNeverServeEachOther(t *testing.T) {
	table := newTable()
	first, second := largeText("d"), largeText("e")
	// Force the collision: the entry of first is stored under second's hash too.
	table.share(first)
	for key, held := range table.entries {
		delete(table.entries, key)
		table.entries[maphashOf(table, second)] = held
	}
	if got := table.share(second); got != second || dataOf(got) != dataOf(second) {
		t.Fatal("a text was served another text's string")
	}
	if got := table.share(strings.Clone(second)); dataOf(got) != dataOf(second) {
		t.Fatal("the text that took the entry is not shared")
	}
	runtime.KeepAlive(first)
}

func TestACollectedTextIsNotReturnedAndItsEntryIsDropped(t *testing.T) {
	table := newTable()
	func() {
		for i := range 2 * minSweep {
			table.share(largeText(string(rune('A'+i%26)) + string(rune('a'+i/26))))
		}
	}()
	// The texts above are unreachable: two cycles clear their weak pointers.
	runtime.GC()
	runtime.GC()
	kept := largeText("kept")
	for i := 0; len(table.entries) > 1 && i < 2*minSweep; i++ {
		table.sweepAt = 1
		if got := table.share(kept); dataOf(got) != dataOf(kept) {
			t.Fatal("a held text was replaced")
		}
	}
	if len(table.entries) != 1 {
		t.Fatalf("the table keeps %d entries of collected texts", len(table.entries)-1)
	}
	again := largeText("Aa")
	if got := table.share(again); dataOf(got) != dataOf(again) {
		t.Fatal("a collected text was returned")
	}
	runtime.KeepAlive(kept)
}

func maphashOf(table *table, text string) uint64 { return maphash.String(table.seed, text) }
