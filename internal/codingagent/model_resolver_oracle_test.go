package codingagent

import (
	"bytes"
	"encoding/json"
	"math/rand/v2"
	"os/exec"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type resolverModel struct {
	Provider  string `json:"provider"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Reasoning bool   `json:"reasoning"`
}

type resolverCatalog struct {
	Models []resolverModel `json:"models"`
	Authed []string        `json:"authed"`
}

type resolverRuntime struct{ catalog resolverCatalog }

func (r resolverRuntime) GetModels(string) []RuntimeModel {
	models := make([]RuntimeModel, 0, len(r.catalog.Models))
	for _, m := range r.catalog.Models {
		models = append(models, RuntimeModel{Provider: m.Provider, ID: m.ID, Name: m.Name, Reasoning: m.Reasoning})
	}
	return models
}
func (r resolverRuntime) HasConfiguredAuth(provider string) bool {
	return slices.Contains(r.catalog.Authed, provider)
}

func resolverKey(m *RuntimeModel) []any {
	if m == nil {
		return nil
	}
	return []any{m.Provider, m.ID, m.Name, m.Reasoning}
}

// core/model-resolver.ts findExactModelReferenceMatch, parseModelPattern (strict and lenient), resolveModelScopeFromModels and resolveCliModel
// against pinned Pi, over catalogs of models with dated and alias ids, ids with slashes and colons, names, case and Unicode variants, and
// patterns built from them: references, partial text, thinking suffixes, globs and ECMAScript whitespace.
func TestModelResolverMatchesPi(t *testing.T) {
	catalogs := []resolverCatalog{
		{Models: []resolverModel{
			{"anthropic", "claude-sonnet-4-5", "Claude Sonnet 4.5", true}, {"anthropic", "claude-sonnet-4-5-20250929", "Claude Sonnet 4.5 (dated)", true},
			{"anthropic", "claude-opus-4-8", "Claude Opus 4.8", true}, {"openai", "gpt-5.5", "GPT-5.5", true}, {"openai", "gpt-4o", "GPT-4o", false},
			{"openai", "gpt-4o-20240806", "GPT-4o 2024-08-06", false}, {"openrouter", "openai/gpt-4o:extended", "OpenAI GPT-4o extended", false},
			{"openrouter", "zai/glm-5", "GLM 5", false}, {"zai", "glm-5", "GLM 5", true}, {"zai", "glm-5-latest", "GLM 5 latest", true},
			{"local", "İstanbul", "İstanbul model", false}, {"local", "ǅ-model", "Dž", false}, {"local", "Straße-1", "Strasse", false}, {"local", "gpt-5.5", "dup id", false},
		}, Authed: []string{"openai", "zai"}},
		{Models: []resolverModel{{"zai", "glm-5", "GLM", true}, {"vercel-ai-gateway", "zai/glm-5", "GLM via gateway", false}, {"xiaomi", "mimo-v2.5-pro", "Mimo", true}, {"commandcode", "xiaomi/mimo-v2.5-pro", "Mimo CC", false}}, Authed: []string{"commandcode"}},
		{Models: []resolverModel{{"anthropic", "a", "A", false}, {"anthropic", "a-1", "A one", false}, {"openai", "a", "A two", false}}, Authed: []string{}},
		{Models: []resolverModel{}, Authed: []string{}},
	}
	r := rand.New(rand.NewPCG(3, 17))
	suffixes := []string{"", "", "", ":high", ":off", ":bogus", ":low:high", ":xhigh", ":", "::", ":extended", ":HIGH", ":max", ":minimal"}
	paddings := []string{"", "", "", " ", "\t", "\u00a0", "\ufeff", "\u0085", "\u2003", "\u180e"}
	mutate := func(s string) string {
		switch r.IntN(8) {
		case 0:
			return toUpperASCII(s)
		case 1:
			if len(s) > 3 {
				return s[1 : len(s)-1]
			}
		case 2:
			return s + "x"
		case 3:
			return "/" + s
		}
		return s
	}
	var probes []map[string]any
	for ci, catalog := range catalogs {
		var pool []string
		for _, m := range catalog.Models {
			pool = append(pool, m.ID, m.Provider+"/"+m.ID, m.Name, m.Provider, m.Provider+"/"+m.Name)
		}
		pool = append(pool, "", "nope", "/", "a/", "/a", "claude", "sonnet", "4.5", "gpt", "glm", "zai/glm-5", "openai/gpt-4o:extended", "i̇stanbul", "istanbul", "STRASSE", "strasse", "ǆ-model", "ǈ")
		// A pattern with a POSIX class and an escaped literal ("gpt-[[:digit:]]*") makes Pi throw a SyntaxError (the class sets the unicode flag, which
		// rejects the escaped hyphen); Pig reports no match there instead, so such patterns are not probed.
		globs := []string{"*", "*sonnet*", "anthropic/*", "*/gpt-*", "gpt-?.?", "[a-c]*", "*:high", "anthropic/*:high", "*GPT*", "openai/gpt-5.5", "?", "[", "a*b*", "{a,b}", "!a", "*\u00a0", "{claude,gpt}-*", "claude-{sonnet,opus}-4*", "!claude*", "**", "**/gpt*", "openai/**", "claude-[so]*", "claude-[!s]*", "claude-[^s]*", "claude-+(sonnet|opus)*", "@(zai|openai)/*", "?(anthropic/)claude-opus*", "*(claude-)opus*", "!(claude)*", "gpt-5.5", "*5.5", "*\\.5", "claude-sonnet-4-?", "{1..3}", "glm-{5,6}*", "*/glm-5", "openai/gpt-4o:*", "\\*", "#*", "claude-*-[0-9]-5", "[[:alpha:]]*", "gpt-4o*.", "./*"}
		for range 450 {
			pattern := paddings[r.IntN(len(paddings))] + mutate(pool[r.IntN(len(pool))]) + suffixes[r.IntN(len(suffixes))] + paddings[r.IntN(len(paddings))]
			probes = append(probes,
				map[string]any{"kind": "exact", "catalog": ci, "pattern": pattern},
				map[string]any{"kind": "parse", "catalog": ci, "pattern": pattern, "allowFallback": true},
				map[string]any{"kind": "parse", "catalog": ci, "pattern": pattern, "allowFallback": false},
			)
			provider, model := "", pattern
			switch r.IntN(4) {
			case 0:
				provider = catalog0Provider(catalog, r)
			case 1:
				provider = toUpperASCII(catalog0Provider(catalog, r))
			case 2:
				provider = []string{"nope", "Zai", " zai"}[r.IntN(3)]
			}
			thinking := []string{"", "", "", "high", "off", "low"}[r.IntN(6)]
			probes = append(probes, map[string]any{"kind": "cli", "catalog": ci, "provider": provider, "model": model, "thinking": thinking})
		}
		for range 120 {
			var patterns []string
			for range 1 + r.IntN(4) {
				if r.IntN(3) == 0 {
					patterns = append(patterns, globs[r.IntN(len(globs))]+suffixes[r.IntN(len(suffixes))])
				} else {
					patterns = append(patterns, paddings[r.IntN(len(paddings))]+mutate(pool[r.IntN(len(pool))])+suffixes[r.IntN(len(suffixes))])
				}
			}
			probes = append(probes, map[string]any{"kind": "scope", "catalog": ci, "patterns": patterns})
		}
	}
	input, err := json.Marshal(map[string]any{"catalogs": catalogs, "probes": probes})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/model_resolver.mjs", pigversion.UpstreamVersion)
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
	// Normalize through JSON so both sides compare as decoded values.
	normalize := func(v any) any {
		data, _ := json.Marshal(v)
		var out any
		_ = json.Unmarshal(data, &out)
		return out
	}
	failures := 0
	for i, probe := range probes {
		catalog := catalogs[probe["catalog"].(int)]
		runtime := resolverRuntime{catalog}
		models := runtime.GetModels("")
		var got map[string]any
		switch probe["kind"] {
		case "exact":
			got = map[string]any{"model": resolverKey(FindExactModelReferenceMatch(probe["pattern"].(string), models))}
		case "parse":
			parsed := ParseModelPattern(probe["pattern"].(string), models, probe["allowFallback"].(bool))
			got = map[string]any{"model": resolverKey(parsed.Model), "thinking": parsed.ThinkingLevel, "warning": parsed.Warning}
		case "scope":
			result := ResolveModelScopeFromModels(probe["patterns"].([]string), models)
			scoped := [][]any{}
			for _, s := range result.ScopedModels {
				model := s.Model
				scoped = append(scoped, []any{resolverKey(&model), s.ThinkingLevel})
			}
			diagnostics := [][]any{}
			for _, d := range result.Diagnostics {
				diagnostics = append(diagnostics, []any{d.Code, d.Message, d.Pattern})
			}
			got = map[string]any{"scoped": scoped, "diagnostics": diagnostics}
		case "cli":
			result := ResolveCliModel(probe["provider"].(string), probe["model"].(string), probe["thinking"].(string), runtime)
			got = map[string]any{"model": resolverKey(result.Model), "thinking": string(result.ThinkingLevel), "warning": result.Warning, "error": result.Error}
		}
		if !reflect.DeepEqual(normalize(got), normalize(want[i])) {
			if failures++; failures <= 10 {
				t.Errorf("probe %q:\n  Pig %v\n  Pi  %v", probe, normalize(got), want[i])
			}
		}
	}
	if failures > 10 {
		t.Errorf("%d of %d probes differ", failures, len(probes))
	}
}

func catalog0Provider(c resolverCatalog, r *rand.Rand) string {
	if len(c.Models) == 0 {
		return "zai"
	}
	return c.Models[r.IntN(len(c.Models))].Provider
}

func toUpperASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}
