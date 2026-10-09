package ai

import (
	"context"
	"encoding/json"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// packages/ai/src/utils/hash.ts shortHash. Expected values come from running the pinned 1.0.4 implementation under Node.
func TestShortHashMatchesUpstreamVectors(t *testing.T) {
	long := make([]byte, 500)
	for i := range long {
		long[i] = 'x'
	}
	for input, want := range map[string]string{
		"":                "k4n83c7h0j2b",
		"a":               "m8735310ae7sx",
		"item_1":          "1k4htsumzev3m",
		"call_abc|fc_123": "jp01dsxufnom",
		"héllo wörld":     "1slrdvn1t61j5h",
		"😀 emoji":         "10l7wx41397zoo",
		string(long):      "130aesn1aldl4j",
		"\u2028\x00":      "1vtd7jj129bkx1",
	} {
		if got := ShortHash(input); got != want {
			t.Errorf("ShortHash(%q) = %q, want %q", input, got, want)
		}
	}
}

// packages/ai/src/utils/text.ts getSystemMessageText: content, then every section whose value is not null, empty parts dropped, joined by a blank line.
func TestGetSystemMessageTextRendersContentThenSections(t *testing.T) {
	value := func(text string) *string { return &text }
	for _, tc := range []struct {
		name    string
		message SystemMessage
		want    string
	}{
		{"content only", SystemMessage{Content: SystemText("lead")}, "lead"},
		{"blocks join with a newline", SystemMessage{Content: SystemTextBlocks{{Text: "a"}, {Text: "b"}}}, "a\nb"},
		{"sections follow content", SystemMessage{Content: SystemText("lead"), Sections: OrderedSections{{Name: "one", Value: value("1")}, {Name: "two", Value: value("2")}}}, "lead\n\n1\n\n2"},
		{"removed section is skipped", SystemMessage{Content: SystemText("lead"), Sections: OrderedSections{{Name: "gone", Value: nil}, {Name: "kept", Value: value("k")}}}, "lead\n\nk"},
		{"empty parts are dropped", SystemMessage{Content: SystemText(""), Sections: OrderedSections{{Name: "empty", Value: value("")}, {Name: "kept", Value: value("k")}}}, "k"},
		{"nothing", SystemMessage{Content: SystemText("")}, ""},
	} {
		if got := GetSystemMessageText(tc.message); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// packages/ai/src/utils/json-parse.ts parseJsonWithRepair: strict parse first, then the repaired text, and the original error when repair changes nothing.
func TestParseJSONWithRepairStrictRepairedAndFailing(t *testing.T) {
	type payload struct {
		Text string `json:"text"`
	}
	if got, err := ParseJSONWithRepair[payload](`{"text":"ok"}`); err != nil || got.Text != "ok" {
		t.Errorf("strict = %+v, %v", got, err)
	}
	if got, err := ParseJSONWithRepair[payload]("{\"text\":\"a\tb\"}"); err != nil || got.Text != "a\tb" {
		t.Errorf("raw control character = %+v, %v", got, err)
	}
	if _, err := ParseJSONWithRepair[payload](`{"text":`); err == nil {
		t.Error("truncated JSON parsed")
	}
}

// packages/ai/src/utils/estimate.ts estimateTextAndImageContentTokens: strings count their UTF-16 length, text blocks likewise, each image 4800 characters, rounded up at 3.5 characters per token.
func TestEstimateTextAndImageContentTokensMatchesUpstreamArithmetic(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content UserContent
		want    int
	}{
		{"string", UserText("abcde"), 2},
		{"astral string counts two units per character", UserText("😀😀😀"), 2},
		{"empty string", UserText(""), 0},
		{"text blocks", UserContentBlocks{TextContent{Text: "abcd"}, TextContent{Text: "e"}}, 2},
		{"image block", UserContentBlocks{ImageContent{Data: "x", MimeType: "image/png"}}, 1372},
		{"text and image", UserContentBlocks{TextContent{Text: "abcd"}, ImageContent{Data: "x", MimeType: "image/png"}}, 1373},
	} {
		if got := EstimateTextAndImageContentTokens(tc.content); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}

// packages/ai/src/api/constrained-sampling.ts makeStrictJsonSchema(schema, isUnsupportedKeyword): the callback sees every keyword of every schema node, and a rejected keyword fails with `<key>: <JSON value> is unsupported`.
func TestMakeStrictJSONSchemaAppliesTheUnsupportedKeywordCheck(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string", "pattern": "^a"}}}
	var seen []string
	_, err := MakeStrictJSONSchema(schema, func(key string, value any) bool {
		seen = append(seen, key)
		return key == "pattern"
	})
	if err == nil || err.Error() != `pattern: "^a" is unsupported` {
		t.Fatalf("error = %v", err)
	}
	if !slices.Contains(seen, "type") || !slices.Contains(seen, "properties") {
		t.Errorf("check skipped keywords: %v", seen)
	}
	if _, err := MakeStrictJSONSchema(schema, nil); err != nil {
		t.Errorf("no check rejected the schema: %v", err)
	}
}

// packages/ai/src/models.ts createProvider: `name` defaults to `id`, and `baseUrl` and `headers` are carried onto the provider unchanged.
func TestCreateProviderCarriesNameBaseURLAndHeaders(t *testing.T) {
	auth := ProviderAuth{APIKey: EnvAPIKeyAuth("Test API key", "TEST_API_KEY")}
	headers := ProviderHeaders{"x-source": new("provider"), "x-drop": nil}
	provider := CreateProvider(CreateProviderOptions{ID: "plain", Auth: auth, API: &ProviderStreams{Stream: StreamSimple, StreamSimple: StreamSimple}, BaseURL: "https://provider.test/v1", Headers: headers})
	if provider.Name != "plain" || provider.BaseURL != "https://provider.test/v1" || !reflect.DeepEqual(provider.Headers, headers) {
		t.Errorf("provider = %q %q %v", provider.Name, provider.BaseURL, provider.Headers)
	}
	named := CreateProvider(CreateProviderOptions{ID: "plain", Name: new("Display"), Auth: auth, API: &ProviderStreams{Stream: StreamSimple, StreamSimple: StreamSimple}})
	if named.Name != "Display" || named.BaseURL != "" || named.Headers != nil {
		t.Errorf("named = %q %q %v", named.Name, named.BaseURL, named.Headers)
	}
}

// packages/ai/src/providers/typesafe.ts typesafeProvider: id "typesafe", name "TypeSafe", an API-key auth, the TypeSafe classifier models only, and the typesafe-system-one classifier.
func TestTypesafeProviderServesOnlyClassifierModels(t *testing.T) {
	provider := TypesafeProvider()
	if provider.ID != "typesafe" || provider.Name != "TypeSafe" || provider.Auth.APIKey == nil {
		t.Fatalf("provider = %q %q auth=%+v", provider.ID, provider.Name, provider.Auth)
	}
	all, err := provider.GetAllModels()
	if err != nil || len(all) == 0 {
		t.Fatalf("models = %d, %v", len(all), err)
	}
	for _, model := range all {
		if _, ok := model.(*ClassifierModel); !ok {
			t.Errorf("non-classifier model %T", model)
		}
	}
	if len(all) != len(GetBuiltinClassifierModels("typesafe")) {
		t.Errorf("provider models %d, built-in classifiers %d", len(all), len(GetBuiltinClassifierModels("typesafe")))
	}
	if chat, err := provider.GetModels(); err != nil || len(chat) != 0 {
		t.Errorf("chat models = %d, %v", len(chat), err)
	}
	if provider.Classify == nil || provider.GenerateImages != nil {
		t.Errorf("implementations: classify=%v images=%v", provider.Classify != nil, provider.GenerateImages != nil)
	}
}

// packages/ai/src/providers/faux.ts FauxResponseFactory: the factory receives the transcript, the request options, the provider state and the model, and its response becomes the stream's message.
func TestFauxResponseFactoryReceivesTranscriptOptionsStateAndModel(t *testing.T) {
	faux := NewFauxProvider(FauxConfig{})
	var gotMessages int
	var gotSession string
	var gotModel *Model
	var gotState *FauxProviderState
	var factory FauxResponseFactory = func(transcript TranscriptContext, options StreamOptions, state *FauxProviderState, model *Model) (AssistantMessage, error) {
		gotMessages = len(transcript.Messages())
		gotSession, gotModel, gotState = options.SessionID, model, state
		return FauxResponse{Content: []FauxContentBlock{FauxText("from factory")}, StopReason: "stop"}.AssistantMessage(), nil
	}
	faux.SetResponses([]FauxResponseStep{FauxFactoryStep(factory)})
	models := CreateModels(CreateModelsOptions{})
	defer models.Close()
	models.SetProvider(faux.Provider())
	result := models.CompleteSimple(t.Context(), faux.GetModel(), Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}, StreamOptions{SessionID: "session-1"})
	if gotMessages != 1 || gotSession != "session-1" || gotModel == nil || gotState == nil || result.StopReason != StopReasonStop || len(result.Content) != 1 {
		t.Errorf("factory args: messages=%d session=%q model=%v state=%v result=%+v", gotMessages, gotSession, gotModel, gotState, result)
	}
}

// packages/ai/src/utils/overflow.ts getOverflowPatterns: a copy of OVERFLOW_PATTERNS, in order, with the same sources (every upstream pattern is case-insensitive, compiled with JavaScript's /i folding as production does).
func TestGetOverflowPatternsMatchesUpstreamList(t *testing.T) {
	data, err := os.ReadFile("../.upstream/current/packages/ai/src/utils/overflow.ts")
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile(`(?s)const OVERFLOW_PATTERNS = \[(.*?)\n\];`).FindStringSubmatch(string(data))
	if block == nil {
		t.Fatal("OVERFLOW_PATTERNS not found")
	}
	var want []string
	for line := range strings.SplitSeq(block[1], "\n") {
		if match := regexp.MustCompile(`^\s*/(.+)/i,`).FindStringSubmatch(line); match != nil {
			want = append(want, lazyregexp.NewJSIgnoreCase(strings.ReplaceAll(match[1], `\s`, jsSpaceClass)).Regexp().String())
		}
	}
	patterns := GetOverflowPatterns()
	var got []string
	for _, pattern := range patterns {
		got = append(got, pattern.String())
	}
	if !slices.Equal(got, want) {
		t.Errorf("patterns differ:\n got %q\nwant %q", got, want)
	}
	patterns[0] = nil
	if GetOverflowPatterns()[0] == nil {
		t.Error("GetOverflowPatterns returned the registry slice instead of a copy")
	}
}

// packages/ai/src/providers/faux.ts model definitions: `cost` becomes the model's price (zeros when absent) and `inputLimits` is carried through (absent stays absent). Expected values come from the pinned 1.0.4 implementation under Node.
// Pi: packages/ai/src/providers/faux.ts:47 (cost).
// Pi: packages/ai/src/providers/faux.ts:46 (inputLimits).
func TestFauxModelDefinitionCostAndInputLimits(t *testing.T) {
	faux := NewFauxProvider(FauxConfig{Models: []FauxModelDefinition{
		{ID: "priced", Cost: &ModelCost{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4}, InputLimits: &ModelInputLimits{MaxRequestBytes: 1234, Images: &ModelImageInputLimits{MaxPerRequest: 5}}},
		{ID: "free"},
	}})
	priced, free := faux.GetModel("priced"), faux.GetModel("free")
	if c := priced.Capabilities; c.InputCostPer1M != 1 || c.OutputCostPer1M != 2 || c.CacheReadCostPer1M != 3 || c.CacheWriteCostPer1M != 4 {
		t.Errorf("priced capabilities = %+v", c)
	}
	if !reflect.DeepEqual(priced.InputLimits, &ModelInputLimits{MaxRequestBytes: 1234, Images: &ModelImageInputLimits{MaxPerRequest: 5}}) {
		t.Errorf("priced limits = %+v", priced.InputLimits)
	}
	if c := free.Capabilities; c.InputCostPer1M != 0 || c.OutputCostPer1M != 0 || c.CacheReadCostPer1M != 0 || c.CacheWriteCostPer1M != 0 || free.InputLimits != nil {
		t.Errorf("free model = %+v limits=%v", c, free.InputLimits)
	}
}

// packages/ai/src/providers/faux.ts FauxProviderState.cancelledDeferred: cancelDeferred records a copy of each handle in order, and a response factory sees them on its state (providers.test.ts:816).
// mutation-checked: the mutant "the faux provider ignores StreamOptions.Deferred" (ai/faux.go, the deferred branch of the stream) fails it.
func TestFauxProviderStateRecordsCancelledDeferredHandles(t *testing.T) {
	faux := NewFauxProvider(FauxConfig{Deferred: &FauxDeferredConfig{PendingFetches: 1}})
	models := CreateModels(CreateModelsOptions{})
	defer models.Close()
	models.SetProvider(faux.Provider())
	var seen []DeferredHandle
	faux.SetResponses([]FauxResponseStep{
		FauxStaticStep(FauxResponse{Content: []FauxContentBlock{FauxText("first")}, StopReason: "stop"}),
		FauxFactoryStep(func(_ TranscriptContext, _ StreamOptions, state *FauxProviderState, _ *Model) (AssistantMessage, error) {
			seen = state.CancelledDeferred()
			return FauxResponse{Content: []FauxContentBlock{FauxText("second")}, StopReason: "stop"}.AssistantMessage(), nil
		}),
	})
	model := faux.GetModel()
	request := Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}
	submission := models.CompleteSimple(t.Context(), model, request, StreamOptions{Deferred: &DeferredOption{Enabled: true}})
	if submission.Deferred == nil {
		t.Fatalf("no deferred handle: %+v", submission)
	}
	cancelled := *submission.Deferred
	cancelled.Data = map[string]any{"cursor": "c1"}
	if err := models.CancelDeferred(t.Context(), model, cancelled, DeferredCancelOptions{}); err != nil {
		t.Fatal(err)
	}
	models.CompleteSimple(t.Context(), model, request, StreamOptions{})
	if !reflect.DeepEqual(seen, []DeferredHandle{cancelled}) {
		t.Errorf("factory saw %+v, want %+v", seen, cancelled)
	}
	exposed := faux.state.CancelledDeferred()
	exposed[0].ID = "mutated"
	exposed[0].Data.(map[string]any)["cursor"] = "mutated"
	cancelled.Data.(map[string]any)["cursor"] = "caller mutated"
	if got := faux.CancelledDeferred(); len(got) != 1 || got[0].ID != submission.Deferred.ID || !reflect.DeepEqual(got[0].Data, map[string]any{"cursor": "c1"}) {
		t.Errorf("recorded handles are not independent deep copies: %+v", got)
	}
}

// packages/ai/src/types.ts TextSignatureV1 and openai-responses-shared.ts encodeTextSignatureV1/parseTextSignature: the signature is `{"v":1,"id":...}` with `phase` only for commentary and final_answer; anything else is a legacy plain id.
func TestTextSignatureV1WireShapeAndParsing(t *testing.T) {
	for _, tc := range []struct{ id, phase, want string }{
		{"msg_1", "", `{"v":1,"id":"msg_1"}`},
		{"msg_1", "commentary", `{"v":1,"id":"msg_1","phase":"commentary"}`},
		{"msg_1", "final_answer", `{"v":1,"id":"msg_1","phase":"final_answer"}`},
		{"msg_1", "other", `{"v":1,"id":"msg_1"}`},
	} {
		if got := encodeResponsesTextSignature(tc.id, tc.phase); got != tc.want {
			t.Errorf("encode(%q,%q) = %s, want %s", tc.id, tc.phase, got, tc.want)
		}
	}
	encoded, err := json.Marshal(TextSignatureV1{V: 1, ID: "a", Phase: "commentary"})
	if err != nil || string(encoded) != `{"v":1,"id":"a","phase":"commentary"}` {
		t.Errorf("TextSignatureV1 json = %s, %v", encoded, err)
	}
	for _, tc := range []struct{ in, id, phase string }{
		{`{"v":1,"id":"x","phase":"final_answer"}`, "x", "final_answer"},
		{`{"v":1,"id":"x","phase":"bogus"}`, "x", ""},
		{`{"v":2,"id":"x"}`, `{"v":2,"id":"x"}`, ""},
		{`{"v":1}`, `{"v":1}`, ""},
		{`{broken`, `{broken`, ""},
		{"legacy_id", "legacy_id", ""},
	} {
		id, phase, ok := parseResponsesTextSignature(tc.in)
		if !ok || id != tc.id || phase != tc.phase {
			t.Errorf("parse(%q) = %q %q %v", tc.in, id, phase, ok)
		}
	}
	if _, _, ok := parseResponsesTextSignature(""); ok {
		t.Error("empty signature parsed")
	}
}

// packages/ai/src/types.ts ClassifierFunction: a classifier API function never throws; a failed request comes back as a result with stopReason "error", an errorMessage and the model's identity.
func TestClassifierFunctionsReportFailuresInTheResult(t *testing.T) {
	functions := map[string]ClassifierFunction{
		"llama-cpp":                        ClassifyLlamaCpp,
		"typesafe-system-one":              ClassifyTypesafeSystemOne,
		"cloudflare-workers-ai-system-one": ClassifyCloudflareWorkersAISystemOne,
	}
	for name, classify := range functions {
		t.Run(name, func(t *testing.T) {
			model := classifierTestModel("test", "model-1")
			model.API = ClassifierAPI(name)
			client := clsClient(func(*http.Request) (*http.Response, error) { return clsText(500, "boom", nil), nil })
			result := classify(t.Context(), *model, classifierTestContext(), ClassifierOptions{APIKey: "secret", APIKeySet: true, Fetch: client})
			if result.StopReason != ClassifierStopReasonError || result.ErrorMessage == "" || result.API != model.API || result.Provider != "test" || result.Model != "model-1" {
				t.Errorf("result = %+v", result)
			}
		})
	}
}

// packages/ai/src/types.ts ImagesFunction and images.ts generateImages: the registered function for the model's api receives the model, context and options unchanged and its result is returned as is; an unregistered api is an error.
func TestImagesFunctionReceivesTheDispatchedRequest(t *testing.T) {
	model := ImageModel{ID: "m", Name: "M", API: "images-function-test", Provider: "p", BaseURL: "https://example.test", Input: []string{"text"}, Output: []string{"image"}}
	request := ImagesContext{Input: []ContentBlock{TextContent{Text: "square"}}}
	var gotModel ImageModel
	var gotRequest ImagesContext
	var gotOptions ProviderImagesOptions
	var generate ImagesFunction = func(_ context.Context, m ImageModel, c ImagesContext, o ProviderImagesOptions) AssistantImages {
		gotModel, gotRequest, gotOptions = m, c, o
		return AssistantImages{API: m.API, Provider: m.Provider, Model: m.ID, Output: []ContentBlock{TextContent{Text: "done"}}, StopReason: ImagesStopReasonStop, ResponseID: "r1"}
	}
	if _, err := GenerateImages(t.Context(), model, request, ImagesOptions{}); err == nil || err.Error() != "No API provider registered for api: images-function-test" {
		t.Fatalf("unregistered error = %v", err)
	}
	RegisterImagesAPIProvider(ImagesAPIProvider{API: model.API, GenerateImages: generate})
	t.Cleanup(func() {
		imagesAPIProviderMu.Lock()
		defer imagesAPIProviderMu.Unlock()
		delete(imagesAPIProviderRegistry, model.API)
	})
	result, err := GenerateImages(t.Context(), model, request, ImagesOptions{APIKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotModel, model) || !reflect.DeepEqual(gotRequest, request) || gotOptions.APIKey != "key" || result.ResponseID != "r1" || result.StopReason != ImagesStopReasonStop {
		t.Errorf("dispatch: model=%+v request=%+v options=%+v result=%+v", gotModel, gotRequest, gotOptions, result)
	}
}
