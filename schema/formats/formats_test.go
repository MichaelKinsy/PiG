package formats_test

import (
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/schema/formats"
)

// piFormatNames are the keys of the `formats` object in Pi's packages/schema/src/formats/index.ts.
var piFormatNames = []string{
	"date", "date-time", "duration", "email", "hostname", "idn-email", "idn-hostname", "ipv4", "ipv6", "iri", "iri-reference",
	"json-pointer", "regex", "relative-json-pointer", "time", "uri", "uri-reference", "uri-template", "uuid",
}

func TestFormatsNames(t *testing.T) {
	table := formats.Formats()
	got := slices.Sorted(maps.Keys(table))
	want := slices.Sorted(slices.Values(piFormatNames))
	if !slices.Equal(got, want) {
		t.Fatalf("Formats() names = %v, want %v", got, want)
	}
	for name, check := range table {
		if check == nil {
			t.Errorf("Formats()[%q] is nil", name)
		}
	}
}

func TestFormatsReturnsFreshMap(t *testing.T) {
	first := formats.Formats()
	first["date"] = nil
	delete(first, "uuid")
	second := formats.Formats()
	if second["date"] == nil || second["uuid"] == nil {
		t.Fatal("mutating one Formats() result changed the next")
	}
	if fmt.Sprintf("%p", first) == fmt.Sprintf("%p", second) {
		t.Fatal("Formats() returned the same map twice")
	}
}
