package ai

import "testing"

// TestOpenAIResponses_ImageAndReasoningResolveOneCatalogEntry pins the unified
// model-resolution precedence for the responses provider. Upstream carries a
// single resolved `model` object and reads both image support
// (model.input.includes("image")) and the reasoning/thinking limits off it. pig
// reconstructs that one entry in resolveResponsesModel and derives every
// capability from it, so image gating and thinking clamping can never resolve
// two different catalog entries.
//
// The fixture exploits a real catalog split: openai-codex/gpt-5.3-codex-spark is
// image-incapable, but the bare id gpt-5.3-codex-spark resolves to an
// image-capable azure entry. A codex-responses provider must gate images on the
// codex entry (codex-first precedence): the same entry that drives its
// reasoning clamp: not borrow the bare azure entry's capabilities.
func TestOpenAIResponses_ImageAndReasoningResolveOneCatalogEntry(t *testing.T) {
	p := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{
		ProviderID: string(APIOpenAICodexResponses),
		Model:      "gpt-5.3-codex-spark",
	}}

	generated, ok := p.resolveResponsesModel()
	if !ok {
		t.Fatal("resolveResponsesModel: no entry resolved")
	}
	// Codex-first precedence: the codex catalog entry, not the bare azure borrow.
	if generated.Provider != "openai-codex" {
		t.Fatalf("resolved provider = %q, want openai-codex (codex-first precedence)", generated.Provider)
	}
	// Images gate on the resolved codex entry (false), not the bare azure entry (true).
	if p.modelSupportsImages() {
		t.Error("modelSupportsImages = true, want false: image gating must read the codex entry, not the bare azure borrow")
	}
	// One entry: modelSupportsImages reads the same entry resolveResponsesModel returns.
	if p.modelSupportsImages() != generated.ToCapabilities().SupportsImages {
		t.Error("modelSupportsImages disagrees with resolveResponsesModel: image gating resolved a different entry than the reasoning clamp")
	}
	// The reasoning view is derived from the same resolved entry.
	if got := p.resolvedModel().ProviderMeta.ProviderID; got != generated.Provider {
		t.Errorf("resolvedModel provider = %q, want %q (same resolved entry)", got, generated.Provider)
	}
}

// TestOpenAIResponses_ResolveModelProviderFirstForNonCodex pins that a
// non-codex responses provider resolves provider-first then bare, unchanged from
// before the unification, so the reasoning wire stays byte-identical.
func TestOpenAIResponses_ResolveModelProviderFirstForNonCodex(t *testing.T) {
	p := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{ProviderID: "openai", Model: "gpt-5"}}
	generated, ok := p.resolveResponsesModel()
	if !ok {
		t.Fatal("resolveResponsesModel: no entry resolved")
	}
	if generated.Provider != "openai" {
		t.Fatalf("resolved provider = %q, want openai (provider-first)", generated.Provider)
	}
	if !p.modelSupportsImages() {
		t.Error("modelSupportsImages = false, want true for openai/gpt-5")
	}
}
