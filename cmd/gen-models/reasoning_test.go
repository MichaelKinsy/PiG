package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVendorReasoningCatalogIngestion(t *testing.T) {
	dir := t.TempDir()
	modelsDev := filepath.Join(dir, "models-dev.json")
	openRouter := filepath.Join(dir, "openrouter.json")
	for path, data := range map[string]string{modelsDev: `{"google":{"models":{"gemini-test":{"reasoning_options":[{"type":"effort","values":["low","high"]}]}}}}`, openRouter: `{"data":[{"id":"stealth/ox-alpha","reasoning":{"mandatory":true,"supported_efforts":["max"]}}]}`} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	rows := []modelRow{{Provider: "google", ID: "gemini-test"}, {Provider: "google-vertex", ID: "gemini-test"}, {Provider: "openrouter", ID: "stealth/ox-alpha"}, {Provider: "unrelated", ID: "kept", ThinkingLevelMap: map[string]*string{"high": new("high")}}}
	if err := applyVendorReasoning(rows, modelsDev, openRouter); err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{0, 1} {
		if rows[i].ThinkingLevelMap == nil || rows[i].ThinkingLevelMap["low"] == nil || *rows[i].ThinkingLevelMap["low"] != "low" || rows[i].ThinkingLevelMap["off"] != nil {
			t.Fatal("models.dev map lost", rows[i])
		}
	}
	if rows[2].ThinkingLevelMap["max"] == nil || *rows[2].ThinkingLevelMap["max"] != "max" || rows[2].ThinkingLevelMap["off"] != nil {
		t.Fatal("OpenRouter map lost")
	}
	if *rows[3].ThinkingLevelMap["high"] != "high" {
		t.Fatal("unrelated row changed")
	}
	output := filepath.Join(dir, "models.go")
	if err := emit(output, "vendor snapshots", rows); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `ThinkingLevel("off"): nil`) || !strings.Contains(string(raw), `ThinkingLevel("max"): ptrString("max")`) {
		t.Fatalf("derived controls absent from generated catalog: %s", raw)
	}
}
