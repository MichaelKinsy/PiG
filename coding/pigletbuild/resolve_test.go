package pigletbuild

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
)

// extensionInputsFromCells must derive fusibility solely from the cell strategy,
// so the runtime cell planner remains the single classifier of record.
func TestExtensionInputsFromCells(t *testing.T) {
	cells := []subprocess.CellSpec{
		{
			Strategy: subprocess.CellStrategyPackedGo, Language: "go",
			Extensions: []subprocess.ExtConfig{{Name: "context-info"}, {Name: "subagent"}},
		},
		{
			Strategy: subprocess.CellStrategyPackedRust, Language: "rust",
			Extensions: []subprocess.ExtConfig{{Name: "rust-helper"}},
		},
		{
			Strategy: subprocess.CellStrategyIsolated, Language: "go",
			Extensions: []subprocess.ExtConfig{{Name: "legacy-go"}},
		},
	}

	got := extensionInputsFromCells(cells)
	byName := map[string]ExtensionInput{}
	for _, e := range got {
		byName[e.Name] = e
	}

	if e := byName["context-info"]; !e.Fusible || e.Language != Go || e.NotFusibleReason != "" {
		t.Errorf("context-info = %+v, want fusible go with no reason", e)
	}
	if e := byName["subagent"]; !e.Fusible {
		t.Errorf("subagent should share the packed-go cell's fusibility, got %+v", e)
	}
	if e := byName["rust-helper"]; e.Fusible || e.Language != Rust || e.NotFusibleReason != "language:rust" {
		t.Errorf("rust-helper = %+v, want not-fusible rust with reason language:rust", e)
	}
	if e := byName["legacy-go"]; e.Fusible || e.NotFusibleReason != "go: not a packable factory" {
		t.Errorf("legacy-go = %+v, want not-fusible with 'go: not a packable factory'", e)
	}
}

// A Pig 0.84 factory imports the legacy SDK module path, a distinct Go type from
// the fused registry's sdk.Factory. It must stay a subprocess cell (AK-001).
func TestLegacySDKPackedGoCellIsNotFusible(t *testing.T) {
	legacy := subprocess.CellSpec{
		Key: "packed-go:legacy", Strategy: subprocess.CellStrategyPackedGo, Language: "go", SDK: extsource.LegacyGoSDKModulePath,
		Extensions: []subprocess.ExtConfig{{Name: "ask", SDKName: extsource.LegacyGoSDKModulePath, Source: "/ext/ask", Package: "example.com/ask", ModulePath: "example.com/ask", Factory: "Extension"}},
	}
	current := subprocess.CellSpec{
		Key: "packed-go:current", Strategy: subprocess.CellStrategyPackedGo, Language: "go", SDK: extsource.GoSDKModulePath,
		Extensions: []subprocess.ExtConfig{{Name: "current", SDKName: extsource.GoSDKModulePath}},
	}
	byName := map[string]ExtensionInput{}
	for _, e := range extensionInputsFromCells([]subprocess.CellSpec{legacy, current}) {
		byName[e.Name] = e
	}
	if e := byName["ask"]; e.Fusible || e.NotFusibleReason != "go: legacy SDK module "+extsource.LegacyGoSDKModulePath {
		t.Errorf("ask = %+v, want not-fusible legacy SDK", e)
	}
	if e := byName["current"]; !e.Fusible || e.NotFusibleReason != "" {
		t.Errorf("current = %+v, want fusible", e)
	}
	if _, err := collectFused(legacy); err == nil || !strings.Contains(err.Error(), "legacy SDK module") {
		t.Fatalf("collectFused(legacy) error = %v", err)
	}
}
