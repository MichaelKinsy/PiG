package compaction

import (
	"bytes"
	"context"
	"encoding/json"
	"math/rand/v2"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type generateSummaryScenario struct {
	Messages  []map[string]any  `json:"messages"`
	Reasoning bool              `json:"reasoning"`
	MaxTokens int               `json:"maxTokens"`
	Reserve   int               `json:"reserve"`
	APIKey    *string           `json:"apiKey"`
	Headers   map[string]string `json:"headers"`
	Custom    string            `json:"custom"`
	Previous  string            `json:"previous"`
	Thinking  string            `json:"thinking"`
	Env       map[string]string `json:"env"`
	SessionID string            `json:"sessionId"`
	WithUsage bool              `json:"withUsage"`
}

// generateSummary and generateSummaryWithUsage (core/compaction/compaction.ts) against pinned Pi through a recording stream function over 1500 seeded calls: the system prompt, the user prompt (conversation, previous summary, extra focus), the token budget (0.8 of the reserve, capped by the model's limit, which may be zero), the auth, headers and environment, the reasoning level for reasoning models only, the session id, and the text and usage returned.
func TestGenerateSummaryMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewPCG(9, 14))
	pick := func(items []string) string { return items[rng.IntN(len(items))] }
	texts := []string{"hello", "Ünï 日本語 😀", "line1\nline2", "", "<b>&</b>", "a\u2028b"}
	var scenarios []generateSummaryScenario
	for range 1500 {
		scenario := generateSummaryScenario{
			Reasoning: rng.IntN(2) == 0, MaxTokens: []int{0, 100, 4096, 64000, 1000000}[rng.IntN(5)], Reserve: []int{0, 1, 5, 99, 100, 10000, 16384, 1000000}[rng.IntN(8)],
			Custom: pick([]string{"", "", "focus on tests", "ünï\nfocus"}), Previous: pick([]string{"", "", "earlier summary", "## Goal\nold"}),
			Thinking: pick([]string{"", "off", "minimal", "low", "medium", "high", "xhigh"}), SessionID: pick([]string{"", "", "sess-1"}), WithUsage: rng.IntN(2) == 0,
		}
		if rng.IntN(3) > 0 {
			scenario.APIKey = new(pick([]string{"k", "", "sk-ünï"}))
		}
		if rng.IntN(2) == 0 {
			scenario.Headers = map[string]string{"X-A": "1", "X-B": pick([]string{"b", ""})}
		}
		if rng.IntN(2) == 0 {
			scenario.Env = map[string]string{"E": "1", "PATH_X": pick([]string{"p", ""})}
		}
		for range 1 + rng.IntN(4) {
			if rng.IntN(2) == 0 {
				scenario.Messages = append(scenario.Messages, map[string]any{"role": "user", "content": pick(texts), "timestamp": 1})
			} else {
				scenario.Messages = append(scenario.Messages, map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": pick(texts)}}, "api": "anthropic-messages", "provider": "anthropic", "model": "m", "usage": map[string]any{"input": 1, "output": 1, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 2, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}, "stopReason": "stop", "timestamp": 2})
			}
		}
		scenarios = append(scenarios, scenario)
	}
	input, err := json.Marshal(scenarios)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/generate_summary_pi.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []map[string]any
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	mismatches := 0
	for i, scenario := range scenarios {
		var messages []agent.AgentMessage
		for _, raw := range scenario.Messages {
			data, _ := json.Marshal(raw)
			var message agent.AgentMessage
			if err := json.Unmarshal(data, &message); err != nil {
				t.Fatal(err)
			}
			messages = append(messages, message)
		}
		model := &ai.Model{ID: "m", DisplayName: "M", Capabilities: ai.ModelCapabilities{ContextWindow: 200000, MaxOutputTokens: scenario.MaxTokens},
			ProviderMeta: ai.ProviderMetadata{ProviderID: "anthropic", API: "anthropic-messages", BaseURL: "https://example.test", Reasoning: scenario.Reasoning}}
		if scenario.Reasoning {
			model.Capabilities.MaxThinking = ai.ThinkingLevelXHigh
		}
		var seen map[string]any
		streamFn := func(_ context.Context, _ *ai.Model, system string, requestMessages []agent.AgentMessage, options ai.StreamOptions) (string, *ai.Usage, error) {
			var user []string
			var roles []string
			roles = append(roles, "system")
			for _, message := range requestMessages {
				roles = append(roles, "user")
				for _, block := range message.ContentBlocks() {
					if text, ok := block.(ai.TextContent); ok {
						user = append(user, text.Text)
					}
				}
			}
			headers := map[string]any{}
			for name, value := range options.Headers {
				if value != nil {
					headers[name] = *value
				}
			}
			record := map[string]any{"cacheRetention": string(options.CacheRetention)}
			// Go's zero MaxTokens is Pi's absent maxTokens (no limit set; summaries use the model's own since Pi 6fcb4867).
			if options.MaxTokens != 0 {
				record["maxTokens"] = options.MaxTokens
			}
			if options.APIKey != "" || scenario.APIKey != nil {
				record["apiKey"] = options.APIKey
			}
			if len(headers) > 0 {
				record["headers"] = headers
			}
			if len(options.Env) > 0 {
				record["env"] = options.Env
			}
			if options.IsReasoning && options.Thinking != "" {
				record["reasoning"] = string(options.Thinking)
			}
			if options.SessionID != "" {
				record["sessionId"] = options.SessionID
			}
			seen = map[string]any{"system": []any{system}, "roles": roles, "user": user, "options": record}
			return "summary text", &ai.Usage{Input: 7, Output: 3, CacheRead: 1, CacheWrite: 2, TotalTokens: 13}, nil
		}
		thinking := ai.ModelThinkingLevel(scenario.Thinking)
		apiKey := ""
		if scenario.APIKey != nil {
			apiKey = *scenario.APIKey
		}
		got := map[string]any{}
		if scenario.WithUsage {
			text, usage, err := GenerateSummaryWithUsage(t.Context(), messages, model, scenario.Reserve, apiKey, scenario.Headers, scenario.Custom, scenario.Previous, thinking, streamFn, scenario.Env, nil, ai.RetryCallbacks{}, scenario.SessionID)
			if err != nil {
				got["thrown"] = err.Error()
			} else {
				got["text"] = text
				got["usage"] = map[string]any{"input": usage.Input, "output": usage.Output, "cacheRead": usage.CacheRead, "cacheWrite": usage.CacheWrite, "totalTokens": usage.TotalTokens}
			}
		} else {
			text, err := GenerateSummary(t.Context(), messages, model, scenario.Reserve, apiKey, scenario.Headers, scenario.Custom, scenario.Previous, thinking, streamFn, scenario.Env, nil, ai.RetryCallbacks{}, scenario.SessionID)
			if err != nil {
				got["thrown"] = err.Error()
			} else {
				got["text"] = text
			}
		}
		got["seen"] = seen
		expected := want[i]
		// Both sides leave the session id empty when none is given and then generate one: compared as present.
		normalize := func(value map[string]any) map[string]any {
			raw, _ := json.Marshal(value)
			var out map[string]any
			_ = json.Unmarshal(raw, &out)
			if seen, ok := out["seen"].(map[string]any); ok {
				if options, ok := seen["options"].(map[string]any); ok {
					if _, has := options["sessionId"]; has && scenario.SessionID == "" {
						options["sessionId"] = "<generated>"
					}
				}
			}
			return out
		}
		if !reflect.DeepEqual(normalize(got), normalize(expected)) {
			if mismatches++; mismatches <= 5 {
				g, _ := json.Marshal(normalize(got))
				e, _ := json.Marshal(normalize(expected))
				t.Errorf("scenario %d:\n Go %.700s\n Pi %.700s", i, g, e)
			}
		}
	}
	if mismatches > 5 {
		t.Errorf("%d of %d calls differ from Pi", mismatches, len(scenarios))
	}
}
