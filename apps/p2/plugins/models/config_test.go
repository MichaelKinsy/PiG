package models

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

func settingsOf(t *testing.T, text string) *chordjson.Object {
	t.Helper()
	value, err := chordjson.Decode([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return value.(*chordjson.Object)
}

// Ports "keeps the settings' thinking level when the default model is not listed" (test/p2/lifecycle.test.ts).
func TestSummarizeKeepsSettingsThinkingLevelForUnlistedDefault(t *testing.T) {
	summary, err := summarizeSnapshot(contract.ResourceSnapshot{
		Settings: settingsOf(t, `{"defaultModel":{"provider":"gone","modelId":"model"},"thinkingLevel":"low"}`),
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	want := contract.ModelChoice{Provider: "gone", ModelID: "model", ThinkingLevel: "low"}
	if summary.DefaultModel == nil || *summary.DefaultModel != want {
		t.Fatalf("default = %+v, want %+v", summary.DefaultModel, want)
	}
}

// Ports the summarize half of "takes the default model from P2_PROVIDER/P2_MODEL (or Pi Mobile's names) after
// settings.json's" (test/p2/lifecycle.test.ts).
func TestSummarizeDefaultModelOrder(t *testing.T) {
	faux := contract.ModelInfo{Provider: "faux", ModelID: "faux-1", Name: "Faux", ThinkingLevels: []string{"off", "high"}, ContextWindow: 1}
	snapshot := func(settings string) contract.ResourceSnapshot {
		return contract.ResourceSnapshot{Settings: settingsOf(t, settings), Models: []contract.ModelInfo{faux}}
	}
	t.Setenv("P2_PROVIDER", "env")
	t.Setenv("P2_MODEL", "model")
	summary, err := summarizeSnapshot(snapshot(`{}`), true)
	if err != nil || summary.DefaultModel == nil || summary.DefaultModel.Provider != "env" {
		t.Fatalf("the environment's default comes after settings.json's: %+v, %v", summary.DefaultModel, err)
	}
	summary, err = summarizeSnapshot(snapshot(`{"defaultModel":{"provider":"faux","modelId":"faux-1"}}`), true)
	if err != nil || summary.DefaultModel == nil || summary.DefaultModel.Provider != "faux" || summary.DefaultModel.ThinkingLevel != "high" {
		t.Fatalf("settings.json's default wins: %+v, %v", summary.DefaultModel, err)
	}
	t.Setenv("P2_MODEL", "")
	if _, err := summarizeSnapshot(snapshot(`{}`), true); err == nil || err.Error() != "Set both P2_PROVIDER and P2_MODEL" {
		t.Fatalf("half-set environment: %v", err)
	}
	t.Setenv("P2_PROVIDER", "")
	summary, err = summarizeSnapshot(snapshot(`{}`), true)
	if err != nil || summary.DefaultModel == nil || summary.DefaultModel.Provider != "faux" {
		t.Fatalf("the first listed model otherwise: %+v, %v", summary.DefaultModel, err)
	}
	// The thinking level is clamped to the model's levels.
	summary, _ = summarizeSnapshot(snapshot(`{"thinkingLevel":"xhigh"}`), true)
	if summary.DefaultModel.ThinkingLevel != "high" {
		t.Fatalf("clamped level = %q", summary.DefaultModel.ThinkingLevel)
	}
	// A provider that is not signed in lists no models.
	summary, _ = summarizeSnapshot(snapshot(`{}`), false)
	if len(summary.Models) != 0 || summary.DefaultModel != nil {
		t.Fatalf("signed out: %+v", summary)
	}
}
