package ai

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// piLiteralList returns the string literals of `export const <name> = [...]` or the Type.Literal members of `function <name>(...)` in a Pi
// ai/src/providers schema module, in source order, with the spread of an earlier list expanded.
func piLiteralList(t *testing.T, file, name string) []string {
	t.Helper()
	raw, err := os.ReadFile("../.upstream/current/packages/ai/src/providers/" + file)
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	var body string
	if m := regexp.MustCompile(`(?s)export const ` + name + ` = \[(.*?)\] as const;`).FindStringSubmatch(src); m != nil {
		body = m[1]
	} else if m := regexp.MustCompile(`(?s)function ` + name + `\(.*?\{\s*return Type\.Union\(\[(.*?)\]`).FindStringSubmatch(src); m != nil {
		body = m[1]
	} else {
		t.Fatalf("%s: no list named %s", file, name)
	}
	var out []string
	for item := range strings.SplitSeq(body, ",") {
		item = strings.TrimSpace(item)
		switch {
		case strings.HasPrefix(item, "..."):
			out = append(out, piLiteralList(t, file, strings.TrimPrefix(item, "..."))...)
		case strings.HasPrefix(item, `"`):
			out = append(out, strings.Trim(item, `"`))
		case strings.HasPrefix(item, "Type.Literal("):
			out = append(out, strings.Trim(strings.TrimSuffix(strings.TrimPrefix(item, "Type.Literal("), ")"), `"`))
		}
	}
	return out
}

func strs[T ~string](values ...T) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = string(v)
	}
	return out
}

// Pi model-schema.ts:3 THINKING_LEVELS is the closed set of the ThinkingLevel union, in order.
func TestThinkingLevelVariantsMatchPi(t *testing.T) {
	got := strs[ThinkingLevel](ThinkingLevelMinimal, ThinkingLevelLow, ThinkingLevelMedium, ThinkingLevelHigh, ThinkingLevelXHigh, ThinkingLevelMax)
	if want := piLiteralList(t, "model-schema.ts", "THINKING_LEVELS"); !slices.Equal(got, want) {
		t.Fatalf("ThinkingLevel variants %v, Pi %v", got, want)
	}
}

// Pi model-schema.ts:4 MODEL_THINKING_LEVELS is "off" followed by every ThinkingLevel.
func TestModelThinkingLevelVariantsMatchPi(t *testing.T) {
	got := strs[ModelThinkingLevel](ThinkingOff, ThinkingMinimal, ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingXHigh, ThinkingMax)
	if want := piLiteralList(t, "model-schema.ts", "MODEL_THINKING_LEVELS"); !slices.Equal(got, want) {
		t.Fatalf("ModelThinkingLevel variants %v, Pi %v", got, want)
	}
}

// Pi model-schema.ts:6 CACHE_RETENTIONS is the closed set of the CacheRetention union.
func TestCacheRetentionVariantsMatchPi(t *testing.T) {
	got := strs[CacheRetention](CacheRetentionNone, CacheRetentionShort, CacheRetentionLong)
	if want := piLiteralList(t, "model-schema.ts", "CACHE_RETENTIONS"); !slices.Equal(got, want) {
		t.Fatalf("CacheRetention variants %v, Pi %v", got, want)
	}
}

// Pi model-schema.ts:5 MODEL_INPUT_MODALITIES is the closed set of the ModelInputModality union.
func TestModelInputModalityVariantsMatchPi(t *testing.T) {
	got := []ModelInputModality{ModelInputText, ModelInputImage}
	if want := piLiteralList(t, "model-schema.ts", "MODEL_INPUT_MODALITIES"); !slices.Equal(got, want) {
		t.Fatalf("ModelInputModality variants %v, Pi %v", got, want)
	}
}

// Pi compat-schema.ts:4 sessionAffinityFormat is the closed set of the SessionAffinityFormat union.
func TestSessionAffinityFormatVariantsMatchPi(t *testing.T) {
	got := strs[SessionAffinityFormat](SessionAffinityOpenAI, SessionAffinityOpenAINoSession, SessionAffinityOpenRouter)
	if want := piLiteralList(t, "compat-schema.ts", "sessionAffinityFormat"); !slices.Equal(got, want) {
		t.Fatalf("SessionAffinityFormat variants %v, Pi %v", got, want)
	}
}

// Pi compat-schema.ts:24 ChatTemplateKwargValueSchema: a string, number, boolean or null, or a `{ $var, omitWhenOff? }` placeholder. Every kind
// decodes from models.json compat into the value the request resolver reads. This checks the decode only: Pig's `any` accepts any object where Pi's
// schema allows only the three `$var` literals, and the payload test TestChatTemplateKwargValueKindsReachThePayload covers the resolver.
func TestChatTemplateKwargValueAllKindsDecodeFromCompat(t *testing.T) {
	var compat OpenAICompat
	const doc = `{"chatTemplateKwargs":{"text":"x","number":1.5,"flag":true,"nothing":null,"placeholder":{"$var":"thinking.budget","omitWhenOff":true}}}`
	if err := json.Unmarshal([]byte(doc), &compat); err != nil {
		t.Fatal(err)
	}
	var kwargs map[string]ChatTemplateKwargValue = compat.ChatTemplateKwargs
	if kwargs["text"] != "x" || kwargs["number"] != 1.5 || kwargs["flag"] != true {
		t.Fatalf("scalar kinds decoded as %#v", kwargs)
	}
	if value, present := kwargs["nothing"]; !present || value != nil {
		t.Fatalf("null must stay a present nil, got %#v present=%v", value, present)
	}
	if placeholder, ok := kwargs["placeholder"].(map[string]any); !ok || placeholder["$var"] != "thinking.budget" || placeholder["omitWhenOff"] != true {
		t.Fatalf("placeholder decoded as %#v", kwargs["placeholder"])
	}
}
