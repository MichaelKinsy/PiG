package ai

// pi: packages/ai/test/openai-completions-tool-choice.test.ts
// pi: packages/ai/src/api/openai-completions.ts
// pi: packages/ai/catalog/providers/github-copilot.ts
//
// Pi 86918f9eeb (#12110, closes #12019): GitHub Copilot's chat-completions models send reasoning_effort (their catalog
// records say supportsReasoningEffort from models.dev's effort options, and so gain a thinkingLevelMap), and
// parseChunkUsage reads a top-level usage.reasoning_tokens when completion_tokens_details has none.

import (
	"encoding/json"
	"reflect"
	"testing"
)

// copilotReasoningUsage is a chunk usage with Pi's counts (prompt 10, completion 40, no cache read), the nested details member and the
// top-level reasoning_tokens member as raw JSON; an empty string leaves the member out.
func copilotReasoningUsage(nested, top string) string {
	usage := `{"prompt_tokens":10,"completion_tokens":40,"prompt_tokens_details":{"cached_tokens":0}`
	if nested != "" {
		usage += `,"completion_tokens_details":` + nested
	}
	if top != "" {
		usage += `,"reasoning_tokens":` + top
	}
	return usage + "}"
}

var copilotReasoningUsageRows = []struct {
	name        string
	nested, top string
	reasoning   int
}{
	// openai-completions-tool-choice.test.ts:1771 "reads top-level reasoning_tokens when completion_tokens_details has none"
	{"top-level only (Pi #12019)", "", "30", 30},
	{"nested zero, top-level present", `{"reasoning_tokens":0}`, "30", 30},
	{"nested member missing, top-level present", `{}`, "30", 30},
	{"nested null, top-level present", `{"reasoning_tokens":null}`, "30", 30},
	{"details not an object, top-level present", `"x"`, "30", 30},
	{"both present: the nested count wins", `{"reasoning_tokens":21}`, "30", 21},
	{"nested present, top-level mistyped and unread", `{"reasoning_tokens":21}`, `"x"`, 21},
	{"nested only", `{"reasoning_tokens":21}`, "", 21},
	{"neither", "", "", 0},
	{"top-level zero", "", "0", 0},
	{"top-level null", "", "null", 0},
	{"top-level false", "", "false", 0},
	{"top-level empty string", "", `""`, 0},
}

func usageReasoning(usage *Usage) int {
	if usage == nil || usage.Reasoning == nil {
		return 0
	}
	return *usage.Reasoning
}

// The streamed path (the JS-read normalisation, then the typed usage), for a GitHub Copilot model, a built-in provider that is not
// Copilot and a custom base URL model: the fallback is parseChunkUsage's, so it holds for every openai-completions provider. A
// top-level count changes usage.reasoning only: input, output, cache, totalTokens and cost are those of the same usage without it.
func TestOpenAICompletionsTopLevelReasoningTokensUpstream(t *testing.T) {
	const head = `{"id":"chatcmpl-copilot-reasoning","choices":[{"delta":{"content":"OK"},"finish_reason":null}]}`
	request := Context{Messages: []Message{UserMessage{Content: UserText("Reply with exactly OK")}}}
	models := map[string]func() *Model{
		"github-copilot/gemini-4-argon": func() *Model { return toolChoiceModel(t, "github-copilot", "gemini-4-argon", false) },
		"openai/gpt-4o-mini":            func() *Model { return toolChoiceModel(t, "openai", "gpt-4o-mini", true) },
		"moonshotai/kimi-k3":            func() *Model { return toolChoiceModel(t, "moonshotai", "kimi-k3", false) },
		"custom base URL":               func() *Model { return localToolChoiceModel("local-reasoner", "Local Reasoner") },
	}
	for modelName, model := range models {
		for _, row := range copilotReasoningUsageRows {
			for _, onChoice := range []bool{false, true} {
				name := modelName + "/" + row.name
				if onChoice {
					name += "/choice usage"
				}
				t.Run(name, func(t *testing.T) {
					last := func(usage string) string {
						if onChoice {
							return `{"id":"chatcmpl-copilot-reasoning","choices":[{"delta":{},"finish_reason":"stop","usage":` + usage + `}]}`
						}
						return `{"id":"chatcmpl-copilot-reasoning","choices":[{"delta":{},"finish_reason":"stop"}],"usage":` + usage + `}`
					}
					_, response, _ := captureToolChoiceRequest(t, model(), request, StreamOptions{}, []string{head, last(copilotReasoningUsage(row.nested, row.top))})
					if response.StopReason != StopReasonStop {
						t.Fatalf("stop %q error %q", response.StopReason, response.ErrorMessage)
					}
					usage := response.Usage
					if got := usageReasoning(&usage); got != row.reasoning || usage.Input != 10 || usage.Output != 40 || usage.CacheRead != 0 || usage.CacheWrite != 0 || usage.TotalTokens != 50 {
						encoded, _ := json.Marshal(usage)
						t.Fatalf("usage=%s, want reasoning %d, input 10, output 40, totalTokens 50", encoded, row.reasoning)
					}
					_, without, _ := captureToolChoiceRequest(t, model(), request, StreamOptions{}, []string{head, last(copilotReasoningUsage(row.nested, ""))})
					plain := without.Usage
					plain.Reasoning, usage.Reasoning = nil, nil
					if !reflect.DeepEqual(plain, usage) {
						t.Fatalf("the top-level count changed more than usage.reasoning:\nwith    %+v\nwithout %+v", usage, plain)
					}
				})
			}
		}
	}
}

// The typed path alone: parseChunkUsage over the decoded usage, with a priced model.
func TestParseChunkUsageTopLevelReasoningTokens(t *testing.T) {
	cost := ModelCost{Input: 2, Output: 10, CacheRead: 0.1}
	parse := func(t *testing.T, text string) *Usage {
		t.Helper()
		var raw oaiUsage
		if err := json.Unmarshal([]byte(text), &raw); err != nil {
			t.Fatal(err)
		}
		return parseChunkUsage(&raw, cost, false)
	}
	for _, row := range []struct {
		name        string
		nested, top string
		reasoning   int
	}{
		{"top-level only (Pi #12019)", "", "30", 30},
		{"nested zero, top-level present", `{"reasoning_tokens":0}`, "30", 30},
		{"nested member missing, top-level present", `{}`, "30", 30},
		{"both present: the nested count wins", `{"reasoning_tokens":21}`, "30", 21},
		{"nested only", `{"reasoning_tokens":21}`, "", 21},
		{"neither", "", "", 0},
		{"top-level zero", "", "0", 0},
	} {
		t.Run(row.name, func(t *testing.T) {
			usage := parse(t, copilotReasoningUsage(row.nested, row.top))
			if got := usageReasoning(usage); got != row.reasoning || usage.Input != 10 || usage.Output != 40 || usage.TotalTokens != 50 {
				t.Fatalf("usage=%+v, want reasoning %d, input 10, output 40, totalTokens 50", usage, row.reasoning)
			}
			plain := parse(t, copilotReasoningUsage(row.nested, ""))
			if !reflect.DeepEqual(usage.Cost, plain.Cost) || usage.Cost.Total == 0 {
				t.Fatalf("cost %+v, without the top-level count %+v: reasoning tokens are part of completion_tokens and have no cost of their own", usage.Cost, plain.Cost)
			}
		})
	}
}

// openai-completions-tool-choice.test.ts:1809 "sends reasoning_effort for GitHub Copilot completions models with effort levels",
// with the other levels of the three Copilot records and rows that the change must leave as they were: a level-less request, two
// other providers' models and a custom base URL model.
func TestGitHubCopilotCompletionsReasoningEffortUpstream(t *testing.T) {
	catalog := func(provider, id string) func() *Model {
		return func() *Model { return toolChoiceModel(t, provider, id, false) }
	}
	for _, row := range []struct {
		name  string
		model func() *Model
		level ThinkingLevel
		want  string // "" = no reasoning_effort member
	}{
		{"copilot gemini-4-argon low (Pi)", catalog("github-copilot", "gemini-4-argon"), "low", `"low"`},
		{"copilot gemini-4-argon high (Pi)", catalog("github-copilot", "gemini-4-argon"), "high", `"high"`},
		{"copilot gemini-4-argon medium", catalog("github-copilot", "gemini-4-argon"), "medium", `"medium"`},
		{"copilot gemini-4-argon no level", catalog("github-copilot", "gemini-4-argon"), "", ""},
		{"copilot gemini-3.8-flash low", catalog("github-copilot", "gemini-3.8-flash"), "low", `"low"`},
		{"copilot gemini-3.8-flash medium", catalog("github-copilot", "gemini-3.8-flash"), "medium", `"medium"`},
		{"copilot gemini-3.8-flash high", catalog("github-copilot", "gemini-3.8-flash"), "high", `"high"`},
		{"copilot kimi-k3 low", catalog("github-copilot", "kimi-k3"), "low", `"low"`},
		{"copilot kimi-k3 high", catalog("github-copilot", "kimi-k3"), "high", `"high"`},
		{"copilot kimi-k3 max", catalog("github-copilot", "kimi-k3"), "max", `"max"`},
		{"copilot kimi-k3 no level", catalog("github-copilot", "kimi-k3"), "", ""},
		// Unchanged by the commit.
		{"moonshotai kimi-k3 high", catalog("moonshotai", "kimi-k3"), "high", `"high"`},
		{"moonshotai kimi-k3 max", catalog("moonshotai", "kimi-k3"), "max", `"max"`},
		{"moonshotai kimi-k3 no level", catalog("moonshotai", "kimi-k3"), "", ""},
		{"zai glm-5.3 high", catalog("zai", "glm-5.3"), "high", `"high"`},
		{"custom base URL high", func() *Model { return localToolChoiceModel("local-reasoner", "Local Reasoner") }, "high", `"high"`},
		{"custom base URL low", func() *Model { return localToolChoiceModel("local-reasoner", "Local Reasoner") }, "low", `"low"`},
		{"custom base URL no level", func() *Model { return localToolChoiceModel("local-reasoner", "Local Reasoner") }, "", ""},
	} {
		t.Run(row.name, func(t *testing.T) {
			payload, _, _ := captureToolChoiceRequest(t, row.model(), toolChoiceHi(), StreamOptions{Thinking: row.level}, nil)
			got, present := payload["reasoning_effort"]
			if row.want == "" {
				if present {
					t.Fatalf("reasoning_effort=%s, want no member", got)
				}
				return
			}
			if string(got) != row.want {
				t.Fatalf("reasoning_effort=%q (present %v), want %s", got, present, row.want)
			}
		})
	}
}

// The catalog records behind the request: supportsReasoningEffort and the effort map models.dev lists for each model.
func TestGitHubCopilotCompletionsCatalogReasoningUpstream(t *testing.T) {
	for _, row := range []struct {
		id   string
		want ThinkingLevelMap
	}{
		{"gemini-4-argon", ThinkingLevelMap{ThinkingOff: nil, ThinkingMinimal: nil, ThinkingLow: new("low"), ThinkingMedium: new("medium"), ThinkingHigh: new("high"), ThinkingXHigh: nil, ThinkingMax: nil}},
		{"gemini-3.8-flash", ThinkingLevelMap{ThinkingOff: nil, ThinkingMinimal: nil, ThinkingLow: new("low"), ThinkingMedium: new("medium"), ThinkingHigh: new("high"), ThinkingXHigh: nil, ThinkingMax: nil}},
		{"kimi-k3", ThinkingLevelMap{ThinkingOff: nil, ThinkingMinimal: nil, ThinkingLow: new("low"), ThinkingMedium: nil, ThinkingHigh: new("high"), ThinkingXHigh: nil, ThinkingMax: new("max")}},
	} {
		t.Run(row.id, func(t *testing.T) {
			model := toolChoiceModel(t, "github-copilot", row.id, false)
			compat := model.ProviderMeta.Compat
			if compat == nil || compat.SupportsReasoningEffort == nil || !*compat.SupportsReasoningEffort {
				t.Fatalf("compat=%#v, want supportsReasoningEffort true", compat)
			}
			if compat.SupportsStore == nil || *compat.SupportsStore || compat.SupportsDeveloperRole == nil || *compat.SupportsDeveloperRole {
				t.Fatalf("compat=%#v, want supportsStore and supportsDeveloperRole false as before", compat)
			}
			if !reflect.DeepEqual(model.ThinkingLevelMap, row.want) {
				t.Fatalf("thinkingLevelMap=%#v", model.ThinkingLevelMap)
			}
		})
	}
	// Every Copilot chat-completions record is one of the three: a record this test does not know would be a catalog change to read.
	for _, generated := range GeneratedModels {
		if generated.Provider == "github-copilot" && generated.API == APIOpenAICompletions && generated.ID != "gemini-4-argon" && generated.ID != "gemini-3.8-flash" && generated.ID != "kimi-k3" {
			t.Errorf("github-copilot/%s is a chat-completions record this test has no row for", generated.ID)
		}
	}
}
