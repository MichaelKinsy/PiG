package registries_test

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/registries"
)

// Ports packages/chord/test/next/registries/entries.test.ts.

type tableValue struct {
	routes []registries.RecordEntry[string]
	items  []string
}

var table = registries.RegistryTable[tableValue]{
	{Plugin: "@a/one", Version: 1, Value: tableValue{
		routes: []registries.RecordEntry[string]{{Key: "/x", Value: "one"}, {Key: "/y", Value: "one"}},
		items:  []string{"late", "first"},
	}},
	{Plugin: "@b/two", Type: "b.store", Version: 1, Value: tableValue{
		routes: []registries.RecordEntry[string]{{Key: "/x", Value: "two"}},
		items:  []string{"second"},
	}},
	{Plugin: "@c/three", Version: 1, Value: tableValue{}},
}

func TestKeyedEntries(t *testing.T) {
	t.Run("maps every entry's keys with their plugin; a duplicate is reported and the first by plugin name keeps it", func(t *testing.T) {
		var conflicts []registries.RegistryConflict
		keyed := registries.KeyedEntries(
			table,
			func(value tableValue) []registries.RecordEntry[string] { return value.routes },
			func(conflict registries.RegistryConflict) { conflicts = append(conflicts, conflict) },
		)
		type pair struct {
			key  string
			item registries.TableItem[string]
		}
		var got []pair
		for key, item := range keyed.All() {
			got = append(got, pair{key, item})
		}
		want := []pair{
			{"/x", registries.TableItem[string]{Value: "one", Plugin: "@a/one"}},
			{"/y", registries.TableItem[string]{Value: "one", Plugin: "@a/one"}},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("map = %#v, want %#v", got, want)
		}
		wantConflicts := []registries.RegistryConflict{{Message: `"/x" is contributed twice`, Plugins: []string{"@a/one", "@b/two"}}}
		if !reflect.DeepEqual(conflicts, wantConflicts) {
			t.Fatalf("conflicts = %#v, want %#v", conflicts, wantConflicts)
		}
		if keyed.Len() != 2 {
			t.Fatalf("Len = %d, want 2", keyed.Len())
		}
		if item, ok := keyed.Get("/x"); !ok || item.Plugin != "@a/one" {
			t.Fatalf("Get(/x) = %#v, %v", item, ok)
		}
		if _, ok := keyed.Get("/z"); ok {
			t.Fatal("Get(/z) found a key nobody contributed")
		}
	})
}

func TestOrderedEntries(t *testing.T) {
	itemsOf := func(value tableValue) []string { return value.items }

	t.Run("lists every entry's items in table order, with their plugin and type", func(t *testing.T) {
		got := registries.OrderedEntries(table, itemsOf, nil)
		want := []registries.TableItem[string]{
			{Value: "late", Plugin: "@a/one"},
			{Value: "first", Plugin: "@a/one"},
			{Value: "second", Plugin: "@b/two", Type: "b.store"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("items = %#v, want %#v", got, want)
		}
	})

	t.Run("sorts by `orderOf`, table order kept among equals", func(t *testing.T) {
		order := func(item string) float64 {
			if item == "first" {
				return 0
			}
			return 1
		}
		var values []string
		for _, item := range registries.OrderedEntries(table, itemsOf, order) {
			values = append(values, item.Value)
		}
		if want := []string{"first", "late", "second"}; !reflect.DeepEqual(values, want) {
			t.Fatalf("values = %v, want %v", values, want)
		}
	})
}
