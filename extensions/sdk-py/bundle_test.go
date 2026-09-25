package pysdk

import (
	"slices"
	"strings"
	"testing"
)

func TestSourceReadable(t *testing.T) {
	for _, name := range BundledFiles() {
		data, err := Source.ReadFile(name)
		if err != nil {
			t.Fatalf("embedded %s: %v", name, err)
		}
		if len(data) == 0 {
			t.Fatalf("embedded %s is empty", name)
		}
	}
	if !slices.Contains(BundledFiles(), "pig_sdk/__init__.py") {
		t.Fatal("BundledFiles must include the pig_sdk package init so the staged SDK is importable")
	}
	data, err := Source.ReadFile("pig_sdk/__init__.py")
	if err != nil || !strings.Contains(string(data), "class Extension") {
		t.Fatalf("embedded pig_sdk/__init__.py is not the SDK module: err=%v", err)
	}
}
