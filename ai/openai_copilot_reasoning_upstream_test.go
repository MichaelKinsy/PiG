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

// zzEffortFixture is a chat-completions model of a fixed fixture catalog: a hand-written record (id zz-fixture-..., never a models.dev id) that goes through
// the path a catalog record takes (GeneratedModel.ToModel and ToCapabilities), so the behaviour under test does not follow models.dev data. compat is the
// record's compat bag and levels its thinkingLevelMap; the shapes below are the ones Pi's catalog gives the models the tests are about.
func zzEffortFixture(provider, id string, compat *ModelCompat, levels ThinkingLevelMap) *Model {
	generated := GeneratedModel{ID: id, Provider: provider, DisplayName: "ZZ Fixture " + id, API: APIOpenAICompletions, BaseURL: "https://fixture.invalid/v1",
		Compat: compat, ThinkingLevelMap: levels, ContextWindow: 128000, MaxOutputTokens: 8192, Reasoning: levels != nil, Capabilities: []string{"text"}}
	model := generated.ToModel()
	model.Capabilities = generated.ToCapabilities()
	model.Capabilities.MaxThinking = thinkingMaxLevel(generated.Reasoning, generated.ThinkingLevelMap)
	model.ProviderMeta.API = APIOpenAICompletions
	return model
}

var (
	// zzCopilotEffortCompat is the compat of Pi's github-copilot chat-completions records that gained supportsReasoningEffort (Pi #12110).
	zzCopilotEffortCompat = func() *ModelCompat {
		return &ModelCompat{SupportsStrictMode: new(true), SupportsStore: new(false), SupportsDeveloperRole: new(false), SupportsReasoningEffort: new(true)}
	}
	// zzLowMediumHigh is the effort map of Pi's gemini-4-argon / gemini-3.8-flash records; zzLowHighMax the one of kimi-k3.
	zzLowMediumHigh = ThinkingLevelMap{ThinkingOff: nil, ThinkingMinimal: nil, ThinkingLow: new("low"), ThinkingMedium: new("medium"), ThinkingHigh: new("high"), ThinkingXHigh: nil, ThinkingMax: nil}
	zzLowHighMax    = ThinkingLevelMap{ThinkingOff: nil, ThinkingMinimal: nil, ThinkingLow: new("low"), ThinkingMedium: nil, ThinkingHigh: new("high"), ThinkingXHigh: nil, ThinkingMax: new("max")}
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
		// Pi's own case (:1790) names this catalog model; the others are fixture records of the shapes a built-in provider, a Moonshot-style provider and a custom model give.
		"github-copilot/gemini-4-argon": func() *Model { return toolChoiceModel(t, "github-copilot", "gemini-4-argon", false) },
		"fixture openai without compat": func() *Model { return zzEffortFixture("openai", "zz-fixture-plain", nil, nil) },
		"fixture moonshotai": func() *Model {
			return zzEffortFixture("moonshotai", "zz-fixture-moonshot", &ModelCompat{SupportsReasoningEffort: new(true), SupportsStore: new(false), SupportsDeveloperRole: new(false), MaxTokensField: "max_tokens", ThinkingFormat: "openai"}, zzLowHighMax)
		},
		"custom base URL": func() *Model { return localToolChoiceModel("local-reasoner", "Local Reasoner") },
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

// openai-completions-tool-choice.test.ts:1809 "sends reasoning_effort for GitHub Copilot completions models with effort levels": Pi's rows are the
// catalog model gemini-4-argon at low and high. The other levels, a level-less request, the effort map with a max level, other providers' shapes and a
// custom base URL model are fixture records (see zzEffortFixture): they test the map -> reasoning_effort behaviour, not models.dev's data.
func TestGitHubCopilotCompletionsReasoningEffortUpstream(t *testing.T) {
	catalog := func(provider, id string) func() *Model {
		return func() *Model { return toolChoiceModel(t, provider, id, false) }
	}
	copilot := func(levels ThinkingLevelMap) func() *Model {
		return func() *Model {
			return zzEffortFixture("github-copilot", "zz-fixture-copilot", zzCopilotEffortCompat(), levels)
		}
	}
	zai := func() *Model {
		return zzEffortFixture("zai", "zz-fixture-zai", &ModelCompat{SupportsReasoningEffort: new(true), SupportsStore: new(false), SupportsDeveloperRole: new(false), MaxTokensField: "max_tokens", ThinkingFormat: "zai"}, zzLowHighMax)
	}
	moonshot := func() *Model {
		return zzEffortFixture("moonshotai", "zz-fixture-moonshot", &ModelCompat{SupportsReasoningEffort: new(true), SupportsStore: new(false), SupportsDeveloperRole: new(false), MaxTokensField: "max_tokens", ThinkingFormat: "openai"}, zzLowHighMax)
	}
	for _, row := range []struct {
		name  string
		model func() *Model
		level ThinkingLevel
		want  string // "" = no reasoning_effort member
	}{
		{"copilot gemini-4-argon low (Pi)", catalog("github-copilot", "gemini-4-argon"), "low", `"low"`},
		{"copilot gemini-4-argon high (Pi)", catalog("github-copilot", "gemini-4-argon"), "high", `"high"`},
		{"copilot fixture low-medium-high: medium", copilot(zzLowMediumHigh), "medium", `"medium"`},
		{"copilot fixture low-medium-high: no level", copilot(zzLowMediumHigh), "", ""},
		{"copilot fixture low-medium-high: low", copilot(zzLowMediumHigh), "low", `"low"`},
		{"copilot fixture low-medium-high: high", copilot(zzLowMediumHigh), "high", `"high"`},
		{"copilot fixture low-high-max: low", copilot(zzLowHighMax), "low", `"low"`},
		{"copilot fixture low-high-max: high", copilot(zzLowHighMax), "high", `"high"`},
		{"copilot fixture low-high-max: max", copilot(zzLowHighMax), "max", `"max"`},
		{"copilot fixture low-high-max: no level", copilot(zzLowHighMax), "", ""},
		// Unchanged by the commit.
		{"moonshot-shaped fixture high", moonshot, "high", `"high"`},
		{"moonshot-shaped fixture max", moonshot, "max", `"max"`},
		{"moonshot-shaped fixture no level", moonshot, "", ""},
		{"zai-shaped fixture high", zai, "high", `"high"`},
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
