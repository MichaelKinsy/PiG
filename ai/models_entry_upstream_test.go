package ai

import (
	"reflect"
	"testing"
)

// .upstream/v0.99.2/packages/ai/test/models-entry.test.ts:31 "lightweight models entry runs a faux completion without TypeBox, catalogs, or SDKs".
// The upstream module-load hook (lines 9-28) forbids importing typebox, provider SDKs and the generated catalog; Go providers are statically linked, so only
// the behavior the spawned script asserts (lines 36-46) is portable. CreateModels is the `createModels` entry.
func TestLightweightModelsEntryUpstream(t *testing.T) {
	models := CreateModels(CreateModelsOptions{})
	defer models.Close()
	if got := models.GetModels(); len(got) != 0 {
		t.Fatalf("models.getModels() = %#v, want empty", got)
	}
	if got := models.GetProviders(); len(got) != 0 {
		t.Fatalf("models.getProviders() = %#v, want empty", got)
	}
	faux := NewFauxProvider(FauxConfig{})
	models.SetProvider(faux.Provider())
	faux.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{Content: []FauxContentBlock{FauxText("OK")}, StopReason: "stop"})})
	response := models.CompleteSimple(t.Context(), faux.GetModel(), Context{Messages: []Message{}}, StreamOptions{})
	if response.StopReason != StopReasonStop {
		t.Fatalf("stopReason = %q (%s), want stop", response.StopReason, response.ErrorMessage)
	}
	if want := []AssistantContentBlock{TextContent{Text: "OK"}}; !reflect.DeepEqual(response.Content, want) {
		t.Fatalf("content = %#v, want %#v", response.Content, want)
	}
}
