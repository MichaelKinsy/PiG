//go:build !pig_strip_bedrock_converse_stream

package ai

// pi: packages/ai/src/api/anthropic-messages.ts
// pi: packages/ai/src/api/bedrock-converse-stream.ts
// pi: packages/ai/src/providers/opencode-headers.ts
// pi: packages/ai/src/api/openai-completions.ts

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type lowerSiteProbe struct {
	Op       string   `json:"op"`
	Name     string   `json:"name"`
	Names    []string `json:"names,omitempty"`
	Provider string   `json:"provider,omitempty"`
}

// lowerSiteProbes spell tool names, header names and base URLs with the characters whose case mapping differs between Go and JavaScript: U+0130 (JS keeps "i" plus a combining dot), U+212A KELVIN SIGN (both lower it to
// "k"), U+017F LONG S (simple case folding equates it with "s", toLowerCase does not) and an upper-case sigma.
func lowerSiteProbes() []lowerSiteProbe {
	random := rand.New(rand.NewSource(20260955))
	bases := []string{"Read", "Write", "Edit", "Bash", "Grep", "Glob", "AskUserQuestion", "EnterPlanMode", "KillShell", "NotebookEdit", "Skill", "Task", "TodoWrite", "WebFetch", "WebSearch",
		"host", "Host", "authorization", "Authorization", "x-amz-date", "X-Amz-Security-Token", "x-opencode-session", "X-OpenCode-Session", "https://api.deepseek.com/v1", "https://API.DeepSeek.COM/v1", "https://x.example/deepseek.com", "deepseek"}
	special := []string{"\u0130", "\u212a", "\u017f", "\u03a3", "\u0131", "\u00cf", "I", "i", "S", "s", "K", "k"}
	lookalikes := map[rune]rune{'i': '\u0130', 's': '\u017f', 'k': '\u212a'}
	spell := func(base string) string {
		runes := []rune(base)
		for range random.Intn(4) {
			if len(runes) == 0 {
				break
			}
			at := random.Intn(len(runes))
			switch random.Intn(4) {
			case 3:
				for _, candidate := range random.Perm(len(runes)) {
					if replacement, ok := lookalikes[runes[candidate]|0x20]; ok {
						runes[candidate] = replacement
						break
					}
				}
			case 0:
				runes[at], _ = utf8.DecodeRuneInString(special[random.Intn(len(special))])
			case 1:
				if runes[at] >= 'a' && runes[at] <= 'z' {
					runes[at] -= 'a' - 'A'
				} else if runes[at] >= 'A' && runes[at] <= 'Z' {
					runes[at] += 'a' - 'A'
				}
			default:
				runes = append(runes[:at], append([]rune(special[random.Intn(len(special))]), runes[at:]...)...)
			}
		}
		return string(runes)
	}
	var probes []lowerSiteProbe
	for range 1500 {
		base := bases[random.Intn(len(bases))]
		name := spell(base)
		names := []string{spell(base), spell(bases[random.Intn(len(bases))]), spell(base)}
		probes = append(probes,
			lowerSiteProbe{Op: "toCC", Name: name},
			lowerSiteProbe{Op: "fromCC", Name: name, Names: names[:1+random.Intn(3)]},
			lowerSiteProbe{Op: "reserved", Name: name},
			lowerSiteProbe{Op: "hasHeader", Name: name, Names: names[:1+random.Intn(3)]},
			lowerSiteProbe{Op: "deepseek", Name: name, Provider: []string{"", "deepseek", "openai"}[random.Intn(3)]})
	}
	return probes
}

// The private case-insensitive helpers cut from Pi run in Node on the same spellings: each compares with toLowerCase, which Go's ToLower and EqualFold map differently for U+0130 and U+017F.
func TestJSLowerCaseSitesMatchPi(t *testing.T) {
	probes := lowerSiteProbes()
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "ai", "src")
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/lower_sites.mjs",
		filepath.Join(src, "api", "anthropic-messages.ts"), filepath.Join(src, "api", "bedrock-converse-stream.ts"), filepath.Join(src, "providers", "opencode-headers.ts"), filepath.Join(src, "api", "openai-completions.ts"))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []json.RawMessage
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("Pi answered %d of %d probes", len(expected), len(probes))
	}
	differing, positives := 0, map[string]int{}
	for i, probe := range probes {
		var got any
		switch probe.Op {
		case "toCC":
			got = toClaudeCodeName(probe.Name)
		case "fromCC":
			tools := make([]ToolSchema, len(probe.Names))
			for j, name := range probe.Names {
				tools[j] = ToolSchema{Name: name}
			}
			got = fromClaudeCodeName(probe.Name, tools)
		case "reserved":
			got = isReservedBedrockHeader(probe.Name)
		case "hasHeader":
			headers := ProviderHeaders{}
			for _, name := range probe.Names {
				headers[name] = new("v")
			}
			got = hasProviderHeader(headers, probe.Name)
		default:
			compat := detectOpenAICompletionsCompat(probe.Provider, probe.Name, "m")
			got = compat.RequiresReasoningContentOnAssistantMessages != nil && *compat.RequiresReasoningContentOnAssistantMessages
		}
		want := reflect.New(reflect.TypeOf(got)).Interface()
		if err := json.Unmarshal(expected[i], want); err != nil {
			t.Fatal(err)
		}
		wantValue := reflect.ValueOf(want).Elem().Interface()
		if !reflect.DeepEqual(got, wantValue) {
			if differing++; differing <= 12 {
				t.Errorf("%s %q %q: Pig %v, Pi %v", probe.Op, probe.Name, probe.Names, got, wantValue)
			}
		}
		if text := string(expected[i]); text == "true" || (probe.Op == "toCC" || probe.Op == "fromCC") && text != string(lowerSiteJSON(probe.Name)) {
			positives[probe.Op]++
		}
	}
	if differing > 12 {
		t.Errorf("%d of %d probes differ from Pi", differing, len(probes))
	}
	for _, op := range []string{"toCC", "fromCC", "reserved", "hasHeader", "deepseek"} {
		if positives[op] < 20 {
			t.Errorf("%s probes are one-sided: %d positive answers", op, positives[op])
		}
	}
}

func lowerSiteJSON(v any) []byte {
	out, _ := json.Marshal(v)
	return out
}

// anthropic-messages.ts:1090 compares name.toLowerCase() with "anthropic-beta": "anthropİc-beta" lowercases to "anthropi̇c-beta" in JavaScript (Go's ToLower and EqualFold fold it to "i"), so it is not the beta header and
// the computed list stays; "ANTHROPIC-BETA" is.
func TestAnthropicBetaHeaderNameMatchesWithJSToLowerCase(t *testing.T) {
	inputs := anthropicBetaInputs{interleavedThinking: true, hasFallbacks: true}
	computed := getBetaFeatures(anthropicHeaders{}, anthropicHeaders{}, inputs)
	if len(computed) == 0 {
		t.Fatal("expected computed beta features without a configured header")
	}
	for name, want := range map[string][]string{"anthropİc-beta": computed, "ANTHROPIC-BETA": {"only"}, "Anthropic-Beta": {"only"}} {
		got := getBetaFeatures(anthropicHeaders{}, anthropicHeadersFromProviderHeaders(ProviderHeaders{name: new("only")}), inputs)
		if !slices.Equal(got, want) {
			t.Errorf("header %q: features %q, want %q", name, got, want)
		}
	}
}
