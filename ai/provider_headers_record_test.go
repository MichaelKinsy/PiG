package ai

import (
	"reflect"
	"testing"
)

// upstream: packages/ai/src/utils/headers.ts providerHeadersToRecord merges the sources case-insensitively in order: a later
// source replaces an earlier header of any casing and keeps its own spelling, a null value removes it, and no header is undefined.
func TestProviderHeadersToRecordMergesCaseInsensitivelyInOrder(t *testing.T) {
	got := providerHeadersToRecord(
		ProviderHeaders{"Accept": new("a"), "X-Keep": new("k")},
		nil,
		ProviderHeaders{"accept": new("b"), "X-Drop": new("d")},
		ProviderHeaders{"x-drop": nil},
	)
	want := map[string]string{"X-Keep": "k", "accept": "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("headers = %v, want %v", got, want)
	}
	for name, sources := range map[string][]ProviderHeaders{
		"every header removed":              {nil, {"A": nil}},
		"a header removed after it was set": {{"A": new("1")}, {"a": nil}},
		"no sources":                        nil,
		"empty source":                      {{}},
	} {
		if got := providerHeadersToRecord(sources...); got != nil {
			t.Errorf("%s: record = %#v, want undefined (nil)", name, got)
		}
	}
}
