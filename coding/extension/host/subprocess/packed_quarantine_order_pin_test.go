package subprocess

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// quarantineReportOrder quarantines a cell whose members are the named extensions and returns the names in the order their ends were reported, with the notice
// each report had.
func quarantineReportOrder(tb testing.TB, loadOrder map[string]int, names []string) (order []string, notices []string) {
	tb.Helper()
	h := NewHost(tb.TempDir())
	const key = "cell"
	h.loadOrder = loadOrder
	for _, name := range names {
		h.exts[name] = &managedExt{config: ExtConfig{Name: name}, packedCellKey: key}
	}
	h.exts["elsewhere"] = &managedExt{config: ExtConfig{Name: "elsewhere"}, packedCellKey: "other-cell"}
	h.SetCrashHandler(func(name string, delay time.Duration, disabled bool, reason string) {
		if delay != 0 || !disabled {
			tb.Errorf("%s: reported with delay %s, disabled %t", name, delay, disabled)
		}
		order = append(order, name)
		notices = append(notices, reason)
	})
	h.quarantinePackedCellGeneration(key, 0, "packed process exited")
	if h.exts["elsewhere"] == nil || len(h.exts) != 1 {
		tb.Errorf("the quarantine left %d extensions", len(h.exts))
	}
	return order, notices
}

// The members of a quarantined cell are reported in load order, then by name; the members come out of a map, so the order in is different on every run.
func TestQuarantineReportsTheMembersInLoadOrder(t *testing.T) {
	loadOrder := map[string]int{"omega": 1, "mid": 2, "delta": 3, "alpha": 3}
	names := []string{"delta", "beta", "omega", "alpha", "aaa", "mid"}
	want := []string{"aaa", "beta", "omega", "mid", "alpha", "delta"}
	const notice = "packed process exited; aaa, alpha, beta, delta, mid, omega stopped; /reload restarts them"
	for range 20 {
		order, notices := quarantineReportOrder(t, loadOrder, names)
		if !slices.Equal(order, want) {
			t.Fatalf("reported in the order %q, want %q", order, want)
		}
		for i, got := range notices {
			if got != notice {
				t.Fatalf("%s: the notice %q, want %q", order[i], got, notice)
			}
		}
	}
}

// For cells of 0 to 40 members with repeated and missing load ranks, the order is the one slices.SortFunc gives with the same comparison.
func TestQuarantineOrderMatchesSlicesSortFunc(t *testing.T) {
	for size := range 41 {
		loadOrder := make(map[string]int)
		names := make([]string, size)
		for i := range names {
			names[i] = fmt.Sprintf("ext-%02d", (i*41)%size)
			if i%3 != 0 {
				loadOrder[names[i]] = (i * 7) % 5
			}
		}
		want := slices.Clone(names)
		slices.SortFunc(want, func(a, b string) int {
			return cmp.Or(cmp.Compare(loadOrder[a], loadOrder[b]), strings.Compare(a, b))
		})
		if size == 0 {
			want = nil
		}
		for range 5 {
			if order, _ := quarantineReportOrder(t, loadOrder, names); !slices.Equal(order, want) {
				t.Fatalf("%d members: reported in the order %q, want %q", size, order, want)
			}
		}
	}
}

func BenchmarkQuarantinePackedCell(b *testing.B) {
	loadOrder := make(map[string]int)
	names := make([]string, 16)
	for i := range names {
		names[i] = fmt.Sprintf("ext-%02d", i)
		loadOrder[names[i]] = (i * 7) % 5
	}
	b.ReportAllocs()
	for b.Loop() {
		if order, _ := quarantineReportOrder(b, loadOrder, names); len(order) != len(names) {
			b.Fatalf("%d reports", len(order))
		}
	}
}
