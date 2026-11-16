package catalogfixture

import (
	"encoding/json"
	"slices"
	"testing"
)

// shippedPaths are the members, nested members and null values that the shipped catalog's entries set on 2026-11-16 (a header map counts as Headers.*), as the JSON of a GeneratedModel spells them. The fixture sets every one of them, so the benchmarks that copy it exercise every field the shipped records exercised.
var shippedPaths = []string{
	"API",
	"BaseURL",
	"CacheReadCost",
	"CacheWriteCost",
	"Capabilities",
	"Capabilities[]",
	"Compat",
	"Compat.allowEmptySignature",
	"Compat.allowedFallbackModels",
	"Compat.allowedFallbackModels[]",
	"Compat.allowedFallbackModels[].cost",
	"Compat.allowedFallbackModels[].cost.cacheRead",
	"Compat.allowedFallbackModels[].cost.cacheWrite",
	"Compat.allowedFallbackModels[].cost.input",
	"Compat.allowedFallbackModels[].cost.output",
	"Compat.allowedFallbackModels[].model",
	"Compat.allowedFallbackModels[].provider",
	"Compat.cacheControlFormat",
	"Compat.chatTemplateArgs",
	"Compat.chatTemplateArgs.enable_thinking",
	"Compat.chatTemplateArgs.enable_thinking.$var",
	"Compat.forceAdaptiveThinking",
	"Compat.maxTokensField",
	"Compat.requiresReasoningContentOnAssistantMessages",
	"Compat.sendSessionAffinityHeaders",
	"Compat.sessionAffinityFormat",
	"Compat.supportsAdditionalTools",
	"Compat.supportsCacheControlOnTools",
	"Compat.supportsDeveloperRole",
	"Compat.supportsEagerToolInputStreaming",
	"Compat.supportsExplicitPromptCacheMode",
	"Compat.supportsLongCacheRetention",
	"Compat.supportsMidConvoEffort",
	"Compat.supportsMidConvoSystemMessages",
	"Compat.supportsMidConvoToolAdditions",
	"Compat.supportsMidConvoToolChanges",
	"Compat.supportsOpenAIGrammarTools",
	"Compat.supportsReasoningEffort",
	"Compat.supportsStore",
	"Compat.supportsStrictMode",
	"Compat.supportsStrictTools",
	"Compat.supportsTemperature",
	"Compat.supportsToolSearch",
	"Compat.supportsUsageInStreaming",
	"Compat.thinkingFormat",
	"Compat.zaiToolStream",
	"Compat=null",
	"ContextWindow",
	"DisplayName",
	"Enabled",
	"Enabled=null",
	"Headers",
	"Headers.*",
	"Headers=null",
	"ID",
	"InputCostPerMTokens",
	"InputLimits",
	"InputLimits.images",
	"InputLimits.images.maxPerMessage",
	"InputLimits.images.maxPerRequest",
	"InputLimits.images.resize",
	"InputLimits.images.resize.jpegQuality",
	"InputLimits.images.resize.maxBytes",
	"InputLimits.images.resize.maxHeight",
	"InputLimits.images.resize.maxWidth",
	"InputLimits.maxRequestBytes",
	"InputLimits=null",
	"Lab",
	"MaxOutputTokens",
	"OutputCostPerMTokens",
	"PromptCache",
	"PromptCache.long",
	"PromptCache.short",
	"PromptCache=null",
	"Provider",
	"Providers",
	"Providers=null",
	"Providers[]",
	"Providers[].credential",
	"Providers[].id",
	"Providers[].name",
	"Providers[].source",
	"Reasoning",
	"SamplingParams",
	"SamplingParams=null",
	"ThinkingLevelMap",
	"ThinkingLevelMap.high",
	"ThinkingLevelMap.high=null",
	"ThinkingLevelMap.low",
	"ThinkingLevelMap.low=null",
	"ThinkingLevelMap.max",
	"ThinkingLevelMap.max=null",
	"ThinkingLevelMap.medium",
	"ThinkingLevelMap.medium=null",
	"ThinkingLevelMap.minimal",
	"ThinkingLevelMap.minimal=null",
	"ThinkingLevelMap.off",
	"ThinkingLevelMap.off=null",
	"ThinkingLevelMap.xhigh",
	"ThinkingLevelMap.xhigh=null",
	"ThinkingLevelMap=null",
	"Tiers",
	"Tiers=null",
	"Tiers[]",
	"Tiers[].cacheRead",
	"Tiers[].cacheWrite",
	"Tiers[].input",
	"Tiers[].inputTokensAbove",
	"Tiers[].output",
}

func collectPaths(value any, path string, into map[string]bool) {
	switch typed := value.(type) {
	case map[string]any:
		for name, member := range typed {
			if path == "Headers" {
				into["Headers.*"] = true
				continue
			}
			child := name
			if path != "" {
				child = path + "." + name
			}
			into[child] = true
			collectPaths(member, child, into)
		}
	case []any:
		into[path+"[]"] = true
		for _, element := range typed {
			collectPaths(element, path+"[]", into)
		}
	case nil:
		into[path+"=null"] = true
	}
}

func TestFixtureSetsEveryFieldTheShippedCatalogSet(t *testing.T) {
	have := map[string]bool{}
	for _, row := range GeneratedModels() {
		data, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		var tree map[string]any
		if err := json.Unmarshal(data, &tree); err != nil {
			t.Fatal(err)
		}
		delete(tree, "Record")
		collectPaths(tree, "", have)
	}
	for _, want := range shippedPaths {
		if !have[want] {
			t.Errorf("no fixture entry sets %s", want)
		}
	}
	if !slices.IsSorted(shippedPaths) {
		t.Error("shippedPaths is not sorted")
	}
}

func TestFixtureHasTheShippedSizeAndDistinctEntries(t *testing.T) {
	rows := GeneratedModels()
	if len(rows) != LiveEntries || len(Models()) != LiveEntries || len(Compats()) != LiveEntries {
		t.Fatalf("fixture has %d rows, %d models, %d compats, want %d", len(rows), len(Models()), len(Compats()), LiveEntries)
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if seen[row.Provider+"/"+row.ID] {
			t.Fatalf("%s/%s appears twice", row.Provider, row.ID)
		}
		seen[row.Provider+"/"+row.ID] = true
	}
}

// Each entry owns its maps, lists and pointers, as entries decoded one by one do.
func TestFixtureEntriesShareNothing(t *testing.T) {
	first, second := GeneratedModels(), GeneratedModels()
	for i := range first {
		if first[i].Compat != nil && first[i].Compat == second[i].Compat {
			t.Fatalf("entry %d shares its compat", i)
		}
		if first[i].InputLimits != nil && first[i].InputLimits == second[i].InputLimits {
			t.Fatalf("entry %d shares its input limits", i)
		}
	}
	if len(first) > 1 && first[0].Compat != nil && first[0].Compat == first[1].Compat {
		t.Fatal("two entries of one template share their compat")
	}
}
