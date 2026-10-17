package ai

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// Direct tests for small exported helpers that the interface ledger binds to upstream functions. Each case mirrors the
// pinned 1.0.4 source of the named function; the upstream package has no test file that names them.

// packages/ai/src/utils/model-operations.ts assertChatModel, assertImageModel, assertClassifierModel.
func TestAssertModelTypeRejectsOtherModelTypesLikeUpstream(t *testing.T) {
	chat := typedChatModel("p", "m")
	image := typedImageModel("p", "m")
	classifier := classifierTestModel("p", "m")
	if err := AssertChatModel(chat); err != nil {
		t.Fatalf("chat model rejected: %v", err)
	}
	if err := AssertImageModel(image); err != nil {
		t.Fatalf("image model rejected: %v", err)
	}
	if err := AssertClassifierModel(classifier); err != nil {
		t.Fatalf("classifier model rejected: %v", err)
	}
	for _, tc := range []struct {
		name   string
		err    error
		expect string
	}{
		{"image is not a classifier", AssertClassifierModel(image), "Model p/m is not a classifier model"},
		{"chat is not an image", AssertImageModel(chat), "Model p/m is not an image model"},
		{"classifier is not an image", AssertImageModel(classifier), "Model p/m is not an image model"},
	} {
		var modelsErr *ModelsError
		if !errors.As(tc.err, &modelsErr) || modelsErr.Code != ModelsErrorProvider || modelsErr.Message != tc.expect {
			t.Errorf("%s: error = %#v, want provider ModelsError %q", tc.name, tc.err, tc.expect)
		}
	}
	// A model without a type is a chat model.
	if err := AssertChatModel(&Model{ID: "legacy"}); err != nil {
		t.Errorf("a model without a type is a chat model: %v", err)
	}
	if err := AssertChatModel(&Model{ID: "x", Type: ModelTypeImage}); err == nil {
		t.Errorf("a model typed image must not pass assertChatModel")
	}
}

// packages/ai/src/utils/model-operations.ts imageErrorResult and classifierErrorResult.
func TestModelOperationErrorResultsCarryTheModelAndStopReasonLikeUpstream(t *testing.T) {
	boom := errors.New("boom")
	image := typedImageModel("p", "img")
	for _, tc := range []struct {
		aborted bool
		want    ImagesStopReason
	}{{false, ImagesStopReasonError}, {true, ImagesStopReasonAborted}} {
		got := ImageErrorResult(image, boom, tc.aborted)
		if got.API != image.API || got.Provider != "p" || got.Model != "img" || got.StopReason != tc.want || got.ErrorMessage != "boom" || got.Output == nil || len(got.Output) != 0 || got.Timestamp == 0 {
			t.Errorf("aborted=%v: image result = %+v", tc.aborted, got)
		}
	}
	classifier := classifierTestModel("p", "cls")
	for _, tc := range []struct {
		aborted bool
		want    ClassifierStopReason
	}{{false, ClassifierStopReasonError}, {true, ClassifierStopReasonAborted}} {
		got := ClassifierErrorResult(classifier, boom, tc.aborted)
		if got.API != classifier.API || got.Provider != "p" || got.Model != "cls" || got.StopReason != tc.want || got.ErrorMessage != "boom" || len(got.Answers) != 0 || got.Timestamp == 0 {
			t.Errorf("aborted=%v: classifier result = %+v", tc.aborted, got)
		}
	}
}

// packages/ai/src/utils/estimate.ts estimateTextTokens: Math.ceil(text.length / 3.5) in UTF-16 code units.
func TestEstimateTextTokensCountsThreeAndAHalfUTF16UnitsPerTokenLikeUpstream(t *testing.T) {
	for _, tc := range []struct {
		text string
		want int
	}{
		{"", 0},
		{"a", 1},
		{"abcd", 2},
		{"abcde", 2},
		{strings.Repeat("x", 4000), 1143},
		{"\U0001F600\U0001F600", 2},  // two astral characters are four UTF-16 units
		{"\U0001F600\U0001F600a", 2}, // five units
		{"é", 1},
	} {
		if got := EstimateTextTokens(tc.text); got != tc.want {
			t.Errorf("EstimateTextTokens(%q) = %d, want %d", tc.text, got, tc.want)
		}
	}
}

// packages/ai/src/utils/error-body.ts truncateErrorText.
func TestTruncateErrorTextReportsOmittedUTF16UnitsLikeUpstream(t *testing.T) {
	if got := TruncateErrorText("short", 5); got != "short" {
		t.Errorf("at the cap = %q", got)
	}
	if got := TruncateErrorText("abcdefgh", 3); got != "abc... [truncated 5 chars]" {
		t.Errorf("over the cap = %q", got)
	}
	// An astral character is two UTF-16 units: the omitted count is in units, not code points.
	if got := TruncateErrorText("ab\U0001F600\U0001F600", 2); got != "ab... [truncated 4 chars]" {
		t.Errorf("astral omitted count = %q", got)
	}
	if got := TruncateErrorText(strings.Repeat("x", MaxProviderErrorBodyChars+10), MaxProviderErrorBodyChars); !strings.HasSuffix(got, "... [truncated 10 chars]") {
		t.Errorf("default cap suffix = %q", got[len(got)-30:])
	}
}

// packages/ai/src/utils/error-body.ts safeJsonStringify.
func TestSafeJsonStringifyLeavesHTMLAndFallsBackLikeUpstream(t *testing.T) {
	if got := SafeJsonStringify(map[string]any{"a": "<b>&"}); got != `{"a":"<b>&"}` {
		t.Errorf("html characters must stay literal: %q", got)
	}
	if got := SafeJsonStringify("text"); got != `"text"` {
		t.Errorf("string = %q", got)
	}
	if got := SafeJsonStringify([]int{1, 2}); got != "[1,2]" {
		t.Errorf("array = %q", got)
	}
	// JSON.stringify throws on an unserializable value; the helper must not.
	if got := SafeJsonStringify(make(chan int)); got == "" {
		t.Errorf("an unserializable value must still render")
	}
}

// packages/ai/src/utils/transcript.ts createInitialSystemMessage.
func TestCreateInitialSystemMessageIsEmptyOnlyWithoutPromptAndToolsLikeUpstream(t *testing.T) {
	tool := ToolSchema{Name: "read", Description: "read a file", Parameters: map[string]any{"type": "object"}}
	if got := CreateInitialSystemMessage("", nil); got != nil {
		t.Errorf("empty prompt and tools = %+v, want nil", got)
	}
	if got := CreateInitialSystemMessage("", []ToolSchema{}); got != nil {
		t.Errorf("empty tool slice = %+v, want nil", got)
	}
	prompt := CreateInitialSystemMessage("be brief", nil)
	if prompt == nil || prompt.Content != SystemText("be brief") || len(prompt.ToolsAdded) != 0 || prompt.Timestamp != 0 {
		t.Errorf("prompt only = %+v", prompt)
	}
	tools := CreateInitialSystemMessage("", []ToolSchema{tool})
	if tools == nil || tools.Content != SystemText("") || len(tools.ToolsAdded) != 1 || tools.ToolsAdded[0].Name != "read" {
		t.Errorf("tools only = %+v", tools)
	}
	// The message owns its tool list: later mutation of the caller's slice must not reach it.
	input := []ToolSchema{tool}
	owned := CreateInitialSystemMessage("p", input)
	input[0].Name = "mutated"
	if owned.ToolsAdded[0].Name != "read" {
		t.Errorf("the tool list aliases the caller's slice")
	}
}

// packages/ai/src/utils/transcript.ts getInitialSystemMessage and withoutInitialSystemMessage.
func TestInitialSystemMessageHelpersOnlyLookAtTheFirstMessageLikeUpstream(t *testing.T) {
	system := SystemMessage{Content: SystemText("lead")}
	later := SystemMessage{Content: SystemText("later")}
	user := UserMessage{Content: UserText("hi")}
	if got := GetInitialSystemMessage[Message](nil); got != nil {
		t.Errorf("empty transcript = %+v", got)
	}
	if got := GetInitialSystemMessage([]Message{user, system}); got != nil {
		t.Errorf("a later system message is not the initial one: %+v", got)
	}
	if got := GetInitialSystemMessage([]Message{system, user}); got == nil || got.Content != SystemText("lead") {
		t.Errorf("leading system message = %+v", got)
	}
	messages := []Message{system, user, later}
	if got := WithoutInitialSystemMessage(messages); !reflect.DeepEqual(got, []Message{user, later}) {
		t.Errorf("without initial = %+v", got)
	}
	if got := WithoutInitialSystemMessage([]Message{user, later}); !reflect.DeepEqual(got, []Message{user, later}) {
		t.Errorf("no leading system message keeps every message: %+v", got)
	}
	if got := WithoutInitialSystemMessage(nil); len(got) != 0 {
		t.Errorf("empty transcript = %+v", got)
	}
}

// packages/ai/src/utils/transcript.ts getDeclaredTools and resolveTranscriptTools.
func TestDeclaredToolsAndTranscriptToolsFollowTheToolHistoryLikeUpstream(t *testing.T) {
	read := ToolSchema{Name: "read", Description: "v1", Parameters: map[string]any{"type": "object"}}
	write := ToolSchema{Name: "write", Description: "v1", Parameters: map[string]any{"type": "object"}}
	readV2 := ToolSchema{Name: "read", Description: "v2", Parameters: map[string]any{"type": "object"}}
	user := UserMessage{Content: UserText("hi")}

	additive := []Message{
		SystemMessage{Content: SystemText("lead"), ToolsAdded: []ToolSchema{read}},
		user,
		SystemMessage{Content: SystemText("more"), ToolsAdded: []ToolSchema{write}},
	}
	if got := GetDeclaredTools(additive); len(got) != 2 || got[0].Name != "read" || got[1].Name != "write" {
		t.Errorf("declared tools = %+v", got)
	}
	// Anchoring transports keep the initial tools in the request and load later ones in place.
	anchored := ResolveTranscriptTools(additive, true)
	if !anchored.AnchorsAdditions || len(anchored.RequestTools) != 1 || anchored.RequestTools[0].Name != "read" {
		t.Errorf("anchored = %+v", anchored)
	}
	// Without addition support the request holds the complete current tool set.
	flat := ResolveTranscriptTools(additive, false)
	if flat.AnchorsAdditions || len(flat.RequestTools) != 2 {
		t.Errorf("not anchored = %+v", flat)
	}
	// An anchoring transport without a leading system message sends no top-level tools.
	noLead := ResolveTranscriptTools([]Message{user, SystemMessage{ToolsAdded: []ToolSchema{write}}}, true)
	if !noLead.AnchorsAdditions || len(noLead.RequestTools) != 0 {
		t.Errorf("no leading system message = %+v", noLead)
	}

	// A redeclaration is declared once, with its later definition, in first-declaration order.
	redeclared := []Message{
		SystemMessage{ToolsAdded: []ToolSchema{read, write}},
		SystemMessage{ToolsAdded: []ToolSchema{readV2}},
	}
	got := GetDeclaredTools(redeclared)
	if len(got) != 2 || got[0].Name != "read" || got[0].Description != "v2" || got[1].Name != "write" {
		t.Errorf("redeclared = %+v", got)
	}
	// A redeclaration or a removal makes the history non-additive: anchoring is refused even when supported.
	nonAdditive := ResolveTranscriptTools(redeclared, true)
	if nonAdditive.AnchorsAdditions || len(nonAdditive.RequestTools) != 2 {
		t.Errorf("redeclared anchored = %+v", nonAdditive)
	}
	removed := []Message{
		SystemMessage{ToolsAdded: []ToolSchema{read, write}},
		SystemMessage{ToolsRemoved: []ToolReference{{Name: "read"}}},
	}
	afterRemoval := ResolveTranscriptTools(removed, true)
	if afterRemoval.AnchorsAdditions || len(afterRemoval.RequestTools) != 1 || afterRemoval.RequestTools[0].Name != "write" {
		t.Errorf("after removal = %+v", afterRemoval)
	}
	if got := GetDeclaredTools(removed); len(got) != 2 {
		t.Errorf("a removal does not undeclare a tool: %+v", got)
	}
}

// packages/ai/src/api/azure-openai-config.ts resolveAzureConfig.
func TestResolveAzureConfigPairsTheBaseURLWithTheAPIVersionLikeUpstream(t *testing.T) {
	got, err := ResolveAzureConfig("https://res.openai.azure.com", StreamOptions{Env: ProviderEnv{}})
	if err != nil || got != (AzureConfig{BaseURL: "https://res.openai.azure.com/openai/v1", APIVersion: "v1"}) {
		t.Errorf("default = %+v, %v", got, err)
	}
	got, err = ResolveAzureConfig("", StreamOptions{Env: ProviderEnv{"AZURE_OPENAI_API_VERSION": "env-version"}, AzureAPIVersion: "2025-01-01", AzureResourceName: "acme"})
	if err != nil || got != (AzureConfig{BaseURL: "https://acme.openai.azure.com/openai/v1", APIVersion: "2025-01-01"}) {
		t.Errorf("option version wins = %+v, %v", got, err)
	}
	got, err = ResolveAzureConfig("", StreamOptions{Env: ProviderEnv{"AZURE_OPENAI_API_VERSION": "env-version"}, AzureBaseURL: "https://example.test/custom"})
	if err != nil || got != (AzureConfig{BaseURL: "https://example.test/custom", APIVersion: "env-version"}) {
		t.Errorf("env version = %+v, %v", got, err)
	}
	if _, err := ResolveAzureConfig("", StreamOptions{Env: ProviderEnv{}}); err == nil || !strings.Contains(err.Error(), "Azure OpenAI base URL is required") {
		t.Errorf("missing endpoint error = %v", err)
	}
}

// packages/ai/src/api/azure-openai-config.ts resolveDeploymentName.
func TestResolveAzureDeploymentNameFollowsTheOptionThenTheMapLikeUpstream(t *testing.T) {
	env := ProviderEnv{"AZURE_OPENAI_DEPLOYMENT_NAME_MAP": " gpt-4o = prod-4o , other=x=y, bad, =none, empty= ,gpt-4o=later"}
	if got := ResolveAzureDeploymentName("gpt-4o", StreamOptions{Env: env, AzureDeploymentName: "explicit"}); got != "explicit" {
		t.Errorf("explicit option = %q", got)
	}
	if got := ResolveAzureDeploymentName("gpt-4o", StreamOptions{Env: env}); got != "later" {
		t.Errorf("later entry replaces an earlier one = %q", got)
	}
	if got := ResolveAzureDeploymentName("other", StreamOptions{Env: env}); got != "x" {
		t.Errorf("split(\"=\", 2) drops the text after a second equals sign = %q", got)
	}
	for _, id := range []string{"empty", "bad", "unmapped"} {
		if got := ResolveAzureDeploymentName(id, StreamOptions{Env: env}); got != id {
			t.Errorf("%s falls back to the model id, got %q", id, got)
		}
	}
}

// packages/ai/src/api/simple-options.ts resolveSamplingParams.
func TestResolveSamplingParamsMergesDefaultsLevelAndRequestInOrderLikeUpstream(t *testing.T) {
	model := &Model{ID: "m", SamplingParams: SamplingParams{"temperature": 0.5, "top_p": 0.9}, SamplingParamsByThinkingLevel: SamplingParamsByThinkingLevel{"off": {"temperature": 0.2, "top_k": 4}}}
	got := ResolveSamplingParams(model, "off", SamplingParams{"top_k": 8})
	want := SamplingParams{"temperature": 0.2, "top_p": 0.9, "top_k": 8}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("merged = %+v, want %+v", got, want)
	}
	got = ResolveSamplingParams(model, "off", nil)
	if !reflect.DeepEqual(got, SamplingParams{"temperature": 0.2, "top_p": 0.9, "top_k": 4}) {
		t.Errorf("without request params = %+v", got)
	}
	if got := ResolveSamplingParams(&Model{ID: "bare"}, "off", nil); got != nil {
		t.Errorf("no source sets anything: %+v", got)
	}
	if got := ResolveSamplingParams(&Model{ID: "bare"}, "off", SamplingParams{"seed": 1}); !reflect.DeepEqual(got, SamplingParams{"seed": 1}) {
		t.Errorf("request only = %+v", got)
	}
	// The merge is a copy: mutating the result must not change the model defaults.
	merged := ResolveSamplingParams(model, "off", nil)
	merged["temperature"] = 9.0
	if model.SamplingParams["temperature"] != 0.5 {
		t.Errorf("the merged parameters alias the model defaults")
	}
}

// Constants whose upstream value is a literal in the pinned 1.0.4 source.
func TestUpstreamLiteralConstants(t *testing.T) {
	for _, tc := range []struct {
		name      string
		got, want any
	}{
		{"MAX_PROVIDER_ERROR_BODY_CHARS (utils/error-body.ts)", MaxProviderErrorBodyChars, 4000},
		{"DEFAULT_MAX_AGENT_RETRY_DELAY_MS (utils/retry.ts)", DefaultMaxAgentRetryDelayMs, 60000},
		{"OPENAI_PROMPT_CACHE_KEY_MAX_LENGTH (api/openai-prompt-cache.ts)", openAIPromptCacheKeyMaxLength, 64},
		{"DEFAULT_RADIUS_GATEWAY (providers/radius-config.ts)", DefaultRadiusGateway, "https://radius.pi.dev"},
		{"UNSUPPORTED_PROXY_PROTOCOL_MESSAGE (utils/node-http-proxy.ts)", UnsupportedProxyProtocolMessage, "Unsupported proxy protocol. SOCKS and PAC proxy URLs are not supported; use an HTTP or HTTPS proxy URL."},
		{"ANTHROPIC_AUTH_TOKEN_ENV (env-api-keys.ts)", AnthropicAuthTokenEnv, "ANTHROPIC_AUTH_TOKEN"},
		{"ANTHROPIC_OAUTH_TOKEN_ENV (env-api-keys.ts)", AnthropicOAuthTokenEnv, "ANTHROPIC_OAUTH_TOKEN"},
		{"ANTHROPIC_API_KEY_ENV (env-api-keys.ts)", AnthropicAPIKeyEnv, "ANTHROPIC_API_KEY"},
		{"CLOUDFLARE_WORKERS_AI_BASE_URL (api/cloudflare.ts)", CloudflareWorkersAIBaseURL, "https://api.cloudflare.com/client/v4/accounts/{CLOUDFLARE_ACCOUNT_ID}/ai/v1"},
		{"CLOUDFLARE_AI_GATEWAY_COMPAT_BASE_URL (api/cloudflare.ts)", CloudflareAIGatewayCompatBaseURL, "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/compat"},
		{"CLOUDFLARE_AI_GATEWAY_OPENAI_BASE_URL (api/cloudflare.ts)", CloudflareAIGatewayOpenAIBaseURL, "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/openai"},
		{"CLOUDFLARE_AI_GATEWAY_ANTHROPIC_BASE_URL (api/cloudflare.ts)", CloudflareAIGatewayAnthropicBaseURL, "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/anthropic"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}

// packages/ai/src/auth/credential-store.ts InMemoryCredentialStore.delete and its read/list/modify neighbours.
func TestInMemoryCredentialStoreDeleteRemovesOnlyTheNamedEntryLikeUpstream(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryCredentialStore()
	for _, id := range []string{"a", "b", "c"} {
		if _, err := store.Modify(ctx, id, func(*Credential) (*Credential, error) {
			return &Credential{Type: CredentialAPIKey, Key: "key-" + id}, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Deleting an absent provider is not an error.
	if err := store.Delete(ctx, "missing"); err != nil {
		t.Fatalf("delete of an absent entry: %v", err)
	}
	if err := store.Delete(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Read(ctx, "b"); err != nil || got != nil {
		t.Errorf("read after delete = %+v, %v; want nil", got, err)
	}
	list, err := store.List(ctx)
	if err != nil || len(list) != 2 || list[0].ProviderID != "a" || list[1].ProviderID != "c" || list[0].Type != CredentialAPIKey {
		t.Errorf("list after delete = %+v, %v", list, err)
	}
	// A deleted provider re-enters at the end of the key order, like a Map entry that was removed and set again.
	if _, err := store.Modify(ctx, "b", func(*Credential) (*Credential, error) { return &Credential{Type: CredentialOAuth}, nil }); err != nil {
		t.Fatal(err)
	}
	list, _ = store.List(ctx)
	if len(list) != 3 || list[2].ProviderID != "b" || list[2].Type != CredentialOAuth {
		t.Errorf("list after re-adding = %+v", list)
	}
	// An already cancelled operation fails before it touches the store.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := store.Delete(cancelled, "a"); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled delete = %v", err)
	}
	if got, _ := store.Read(ctx, "a"); got == nil {
		t.Errorf("a cancelled delete removed the entry")
	}
	// A nil result from modify leaves the entry unchanged and returns it.
	kept, err := store.Modify(ctx, "a", func(current *Credential) (*Credential, error) { return nil, nil })
	if err != nil || kept == nil || kept.Key != "key-a" {
		t.Errorf("modify returning nothing = %+v, %v", kept, err)
	}
}

// packages/ai/src/models.ts Models.streamDeferred: the returned stream carries the polled deferred state, and a provider
// without deferred support fails the stream with a provider ModelsError.
// Pi source: packages/ai/src/models.ts:218 (fetchDeferred) and packages/ai/src/providers/faux.ts:569 (fetchDeferred).
// mutation-checked: the mutant "the faux provider ignores StreamOptions.Deferred" (ai/faux.go, the deferred branch of the stream) fails it.
func TestModelsStreamDeferredStreamsThePolledStateLikeUpstream(t *testing.T) {
	faux := NewFauxProvider(FauxConfig{Deferred: &FauxDeferredConfig{PendingFetches: 1, PollAfterMS: new(int64(25))}})
	models := CreateModels(CreateModelsOptions{})
	defer models.Close()
	models.SetProvider(faux.Provider())
	faux.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{Content: []FauxContentBlock{FauxText("ready")}, StopReason: "stop"})})
	model := faux.GetModel()
	handle := models.StreamSimple(t.Context(), model, Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}, StreamOptions{Deferred: &DeferredOption{Object: true, Window: "1h"}}).Result().Deferred
	if handle == nil {
		t.Fatal("no deferred handle")
	}
	pendingStream := models.StreamDeferred(t.Context(), model, *handle)
	var types []AssistantEventType
	for event := range pendingStream.Events(t.Context()) {
		types = append(types, event.EventType())
	}
	if pending := pendingStream.Result(); pending.StopReason != StopReasonDeferred || len(types) == 0 || types[len(types)-1] != EventDone {
		t.Errorf("pending stream = %+v events=%v", pending, types)
	}
	ready := models.StreamDeferred(t.Context(), model, *handle, DeferredFetchOptions{Wait: new(0.0)}).Result()
	if ready.StopReason != StopReasonStop || len(ready.Content) != 1 {
		t.Errorf("ready = %+v", ready)
	}
	unsupported := models.StreamDeferred(t.Context(), typedChatModel("missing", "m"), DeferredHandle{ID: "h"}).Result()
	if unsupported.StopReason != StopReasonError || unsupported.ErrorMessage == "" {
		t.Errorf("unknown provider = %+v", unsupported)
	}
}
