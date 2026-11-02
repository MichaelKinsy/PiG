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
	"json-pointer", "json-pointer-uri-fragment", "regex", "relative-json-pointer", "time", "uri", "uri-reference", "uri-template", "url", "uuid",
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

// TestSplitSurrogatePairs checks that a surrogate pair written as two WTF-8 halves, which is the same JavaScript string as the pair's UTF-8 spelling, gets the same result in every format.
func TestSplitSurrogatePairs(t *testing.T) {
	const pair, split = "\U00010000", "\xed\xa0\x80\xed\xb0\x80"
	samples := []string{"a@%s.com", "%s@b.com", "%s.com", "http://a/%s", "http://%s.com/", "/%s", "%s", "a/%s", "{a}%s", "/%s~0", "xn--%s"}
	for name, check := range formats.Formats() {
		if name == "regex" {
			continue
		}
		for _, sample := range samples {
			want := check(fmt.Sprintf(sample, pair))
			if got := check(fmt.Sprintf(sample, split)); got != want {
				t.Errorf("%s(%q) = %v, but %q is %v", name, fmt.Sprintf(sample, split), got, fmt.Sprintf(sample, pair), want)
			}
		}
	}
}
