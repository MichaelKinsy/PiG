package jsloop

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// copiedFiles are the files the SDK module carries verbatim. Each is the implementation of one primitive or the behavior table that pins it, so one table runs against both copies.
var copiedFiles = []string{"settle_gate.go", "settle_gate_table_test.go", "lane.go", "lane_table_test.go"}

func TestSDKCopy(t *testing.T) {
	sdk := filepath.Join("..", "..", "extensions", "sdk", "internal", "jsloop")
	for _, name := range copiedFiles {
		want, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(sdk, name))
		if err != nil {
			t.Fatalf("the SDK copy of %s: %v", name, err)
		}
		if !bytes.Equal(want, got) {
			t.Errorf("extensions/sdk/internal/jsloop/%s differs from internal/jsloop/%s; copy it over", name, name)
		}
	}
}
