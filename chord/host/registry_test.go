package host

import (
	"fmt"
	"slices"
	"testing"
)

// Pi registry.ts:11-14 defineRegistry(name, options = {}) is plain data { name, conflict }, conflict defaulting to
// "error"; registry.ts:28-32 Registration is { registry: Registry<unknown>, key, value }.
func TestDefineRegistryIsPlainDataWithErrorConflictByDefault(t *testing.T) {
	routes := DefineRegistry[string]("t.routes")
	if routes != (Registry[string]{Name: "t.routes", Conflict: ConflictError}) {
		t.Fatalf("DefineRegistry(t.routes) = %+v", routes)
	}
	if got := DefineRegistry[string]("t.routes", RegistryOptions{}); got.Conflict != ConflictError {
		t.Fatalf("empty options: %+v", got)
	}
	last := DefineRegistry[func() any]("t.read", RegistryOptions{Conflict: ConflictLast})
	if last.Name != "t.read" || last.Conflict != "last" {
		t.Fatalf("conflict last: %+v", last)
	}
	// Two tokens with one name are the same registry to the host (host.ts:613 keys tables by registry.name).
	if DefineRegistry[string]("t.routes") != routes {
		t.Fatal("tokens with one name differ")
	}
	registration := Registration{Registry: Registry[any](routes), Key: "GET /a", Value: "a"}
	if registration.Registry.Name != "t.routes" || registration.Registry.Conflict != ConflictError {
		t.Fatalf("Registration = %+v", registration)
	}
}

// Pi registry.ts:16 RegistryTable<V> = ReadonlyMap<string, V>: get, has, size and iteration in insertion order
// (host.test.ts:435 expects keys ["GET /a", "GET /c", "GET /b"] in registration order); host.ts:177 EMPTY_TABLE is
// the table of a registry nothing registered in.
func TestRegistryTableIsAnOrderedReadonlyMap(t *testing.T) {
	keys := []string{"GET /a", "GET /c", "GET /b"}
	table := tableAs[string](NewRegistryTable(keys, map[string]any{"GET /a": "a", "GET /b": "b", "GET /c": "c"}))
	if got := table.Keys(); !slices.Equal(got, keys) {
		t.Fatalf("Keys = %v", got)
	}
	if value, ok := table.Get("GET /b"); !ok || value != "b" {
		t.Fatalf("Get(GET /b) = %q, %v", value, ok)
	}
	if value, ok := table.Get("GET /z"); ok || value != "" {
		t.Fatalf("Get(missing) = %q, %v", value, ok)
	}
	if !table.Has("GET /c") || table.Has("GET /z") || table.Len() != 3 {
		t.Fatalf("Has/Len = %v %v %d", table.Has("GET /c"), table.Has("GET /z"), table.Len())
	}
	var entries []string
	for key, value := range table.All() {
		entries = append(entries, key+"="+value)
	}
	if fmt.Sprint(entries) != "[GET /a=a GET /c=c GET /b=b]" {
		t.Fatalf("All = %v", entries)
	}
	table.Keys()[0] = "changed"
	if table.Keys()[0] != "GET /a" {
		t.Fatal("Keys aliases the table")
	}
	var empty RegistryTable[string]
	if _, ok := empty.Get("x"); ok || empty.Has("x") || empty.Len() != 0 || len(empty.Keys()) != 0 {
		t.Fatal("zero table is not empty")
	}
	for range empty.All() {
		t.Fatal("zero table yields an entry")
	}
}
