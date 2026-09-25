package ai

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	bdoc "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	btypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
)

func TestBedrockResolveRegion(t *testing.T) {
	cases := []struct {
		name  string
		env   map[string]string
		base  string
		model string
		want  string
	}{
		{
			name: "explicit AWS_REGION wins",
			env:  map[string]string{"AWS_REGION": "us-west-2"},
			want: "us-west-2",
		},
		{
			name:  "inference-profile ARN region beats AWS_REGION",
			env:   map[string]string{"AWS_REGION": "us-west-2"},
			model: "arn:aws:bedrock:eu-central-1:123456789012:inference-profile/eu.anthropic.claude-fable-5",
			want:  "eu-central-1",
		},
		{
			name:  "GovCloud ARN partition region parsed",
			model: "arn:aws-us-gov:bedrock:us-gov-west-1:123456789012:inference-profile/x",
			want:  "us-gov-west-1",
		},
		{
			name: "AWS_DEFAULT_REGION falls back when AWS_REGION unset",
			env:  map[string]string{"AWS_DEFAULT_REGION": "eu-west-1"},
			want: "eu-west-1",
		},
		{
			name: "endpoint hostname region parsed",
			base: "https://bedrock-runtime.ap-northeast-1.amazonaws.com",
			want: "ap-northeast-1",
		},
		{
			name: "fips endpoint hostname region parsed",
			base: "https://bedrock-runtime-fips.us-east-2.amazonaws.com",
			want: "us-east-2",
		},
		{
			name: "default to us-east-1 when no profile and no other signal",
			want: "us-east-1",
		},
		{
			name: "AWS_PROFILE forces SDK profile resolution (empty here)",
			env:  map[string]string{"AWS_PROFILE": "dev"},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range []string{"AWS_REGION", "AWS_DEFAULT_REGION", "AWS_PROFILE"} {
				t.Setenv(k, "")
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			got := resolveBedrockRegion(tc.base, tc.model)
			if got != tc.want {
				t.Errorf("resolveBedrockRegion(%q, %q) = %q, want %q", tc.base, tc.model, got, tc.want)
			}
		})
	}
}

func TestBedrockResolveCacheRetention(t *testing.T) {
	cases := []struct {
		env  string
		want bedrockCacheRetention
	}{
		{"", bedrockCacheShort},
		{"short", bedrockCacheShort},
		{"long", bedrockCacheLong},
		{"none", bedrockCacheNone},
	}
	for _, tc := range cases {
		t.Run(tc.env, func(t *testing.T) {
			t.Setenv("PI_CACHE_RETENTION", tc.env)
			if got := resolveBedrockCacheRetention(); got != tc.want {
				t.Errorf("PI_CACHE_RETENTION=%q -> %q, want %q", tc.env, got, tc.want)
			}
		})
	}
}

func TestBedrockSupportsPromptCaching(t *testing.T) {
	cases := map[string]bool{
		"anthropic.claude-3-5-haiku-20241022-v1:0":  true,
		"anthropic.claude-3-7-sonnet-20250219-v1:0": true,
		"anthropic.claude-sonnet-4-20250514-v1:0":   true,
		"anthropic.claude-sonnet-5-20260101-v1:0":   true,
		"anthropic.claude-3-haiku-20240307-v1:0":    false,
		"amazon.nova-lite-v1:0":                     false,
		"arn:aws:bedrock:::application-profile/foo": false,
	}
	t.Setenv("AWS_BEDROCK_FORCE_CACHE", "")
	for id, want := range cases {
		if got := supportsBedrockPromptCaching(id); got != want {
			t.Errorf("supportsBedrockPromptCaching(%q) = %v, want %v", id, got, want)
		}
	}

	if !supportsBedrockPromptCachingWithName("arn:aws:bedrock:::application-profile/foo", "Claude Sonnet 4.6") {
		t.Error("model name should enable prompt caching for inference profiles when ARN omits Claude reference")
	}

	// FORCE_CACHE flips non-Claude models on.
	t.Setenv("AWS_BEDROCK_FORCE_CACHE", "1")
	if !supportsBedrockPromptCaching("arn:aws:bedrock:::application-profile/foo") {
		t.Error("AWS_BEDROCK_FORCE_CACHE=1 should force caching on non-Claude ids")
	}
}

func TestBedrockNormalizeToolID(t *testing.T) {
	cases := map[string]string{
		"call_abc-123":          "call_abc-123",
		"tool.with.dots":        "tool_with_dots",
		"toolu_01abcdef":        "toolu_01abcdef",
		strings.Repeat("x", 80): strings.Repeat("x", 64),
		"tool with spaces!":     "tool_with_spaces_",
	}
	for in, want := range cases {
		if got := normalizeBedrockToolID(in); got != want {
			t.Errorf("normalizeBedrockToolID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBedrockBuildSystemPrompt(t *testing.T) {
	t.Setenv("AWS_BEDROCK_FORCE_CACHE", "")
	// Claude 3.7 Sonnet supports caching → expect text + cache point.
	got := buildBedrockSystemPrompt("anthropic.claude-3-7-sonnet-20250219-v1:0", "", "hello", bedrockCacheShort)
	if len(got) != 2 {
		t.Fatalf("Claude+short: want 2 blocks, got %d", len(got))
	}
	if _, ok := got[0].(*btypes.SystemContentBlockMemberText); !ok {
		t.Errorf("first block not text: %T", got[0])
	}
	if _, ok := got[1].(*btypes.SystemContentBlockMemberCachePoint); !ok {
		t.Errorf("second block not cache point: %T", got[1])
	}

	// Non-Claude with no caching → text only.
	got = buildBedrockSystemPrompt("amazon.nova-lite-v1:0", "", "hi", bedrockCacheShort)
	if len(got) != 1 {
		t.Fatalf("Nova: want 1 block, got %d", len(got))
	}

	// Inference profile with Claude name uses name-based detection.
	got = buildBedrockSystemPrompt("arn:aws:bedrock:::application-profile/foo", "Claude Sonnet 4.6", "hi", bedrockCacheShort)
	if len(got) != 2 {
		t.Fatalf("inference profile + model name: want 2 blocks, got %d", len(got))
	}

	// Cache disabled by env → no cache point even on Claude.
	got = buildBedrockSystemPrompt("anthropic.claude-3-7-sonnet-20250219-v1:0", "", "hi", bedrockCacheNone)
	if len(got) != 1 {
		t.Fatalf("none retention: want 1 block, got %d", len(got))
	}

	// Empty system prompt → nil.
	if got := buildBedrockSystemPrompt("anything", "", "", bedrockCacheShort); got != nil {
		t.Errorf("empty prompt: want nil, got %+v", got)
	}
}

func TestBedrockConvertMessages_UserText(t *testing.T) {
	msgs := []Message{UserMessage{Content: UserText("hello world")}}
	out, err := convertBedrockMessages(msgs, "anthropic.claude-3-7-sonnet-20250219-v1:0", "", bedrockCacheNone)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1", len(out))
	}
	if out[0].Role != btypes.ConversationRoleUser {
		t.Errorf("role = %q, want user", out[0].Role)
	}
	tb, ok := out[0].Content[0].(*btypes.ContentBlockMemberText)
	if !ok {
		t.Fatalf("content[0] = %T, want text", out[0].Content[0])
	}
	if tb.Value != "hello world" {
		t.Errorf("text = %q, want hello world", tb.Value)
	}
}

func TestBedrockConvertMessages_UserBlocksSkipBlankContent(t *testing.T) {
	msgs := []Message{UserMessage{Content: UserContentBlocks{
		TextContent{Text: "hello"},
		TextContent{Text: "   "},
	}}}
	out, err := convertBedrockMessages(msgs, "anthropic.claude-3-7-sonnet-20250219-v1:0", "", bedrockCacheNone)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1", len(out))
	}
	if len(out[0].Content) != 1 {
		t.Fatalf("content len = %d, want 1", len(out[0].Content))
	}
	if text, ok := out[0].Content[0].(*btypes.ContentBlockMemberText); !ok || text.Value != "hello" {
		t.Fatalf("content[0] = %#v, want text hello", out[0].Content[0])
	}
}

func TestBedrockConvertMessages_PlaceholderForEmptyUser(t *testing.T) {
	// A user message emptied by filtering (only unknown/blank blocks) is
	// replaced with an "<empty>" placeholder, not dropped: Bedrock rejects
	// empty content. Mirrors upstream convertMessages.
	msgs := []Message{UserMessage{Content: UserContentBlocks{
		TextContent{Text: "   "},
	}}}
	out, err := convertBedrockMessages(msgs, "anthropic.claude-3-7-sonnet-20250219-v1:0", "", bedrockCacheNone)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1 (placeholder, not skipped)", len(out))
	}
	tb, ok := out[0].Content[0].(*btypes.ContentBlockMemberText)
	if !ok || tb.Value != bedrockEmptyPlaceholder {
		t.Fatalf("content[0] = %#v, want text %q", out[0].Content[0], bedrockEmptyPlaceholder)
	}
}

func TestBedrockConvertMessages_PlaceholderForBlankUserString(t *testing.T) {
	msgs := []Message{UserMessage{Content: UserText("   ")}}
	out, err := convertBedrockMessages(msgs, "anthropic.claude-3-7-sonnet-20250219-v1:0", "", bedrockCacheNone)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1", len(out))
	}
	tb, ok := out[0].Content[0].(*btypes.ContentBlockMemberText)
	if !ok || tb.Value != bedrockEmptyPlaceholder {
		t.Fatalf("content[0] = %#v, want text %q", out[0].Content[0], bedrockEmptyPlaceholder)
	}
}

func TestBedrockConvertMessages_PlaceholderForBlankToolResult(t *testing.T) {
	// A tool result emptied to blank must get a placeholder so the preceding
	// tool_use stays paired; an empty tool-result content is rejected.
	// Upstream convertToolResultContent uses createNonBlankTextBlock, which
	// sanitizes and TRIMS: whitespace-only content is dropped and replaced by
	// the placeholder, not sent verbatim. pig previously kept `"   "` as text.
	for _, content := range []string{"", "   ", "\n\t "} {
		msgs := []Message{ToolResultMessage{
			ToolCallID: "call_1",
			Content:    []ToolResultMessageContent{TextContent{Text: content}},
		}}
		out, err := convertBedrockMessages(msgs, "anthropic.claude-3-7-sonnet-20250219-v1:0", "", bedrockCacheNone)
		if err != nil {
			t.Fatalf("convert %q: %v", content, err)
		}
		if len(out) != 1 {
			t.Fatalf("content %q: len = %d, want 1", content, len(out))
		}
		tr, ok := out[0].Content[0].(*btypes.ContentBlockMemberToolResult)
		if !ok {
			t.Fatalf("content %q: content[0] = %T, want tool result", content, out[0].Content[0])
		}
		if len(tr.Value.Content) != 1 {
			t.Fatalf("content %q: tool result content len = %d, want 1 (placeholder)", content, len(tr.Value.Content))
		}
		ptb, ok := tr.Value.Content[0].(*btypes.ToolResultContentBlockMemberText)
		if !ok || ptb.Value != bedrockEmptyPlaceholder {
			t.Fatalf("content %q: tool result content[0] = %#v, want text %q", content, tr.Value.Content[0], bedrockEmptyPlaceholder)
		}
	}
}

func TestBedrockConvertMessages_SkipsEmptyAssistant(t *testing.T) {
	msgs := []Message{
		UserMessage{Content: UserText("go")},
		AssistantMessage{Content: []AssistantContentBlock{
			TextContent{Text: "   "},
		}},
		UserMessage{Content: UserText("again")},
	}
	out, err := convertBedrockMessages(msgs, "amazon.nova-lite-v1:0", "", bedrockCacheNone)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2 (empty assistant must be dropped)", len(out))
	}
	if out[0].Role != btypes.ConversationRoleUser || out[1].Role != btypes.ConversationRoleUser {
		t.Errorf("roles = %v %v, want user user", out[0].Role, out[1].Role)
	}
}

func TestBedrockConvertMessages_FinalCachePointOnLastUser(t *testing.T) {
	msgs := []Message{UserMessage{Content: UserText("hi")}}
	out, err := convertBedrockMessages(msgs, "anthropic.claude-3-7-sonnet-20250219-v1:0", "", bedrockCacheShort)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	last := out[len(out)-1]
	hasCache := false
	for _, b := range last.Content {
		if _, ok := b.(*btypes.ContentBlockMemberCachePoint); ok {
			hasCache = true
		}
	}
	if !hasCache {
		t.Errorf("expected cache point on last user message for Claude 3.7 Sonnet, got %+v", last.Content)
	}
}

func TestBedrockConvertTools(t *testing.T) {
	tools := []ToolSchema{
		{Name: "bash", Description: "run a command", Parameters: map[string]any{"type": "object"}},
	}
	cfg, err := convertBedrockTools(tools, false)
	if err != nil {
		t.Fatalf("convertBedrockTools: %v", err)
	}
	if cfg == nil || len(cfg.Tools) != 1 {
		t.Fatalf("convertBedrockTools = %+v", cfg)
	}
	spec, ok := cfg.Tools[0].(*btypes.ToolMemberToolSpec)
	if !ok {
		t.Fatalf("tool[0] = %T, want ToolMemberToolSpec", cfg.Tools[0])
	}
	if spec.Value.Name == nil || *spec.Value.Name != "bash" {
		t.Errorf("spec.Name = %v, want bash", spec.Value.Name)
	}
	if _, ok := spec.Value.InputSchema.(*btypes.ToolInputSchemaMemberJson); !ok {
		t.Errorf("InputSchema = %T, want JSON member", spec.Value.InputSchema)
	}
}

func TestBedrockConvertStrictTools(t *testing.T) {
	tools := []ToolSchema{{
		Name: "lookup", Description: "lookup",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"value": map[string]any{"type": "string"},
		}},
		ConstrainedSampling: &ConstrainedSamplingConfig{Type: "json_schema", Strict: "prefer"},
	}}
	cfg, err := convertBedrockTools(tools, true)
	if err != nil {
		t.Fatalf("convertBedrockTools: %v", err)
	}
	spec := cfg.Tools[0].(*btypes.ToolMemberToolSpec).Value
	if spec.Strict == nil || !*spec.Strict {
		t.Fatalf("strict = %#v, want true", spec.Strict)
	}
	member := spec.InputSchema.(*btypes.ToolInputSchemaMemberJson)
	body, err := member.Value.MarshalSmithyDocument()
	if err != nil {
		t.Fatalf("marshal input schema: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(body, &schema); err != nil {
		t.Fatalf("decode input schema: %v", err)
	}
	if schema["additionalProperties"] != false {
		t.Fatalf("input schema = %s, want strict conversion", body)
	}
}

func TestBedrockConvertTools_Empty(t *testing.T) {
	if cfg, err := convertBedrockTools(nil, false); err != nil || cfg != nil {
		t.Errorf("nil tools must yield nil ToolConfiguration, got %+v, %v", cfg, err)
	}
	if cfg, err := convertBedrockTools([]ToolSchema{}, false); err != nil || cfg != nil {
		t.Errorf("empty tools must yield nil ToolConfiguration, got %+v, %v", cfg, err)
	}
}

func TestBedrockBuildAdditionalFields(t *testing.T) {
	// Non-Claude → no thinking config.
	if got := buildBedrockAdditionalFields(&Model{ID: "amazon.nova-lite-v1:0"}, "", StreamOptions{Thinking: ThinkingMedium, IsReasoning: true}); got != nil {
		t.Errorf("Nova must not get thinking config, got %+v", got)
	}

	// Claude 3.7 Sonnet, medium thinking → enabled with budget_tokens.
	got := buildBedrockAdditionalFields(&Model{ID: "anthropic.claude-3-7-sonnet-20250219-v1:0"}, "", StreamOptions{Thinking: ThinkingMedium, IsReasoning: true})
	if got == nil {
		t.Fatalf("Claude 3.7 + medium must return thinking config")
	}
	th, ok := got["thinking"].(map[string]any)
	if !ok || th["type"] != "enabled" {
		t.Errorf("thinking = %+v, want enabled", got["thinking"])
	}
	if budget, _ := th["budget_tokens"].(int); budget != 8192 {
		t.Errorf("medium budget = %v, want 8192", th["budget_tokens"])
	}

	// Adaptive thinking model (Claude Opus 4.7) → adaptive type + output_config.
	got = buildBedrockAdditionalFields(&Model{ID: "anthropic.claude-opus-4-7-v1:0", ThinkingLevelMap: ThinkingLevelMap{ThinkingXHigh: new("xhigh")}}, "", StreamOptions{Thinking: ThinkingHigh, IsReasoning: true})
	if got == nil {
		t.Fatalf("adaptive model must return thinking config")
	}
	th, _ = got["thinking"].(map[string]any)
	if th["type"] != "adaptive" {
		t.Errorf("adaptive thinking type = %v, want adaptive", th["type"])
	}
	if _, ok := got["output_config"]; !ok {
		t.Error("adaptive thinking must include output_config")
	}

	// Inference profile should use model name for adaptive detection.
	got = buildBedrockAdditionalFields(&Model{ID: "arn:aws:bedrock:::application-profile/foo", ThinkingLevelMap: ThinkingLevelMap{ThinkingXHigh: new("xhigh")}}, "Claude Opus 4.7", StreamOptions{Thinking: ThinkingXHigh, IsReasoning: true})
	if got == nil {
		t.Fatalf("adaptive inference profile must return thinking config")
	}
	th, _ = got["thinking"].(map[string]any)
	if th["type"] != "adaptive" {
		t.Errorf("adaptive inference profile thinking type = %v, want adaptive", th["type"])
	}
	oc, _ := got["output_config"].(map[string]any)
	if oc["effort"] != "xhigh" {
		t.Errorf("adaptive inference profile effort = %v, want xhigh", oc["effort"])
	}

	// Custom map should override built-in effort mapping.
	got = buildBedrockAdditionalFields(&Model{ID: "anthropic.claude-opus-4-6-v1", ThinkingLevelMap: ThinkingLevelMap{ThinkingXHigh: new("max")}}, "", StreamOptions{Thinking: ThinkingXHigh, IsReasoning: true})
	oc, _ = got["output_config"].(map[string]any)
	if oc["effort"] != "max" {
		t.Errorf("adaptive mapped effort = %v, want max", oc["effort"])
	}

	// IsReasoning=false → no thinking config even on Claude.
	if got := buildBedrockAdditionalFields(&Model{ID: "anthropic.claude-3-7-sonnet-20250219-v1:0"}, "", StreamOptions{Thinking: ThinkingMedium}); got != nil {
		t.Errorf("IsReasoning=false must skip thinking config, got %+v", got)
	}
}

func TestBedrockBuildAdditionalFields_UsesModelCapWhenMaxTokensUnset(t *testing.T) {
	got := buildBedrockAdditionalFields(&Model{
		ID:           "anthropic.claude-3-7-sonnet-20250219-v1:0",
		Capabilities: ModelCapabilities{MaxOutputTokens: 128000},
	}, "", StreamOptions{Thinking: ThinkingHigh, IsReasoning: true})
	if got == nil {
		t.Fatal("expected thinking config")
	}
	th, ok := got["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("thinking = %T, want map", got["thinking"])
	}
	if budget, _ := th["budget_tokens"].(int); budget != 16384 {
		t.Fatalf("budget_tokens = %v, want 16384", th["budget_tokens"])
	}
}

func TestBedrockAdaptiveThinkingWithModelName(t *testing.T) {
	if !supportsBedrockAdaptiveThinkingWithName("arn:aws:bedrock:::application-profile/foo", "Claude Sonnet 4.6") {
		t.Fatal("expected model name to enable adaptive thinking detection")
	}
	// Claude 5 (sonnet-5) is a 0.81 addition to adaptive-thinking + native xhigh.
	if !supportsBedrockAdaptiveThinkingWithName("arn:aws:bedrock:::application-profile/foo", "Claude Sonnet 5") {
		t.Fatal("expected sonnet-5 to enable adaptive thinking detection")
	}
	if !supportsNativeXhighEffort(&Model{ID: "anthropic.claude-sonnet-5-20260101-v1:0"}) {
		t.Fatal("expected sonnet-5 to support native xhigh effort")
	}
	if !supportsBedrockThinkingSignatureWithName("arn:aws:bedrock:::application-profile/foo", "Claude Sonnet 4.6") {
		t.Fatal("expected model name to enable thinking signature support detection")
	}
}

func TestBedrockMapStopReason(t *testing.T) {
	cases := map[string]struct {
		reason StopReason
		errMsg string
	}{
		"end_turn":                      {StopReasonStop, ""},
		"stop_sequence":                 {StopReasonStop, ""},
		"max_tokens":                    {StopReasonLength, ""},
		"model_context_window_exceeded": {StopReasonLength, ""},
		"tool_use":                      {StopReasonToolUse, ""},
		"guardrail_intervened":          {StopReasonError, "guardrail_intervened"},
		"":                              {StopReasonError, ""},
	}
	for in, want := range cases {
		got, gotMsg := mapBedrockStopReason(in)
		if got != want.reason || gotMsg != want.errMsg {
			t.Errorf("mapBedrockStopReason(%q) = (%q, %q), want (%q, %q)", in, got, gotMsg, want.reason, want.errMsg)
		}
	}
}

// fakeAPIErr satisfies smithy.APIError so we can drive the error
// formatter without standing up a real AWS call.
type fakeAPIErr struct {
	code    string
	message string
}

func (e *fakeAPIErr) Error() string                 { return e.code + ": " + e.message }
func (e *fakeAPIErr) ErrorCode() string             { return e.code }
func (e *fakeAPIErr) ErrorMessage() string          { return e.message }
func (e *fakeAPIErr) ErrorFault() smithy.ErrorFault { return smithy.FaultServer }

func TestBedrockFormatError(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{nil, ""},
		{errors.New("plain"), "plain"},
		{&fakeAPIErr{code: "ThrottlingException", message: "Too many requests"}, "Throttling error: Too many requests"},
		{&fakeAPIErr{code: "ValidationException", message: "bad input"}, "Validation error: bad input"},
		{&fakeAPIErr{code: "ServiceUnavailableException", message: "down"}, "Service unavailable: down"},
		{&fakeAPIErr{code: "UnknownCode", message: "oops"}, "UnknownCode: oops"},
		// #5561: data-retention errors get an AWS docs hint appended.
		{&fakeAPIErr{code: "ValidationException", message: "data retention mode 'default' is not available for this model"}, "Validation error: data retention mode 'default' is not available for this model See https://docs.aws.amazon.com/bedrock/latest/userguide/data-retention.html for supported data retention modes."},
		{errors.New("Data Retention Mode rejected"), "Data Retention Mode rejected See https://docs.aws.amazon.com/bedrock/latest/userguide/data-retention.html for supported data retention modes."},
	}
	for _, tc := range cases {
		if got := formatBedrockError(tc.err); got != tc.want {
			t.Errorf("formatBedrockError(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestBedrockSanitizeSurrogates(t *testing.T) {
	// Plain ASCII passes through.
	if got := sanitizeSurrogates("hello"); got != "hello" {
		t.Errorf("sanitize(plain) = %q, want hello", got)
	}
	// Constructed string containing a surrogate code point is dropped.
	// Go strings are UTF-8 but the helper still strips any rune that
	// would map to a UTF-16 surrogate half if re-encoded.
	in := string([]rune{'a', 0xD800, 'b'})
	got := sanitizeSurrogates(in)
	if strings.ContainsRune(got, 0xD800) {
		t.Errorf("sanitize must drop 0xD800: got %q", got)
	}
}

func TestBedrockProvider_ConstructAndID(t *testing.T) {
	p := NewBedrockProvider("anthropic.claude-3-7-sonnet-20250219-v1:0", "")
	if p.ID() != "amazon-bedrock" {
		t.Errorf("ID() = %q, want amazon-bedrock", p.ID())
	}
	if err := p.Close(); err != nil {
		t.Errorf("Close() = %v", err)
	}
}

// _ keeps bdoc imported so test file always compiles in isolation
// (some helpers reuse the document package indirectly).
var _ = bdoc.NewLazyDocument

var _ = os.Setenv // satisfy importer if the env API set above were dropped
