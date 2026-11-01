package coding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type resolverProbe struct {
	Kind     string   `json:"kind"`
	Provider string   `json:"provider"`
	Model    string   `json:"model"`
	Thinking string   `json:"thinking"`
	Patterns []string `json:"patterns"`
}

func resolverProbes() []resolverProbe {
	rng := rand.New(rand.NewPCG(23, 9))
	pick := func(items []string) string { return items[rng.IntN(len(items))] }
	models := []string{"claude-sonnet-4-5", "claude-opus-4-8", "claude-haiku-4-5", "gpt-4o", "gpt-5", "o3", "gpt-4.1", "sonnet", "opus", "haiku", "gpt", "4o", "claude", "nope", "claude-sonnet-4-5-20250929", "gpt-4o-mini", "GPT-5", "Claude-Opus-4-8"}
	providers := []string{"anthropic", "openai", "google", "nope", "ANTHROPIC", ""}
	levels := []string{"off", "minimal", "low", "medium", "high", "xhigh", "bogus", ""}
	pattern := func() string {
		text := pick(models)
		switch rng.IntN(6) {
		case 0:
			text = pick(providers) + "/" + text
		case 1:
			text += ":" + pick(levels)
		case 2:
			text = pick(providers) + "/" + text + ":" + pick(levels)
		case 3:
			text = "*" + pick([]string{"sonnet", "gpt", "4o", "claude-*", "mini"}) + "*"
		case 4:
			text = pick(providers) + "/*"
		}
		return text
	}
	var probes []resolverProbe
	for range 700 {
		if rng.IntN(3) == 0 {
			probe := resolverProbe{Kind: "scope", Patterns: []string{}}
			for range rng.IntN(4) {
				probe.Patterns = append(probe.Patterns, pattern())
			}
			probes = append(probes, probe)
			continue
		}
		probe := resolverProbe{Kind: "cli", Provider: pick(providers), Model: pattern(), Thinking: pick(levels)}
		if rng.IntN(8) == 0 {
			probe.Model = ""
		}
		probes = append(probes, probe)
	}
	return probes
}

// resolveCliModel and resolveModelScopeWithDiagnostics (model-resolver.ts) against pinned Pi over 700 seeded probes on the built-in catalog with three credential sets (Anthropic and OpenAI, OpenAI only, none): exact ids, provider-prefixed ids, partial and mixed-case matches, globs, ":level" suffixes (valid and invalid), an unknown provider, the unauthenticated catalog for --api-key, and the diagnostics a scope reports; model, thinking level, warning, error and diagnostics must agree.
func TestResolveCliModelAndScopeMatchPi(t *testing.T) {
	probes := resolverProbes()
	input, err := json.Marshal(map[string]any{"probes": probes})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/model_resolver_cli.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]map[string]any
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	for set, authed := range [][]string{{"anthropic", "openai"}, {"openai"}, {}} {
		dir := t.TempDir()
		services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(services.Close)
		for _, provider := range authed {
			if err := services.ModelRuntime().SetRuntimeApiKey(context.Background(), provider, "k-"+provider); err != nil {
				t.Fatal(err)
			}
		}
		runtime := services.ModelRuntime()
		ref := func(model *ai.Model) any {
			if model == nil {
				return nil
			}
			return model.ProviderID() + "/" + model.ID
		}
		nilIfEmpty := func(text string) any {
			if text == "" {
				return nil
			}
			return text
		}
		failures := 0
		for i, probe := range probes {
			var got map[string]any
			if probe.Kind == "cli" {
				result := ResolveCliModel(ResolveCliModelOptions{CliProvider: probe.Provider, CliModel: probe.Model, CliThinking: ai.ThinkingLevel(probe.Thinking), ModelRuntime: runtime})
				got = map[string]any{"model": ref(result.Model), "thinking": nilIfEmpty(string(result.ThinkingLevel)), "warning": nilIfEmpty(result.Warning), "error": nilIfEmpty(result.Error)}
			} else {
				result, err := ResolveModelScopeWithDiagnostics(context.Background(), probe.Patterns, runtime)
				if err != nil {
					got = map[string]any{"threw": err.Error()}
				} else {
					scoped := [][]any{}
					for _, entry := range result.ScopedModels {
						scoped = append(scoped, []any{ref(entry.Model), nilIfEmpty(string(entry.ThinkingLevel))})
					}
					diagnostics := [][]any{}
					for _, diagnostic := range result.Diagnostics {
						diagnostics = append(diagnostics, []any{diagnostic.Code, diagnostic.Message, diagnostic.Pattern})
					}
					got = map[string]any{"scoped": scoped, "diagnostics": diagnostics}
				}
			}
			raw, _ := json.Marshal(got)
			var normalized map[string]any
			_ = json.Unmarshal(raw, &normalized)
			if reflect.DeepEqual(normalized, expected[set][i]) {
				continue
			}
			if failures++; failures > 5 {
				continue
			}
			p, _ := json.Marshal(probe)
			e, _ := json.Marshal(expected[set][i])
			t.Errorf("credentials %v probe %d %s\n  Pig %s\n  Pi  %s", authed, i, p, raw, e)
		}
		if failures > 5 {
			t.Errorf("credentials %v: %d of %d probes differ from Pi", authed, failures, len(probes))
		}
	}
	_ = fmt.Sprint
}
