package ai

// pi: packages/ai/src/api/openai-completions.ts (the compat objects of buildParams: 897-910, 979-980, 994-997), packages/ai/src/api/simple-options.ts
// resolveSamplingParams (37-47) and packages/coding-agent/src/core/provider-composer.ts mergeCompat (124-146).

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

func compatFromJSON(t *testing.T, text string) *OpenAICompat {
	t.Helper()
	compat := &OpenAICompat{}
	if err := json.Unmarshal([]byte(text), compat); err != nil {
		t.Fatal(err)
	}
	return compat
}

func compatJSON(t *testing.T, compat *OpenAICompat) string {
	t.Helper()
	encoded, err := json.Marshal(compat)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// legacyCompatJSON is OpenAICompat.MarshalJSON as it was before the compat objects kept their member order: every map written sorted.
func legacyCompatJSON(t *testing.T, compat OpenAICompat) string {
	t.Helper()
	type fields OpenAICompat
	var fallbacks *[]AnthropicAllowedFallbackModel
	if compat.AllowedFallbackModels != nil {
		fallbacks = &compat.AllowedFallbackModels
	}
	data, err := json.Marshal(struct {
		fields
		AllowedFallbackModels *[]AnthropicAllowedFallbackModel `json:"allowedFallbackModels,omitempty"`
	}{fields: fields(compat), AllowedFallbackModels: fallbacks})
	if err != nil {
		t.Fatal(err)
	}
	if data, err = orderedjson.AppendMembers(data, compat.Extra); err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Pi sends compat.openRouterRouting as the entry's own object (openai-completions.ts:979-980) and enumerates chatTemplateKwargs and
// chatTemplateArgs with Object.entries (:1033), so the order in which an entry lists the members of those objects, nested objects
// included, is on the wire. A compat decoded from JSON keeps it and writes it back; an entry that lists them sorted needs no record and
// is encoded as it always was.
func TestOpenAICompatKeepsObjectMemberOrder(t *testing.T) {
	for _, text := range []string{
		`{"openRouterRouting":{"order":["a","b"],"only":["a"],"max_price":{"prompt":1,"completion":2},"allow_fallbacks":false}}`,
		`{"openRouterRouting":{"allow_fallbacks":false,"max_price":{"prompt":1,"completion":2}}}`,
		`{"chatTemplateKwargs":{"preserve_thinking":true,"enable_thinking":{"$var":"thinking.enabled"},"budget":{"$var":"thinking.budget"},"a":1}}`,
		`{"chatTemplateArgs":{"thinking":{"$var":"thinking.enabled"},"effort":{"omitWhenOff":true,"$var":"thinking.effort"},"clear":false}}`,
		`{"supportsStore":false,"openRouterRouting":{"only":["a"],"order":["b"]},"chatTemplateKwargs":{"b":1,"a":2},"chatTemplateArgs":{"z":"x","y":null},"unmodeled":{"z":1,"a":2}}`,
	} {
		compat := compatFromJSON(t, text)
		if compat.objectOrder == nil {
			t.Errorf("%s: no member order was recorded", text)
		}
		if got := compatJSON(t, compat); got != text {
			t.Errorf("a compat decoded from\n  %s\nis encoded\n  %s", text, got)
		}
	}
	for _, text := range []string{
		`{"openRouterRouting":{"allow_fallbacks":false,"max_price":{"completion":2,"prompt":1},"only":["a"],"order":["a","b"]}}`,
		`{"chatTemplateKwargs":{"a":1,"enable_thinking":{"$var":"thinking.enabled"}},"chatTemplateArgs":{"clear":false,"thinking":{"$var":"thinking.enabled"}}}`,
		`{"supportsStore":false}`,
	} {
		compat := compatFromJSON(t, text)
		if compat.objectOrder != nil {
			t.Errorf("%s: sorted members were recorded: %v", text, compat.objectOrder)
		}
		if got, legacy := compatJSON(t, compat), legacyCompatJSON(t, *compat); got != text || got != legacy {
			t.Errorf("a compat decoded from\n  %s\nis encoded\n  %s\nand was encoded\n  %s", text, got, legacy)
		}
	}
}

// Every compat of the bundled catalog: its encoding survives a decode, a compat without one of the three objects is encoded byte for
// byte as before the objects kept their order, and a compat with one writes the object as the catalog entry spells it
// (GeneratedModel.Record is the entry's JSON in Pi's member order). gen-models writes the objects as Go map literals and carries no order
// beside them or beside SamplingParams, so this fails at the first catalog entry that lists such members in another order than sorted:
// the generator must then carry the order (the failure text says where); sorting the entry would hide a wire difference.
func TestBundledCatalogCompatObjectsRoundTrip(t *testing.T) {
	compats, withObjects, withSampling := 0, 0, 0
	for _, model := range generatedCatalog() {
		var entry struct {
			Compat         json.RawMessage `json:"compat"`
			SamplingParams json.RawMessage `json:"samplingParams"`
		}
		if err := json.Unmarshal([]byte(ResolveCatalogRecord(model.Record)), &entry); err != nil {
			t.Fatalf("%s/%s: record: %v", model.Provider, model.ID, err)
		}
		if orderedjson.IsObject(entry.SamplingParams) {
			withSampling++
			spelled, err := orderedjson.Parse(entry.SamplingParams)
			if err != nil {
				t.Fatal(err)
			}
			if keys := spelled.Keys(); !slices.IsSorted(keys) {
				t.Errorf("%s/%s: the catalog entry lists samplingParams as %v, and GeneratedModel has no order to send them in. Do not sort the entry: cmd/gen-models must write the order (a SamplingParamsOrder beside GeneratedModel.SamplingParams, copied where SamplingParams is)", model.Provider, model.ID, keys)
			}
		}
		if model.Compat == nil {
			continue
		}
		compats++
		encoded := compatJSON(t, model.Compat)
		if again := compatJSON(t, compatFromJSON(t, encoded)); again != encoded {
			t.Errorf("%s/%s: compat is encoded\n  %s\nand after a decode\n  %s", model.Provider, model.ID, encoded, again)
		}
		if model.Compat.OpenRouterRouting == nil && model.Compat.ChatTemplateKwargs == nil && model.Compat.ChatTemplateArgs == nil {
			if legacy := legacyCompatJSON(t, *model.Compat); encoded != legacy {
				t.Errorf("%s/%s: compat is encoded\n  %s\nand was encoded\n  %s", model.Provider, model.ID, encoded, legacy)
			}
			continue
		}
		withObjects++
		if !orderedjson.IsObject(entry.Compat) {
			t.Errorf("%s/%s: the catalog entry has no compat object: %s", model.Provider, model.ID, entry.Compat)
			continue
		}
		written, err := orderedjson.Parse([]byte(encoded))
		if err != nil {
			t.Fatal(err)
		}
		spelled, err := orderedjson.Parse(entry.Compat)
		if err != nil {
			t.Fatal(err)
		}
		for _, member := range compatObjectMembers {
			got, ok := written.Get(member)
			if !ok {
				continue
			}
			if want, _ := spelled.Get(member); string(got) != string(want) {
				t.Errorf("%s/%s: compat.%s is encoded %s; the catalog entry spells it %s. Do not sort the entry: cmd/gen-models compatLiteral must write the entry's member order into the literal (objectOrder, what readCompatObjectOrder returns for the entry's compat text)", model.Provider, model.ID, member, got, want)
			}
		}
	}
	if compats == 0 || withObjects == 0 {
		t.Fatalf("the catalog has %d compats, %d with one of %v: the round trip compared nothing", compats, withObjects, compatObjectMembers)
	}
	t.Logf("%d compats, %d with one of %v, %d entries with samplingParams", compats, withObjects, compatObjectMembers, withSampling)
}

// mergeCompat (provider-composer.ts:124-146) is `{ ...base, ...override }` with openRouterRouting, chatTemplateKwargs and chatTemplateArgs
// spread member by member (:141). A spread keeps base's members in base's order, an overridden one in its place with the override's
// value, and appends the override's new members in the override's order; a nested object such as max_price is one member's value and
// comes whole from the side that gives it.
func TestMergeOpenAICompatSpreadsObjectMembers(t *testing.T) {
	for _, tc := range []struct{ name, base, override, want string }{
		{"an overridden member keeps its place and new members follow in the override's order",
			`{"openRouterRouting":{"order":["a"],"only":["b"],"allow_fallbacks":true}}`,
			`{"openRouterRouting":{"zdr":true,"only":["c"],"data_collection":"deny"}}`,
			`{"openRouterRouting":{"order":["a"],"only":["c"],"allow_fallbacks":true,"zdr":true,"data_collection":"deny"}}`},
		{"an overridden nested object takes the override's members in the override's order",
			`{"openRouterRouting":{"sort":"price","max_price":{"prompt":1,"completion":2},"only":["b"]}}`,
			`{"openRouterRouting":{"max_price":{"request":3,"completion":4}}}`,
			`{"openRouterRouting":{"sort":"price","max_price":{"request":3,"completion":4},"only":["b"]}}`},
		{"a nested object the override leaves out keeps base's order",
			`{"openRouterRouting":{"sort":"price","max_price":{"prompt":1,"completion":2},"only":["b"]}}`,
			`{"openRouterRouting":{"only":["c"],"max_price_note":{"z":1,"a":2}}}`,
			`{"openRouterRouting":{"sort":"price","max_price":{"prompt":1,"completion":2},"only":["c"],"max_price_note":{"z":1,"a":2}}}`},
		{"two sorted objects merge into base's members then the override's",
			`{"chatTemplateKwargs":{"b":1}}`,
			`{"chatTemplateKwargs":{"a":2}}`,
			`{"chatTemplateKwargs":{"b":1,"a":2}}`},
		{"chatTemplateArgs",
			`{"chatTemplateArgs":{"thinking":{"$var":"thinking.enabled"},"clear":false}}`,
			`{"chatTemplateArgs":{"effort":{"$var":"thinking.effort"},"clear":true}}`,
			`{"chatTemplateArgs":{"thinking":{"$var":"thinking.enabled"},"clear":true,"effort":{"$var":"thinking.effort"}}}`},
		{"an override without the object keeps base's",
			`{"openRouterRouting":{"order":["a"],"only":["b"]}}`,
			`{"supportsStore":false}`,
			`{"supportsStore":false,"openRouterRouting":{"order":["a"],"only":["b"]}}`},
		{"a base without the object takes the override's",
			`{"supportsStore":false}`,
			`{"chatTemplateKwargs":{"b":1,"a":2}}`,
			`{"supportsStore":false,"chatTemplateKwargs":{"b":1,"a":2}}`},
	} {
		base, override := compatFromJSON(t, tc.base), compatFromJSON(t, tc.override)
		merged := MergeOpenAICompat(base, override)
		if got := compatJSON(t, merged); got != tc.want {
			t.Errorf("%s:\n  base     %s\n  override %s\n  merged   %s\n  Pi       %s", tc.name, tc.base, tc.override, got, tc.want)
		}
		if again := compatJSON(t, compatFromJSON(t, compatJSON(t, merged))); again != tc.want {
			t.Errorf("%s: the merged compat after a decode is %s, want %s", tc.name, again, tc.want)
		}
		if got := compatJSON(t, base); got != tc.base {
			t.Errorf("%s: the merge changed base to %s", tc.name, got)
		}
		if got := compatJSON(t, override); got != tc.override {
			t.Errorf("%s: the merge changed override to %s", tc.name, got)
		}
	}
	text := `{"openRouterRouting":{"order":["a"],"only":["b"]}}`
	if got := compatJSON(t, MergeOpenAICompat(compatFromJSON(t, text), nil)); got != text {
		t.Errorf("a compat merged with no override is %s, want %s", got, text)
	}
	if got := compatJSON(t, MergeOpenAICompat(nil, compatFromJSON(t, text))); got != text {
		t.Errorf("an override merged over no compat is %s, want %s", got, text)
	}
	if MergeOpenAICompat(nil, nil) != nil {
		t.Error("two absent compats merged into one")
	}
}

// resolveSamplingParams (simple-options.ts:44-46) is `{ ...model.samplingParams, ...thinkingLevelParams, ...requestParams }`: the model's
// members in the model's order, then the level's new ones, then the request's new ones. The maps carry no order, so each source is
// enumerated in the order recorded beside it; a source with no record is enumerated sorted, which is what every source was before the
// records existed, and a partial record puts its keys first.
func TestResolveSamplingParamsInOrder(t *testing.T) {
	model := &Model{
		SamplingParams:                SamplingParams{"top_p": 0.9, "min_p": 0.1, "repetition_penalty": 1.1},
		SamplingParamsByThinkingLevel: SamplingParamsByThinkingLevel{ThinkingOff: {"top_k": 40.0, "min_p": 0.2, "temperature": 0.5}},
	}
	request := SamplingParams{"seed": 1.0, "top_p": 0.8, "frequency_penalty": 0.0}
	keys, merged := resolveSamplingParamsInOrder(model, ThinkingOff, request, nil)
	if want := []string{"min_p", "repetition_penalty", "top_p", "temperature", "top_k", "frequency_penalty", "seed"}; !slices.Equal(keys, want) {
		t.Errorf("without a recorded order the keys are %v, want each source sorted: %v", keys, want)
	}
	if len(merged) != 7 || merged["top_p"] != 0.8 || merged["min_p"] != 0.2 {
		t.Errorf("merged = %v", merged)
	}
	model.SamplingParamsOrder = []string{"top_p", "min_p", "repetition_penalty"}
	model.SamplingParamsByThinkingLevelOrder = map[ModelThinkingLevel][]string{ThinkingOff: {"top_k", "min_p", "temperature"}}
	keys, _ = resolveSamplingParamsInOrder(model, ThinkingOff, request, []string{"seed", "top_p", "frequency_penalty"})
	if want := []string{"top_p", "min_p", "repetition_penalty", "top_k", "temperature", "seed", "frequency_penalty"}; !slices.Equal(keys, want) {
		t.Errorf("with recorded orders the keys are %v, want %v", keys, want)
	}
	model.SamplingParamsOrder, model.SamplingParamsByThinkingLevel = []string{"top_p", "absent", "top_p"}, nil
	keys, _ = resolveSamplingParamsInOrder(model, ThinkingOff, nil, nil)
	if want := []string{"top_p", "min_p", "repetition_penalty"}; !slices.Equal(keys, want) {
		t.Errorf("with a partial record the keys are %v, want %v", keys, want)
	}
}
