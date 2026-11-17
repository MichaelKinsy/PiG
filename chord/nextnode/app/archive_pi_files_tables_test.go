package app

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The tables of the Go `Files` facet are Pi's: a Pi-made Files table is read by the Go facet as it is (no refusal, no migration), because
// each table's version, key, indexes and fingerprint are the same in the store Pi made and in a fresh Go kit store.
func TestGoFilesTablesAreIdenticalToPis(t *testing.T) {
	_, pi := piPacks(t)
	fresh := filesOf(t, fixtureOptions{Store: kitStore})
	fresh.close()
	goTables := describeStore(t, filepath.Join(fresh.dir, kitStore)).Tables
	seen := 0
	for name, want := range pi.Tables {
		if !strings.HasPrefix(name, "chord.app.files:") {
			continue
		}
		seen++
		got, ok := goTables[name]
		if !ok {
			t.Errorf("the Go kit store has no table %s", name)
			continue
		}
		want.Puts, got.Puts = 0, 0
		if want.Version != got.Version || want.Fingerprint != got.Fingerprint || !slices.Equal(want.Key, got.Key) || !maps.EqualFunc(want.Indexes, got.Indexes, slices.Equal) {
			t.Errorf("table %s: Go %+v, Pi %+v", name, got, want)
		}
	}
	if seen != len(goTables) || seen != 4 {
		t.Errorf("Pi's host has %d Files tables and the Go kit store %d, want 4 each", seen, len(goTables))
	}
}
