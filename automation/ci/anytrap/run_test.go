// SPDX-License-Identifier: MIT

package main

import (
	"path/filepath"
	"slices"
	"testing"
)

func sampleFindings(t *testing.T) []finding {
	t.Helper()
	t.Setenv("GOWORK", "off") // the sample is its own module, outside the repository workspace
	root, err := filepath.Abs(filepath.Join("testdata", "sample"))
	if err != nil {
		t.Fatal(err)
	}
	pkgs, err := load(root, []string{"./..."})
	if err != nil {
		t.Fatal(err)
	}
	return scan(root, pkgs)
}

// Decodes into any, map[string]any, []any and a type parameter are found, with a count per call and destination; a struct and a json.RawMessage are not.
func TestScanFindsInterfaceDestinationsOnly(t *testing.T) {
	want := []finding{
		{File: "sample.go", Func: "Boxed", Call: "json.Decode", Dest: "*any", Count: 1},
		{File: "sample.go", Func: "Boxed", Call: "json.Unmarshal", Dest: "*any", Count: 1},
		{File: "sample.go", Func: "Decode", Call: "json.Unmarshal", Dest: "*T", Count: 1},
		{File: "sample.go", Func: "Holder.Loose", Call: "json.Unmarshal", Dest: "*map[string]any", Count: 1},
		{File: "sample.go", Func: "List", Call: "json.Unmarshal", Dest: "*[]any", Count: 1},
	}
	if got := sampleFindings(t); !slices.Equal(got, want) {
		t.Fatalf("findings\n got %+v\nwant %+v", got, want)
	}
}

// The ratchet fails a new decode, a raised count, a lowered count and a vanished decode, and accepts an exact match.
func TestCompareRatchetsInBothDirections(t *testing.T) {
	found := sampleFindings(t)
	if grew, stale := compare(found, found); len(grew)+len(stale) != 0 {
		t.Fatalf("exact baseline reported %v %v", grew, stale)
	}
	if grew, _ := compare(found, found[1:]); len(grew) != 1 {
		t.Fatalf("a decode missing from the baseline must fail, got %v", grew)
	}
	raised := slices.Clone(found)
	raised[0].Count++
	if _, stale := compare(found, raised); len(stale) != 1 {
		t.Fatalf("a baseline above the count must fail, got %v", stale)
	}
	lowered := slices.Clone(found)
	lowered[0].Count--
	if grew, _ := compare(found, lowered); len(grew) != 1 {
		t.Fatalf("a count above the baseline must fail, got %v", grew)
	}
	extra := append(slices.Clone(found), finding{File: "gone.go", Func: "F", Call: "json.Unmarshal", Dest: "*any", Count: 1})
	if _, stale := compare(found, extra); len(stale) != 1 {
		t.Fatalf("a baseline entry with no decode must fail, got %v", stale)
	}
}

// The SDK's copy of the codec decodes with the same argument positions as encoding/json, and any other package's Unmarshal is not a decode this gate counts.
func TestDestinationArgumentKnowsTheSDKCodec(t *testing.T) {
	for _, path := range []string{"encoding/json", "encoding/json/v2", sdkJSONPath} {
		for name, want := range map[string]int{"Unmarshal": 1, "UnmarshalRead": 1, "UnmarshalDecode": 1, "Decode": 0} {
			if got, ok := destinationArgument(path, name); !ok || got != want {
				t.Errorf("destinationArgument(%q, %q) = %d, %v; want %d", path, name, got, ok, want)
			}
		}
		if _, ok := destinationArgument(path, "Marshal"); ok {
			t.Errorf("%s.Marshal is not a decode", path)
		}
	}
	if _, ok := destinationArgument("gopkg.in/yaml.v3", "Unmarshal"); ok {
		t.Error("yaml.Unmarshal is not counted")
	}
}
