package closure

import (
	"fmt"
	"slices"
)

type providerWireTestBinding struct {
	id       string
	pin      *sourceRange
	fixtures []sourceRange
	factType string
	subject  string
	asserts  []providerWireAssertionBinding
}

type providerWireAssertionBinding struct {
	behavior string
	facet    string
	class    string
	oracle   string
}

func AddProviderWireAssertions(root string, snapshot *Snapshot, records []Record) ([]Record, error) {
	result := slices.Clone(records)
	pins := make(map[string]*Pin)
	for _, binding := range providerWireTestBindings() {
		var pinID, definitionHash string
		if binding.pin != nil {
			pin, err := makeSourceRangePin(root, snapshot, *binding.pin)
			if err != nil {
				return nil, fmt.Errorf("add provider-wire test %s: %w", binding.id, err)
			}
			pins[pin.ID] = pin
			pinID = pin.ID
			definitionHash = pin.BodyHash
		} else {
			factID, err := findDenominatorFacts(records, []string{binding.factType + "\x00" + binding.subject})
			if err != nil {
				return nil, fmt.Errorf("add provider-wire test %s: %w", binding.id, err)
			}
			fact := findFactByID(records, factID[0])
			if fact == nil || len(fact.PinIDs) != 1 {
				return nil, fmt.Errorf("add provider-wire test %s: scenario fact has no unique pin", binding.id)
			}
			pinID = fact.PinIDs[0]
			definitionHash = HashBytes(fact.Value)
		}
		fixturePinIDs, err := addSourceRanges(root, snapshot, pins, binding.fixtures)
		if err != nil {
			return nil, fmt.Errorf("add provider-wire test %s fixtures: %w", binding.id, err)
		}
		testID := "test:provider-wire:" + binding.id
		result = append(result, &Test{Kind: KindTest, ID: testID, SnapshotID: snapshot.ID, PinID: pinID, FixturePinIDs: fixturePinIDs, DefinitionHash: definitionHash})
		for _, assertion := range binding.asserts {
			result = append(result, &Assertion{
				Kind: KindAssertion, ID: "assertion:provider-wire:" + binding.id + ":" + assertion.behavior + ":" + assertion.facet,
				TestID: testID, Class: assertion.class, BehaviorID: "behavior:provider-wire:" + assertion.behavior,
				FacetID: "facet:" + assertion.facet, Oracle: assertion.oracle,
			})
		}
	}
	pinIDs := make([]string, 0, len(pins))
	for id := range pins {
		pinIDs = append(pinIDs, id)
	}
	slices.Sort(pinIDs)
	for _, id := range pinIDs {
		result = append(result, pins[id])
	}
	return result, nil
}

func providerWireTestBindings() []providerWireTestBinding {
	a2 := "authored-upstream-contract"
	a3 := "source-generated-differential"
	return []providerWireTestBinding{
		{
			id: "differential", factType: "scenario", subject: "parity/scenarios/providers-registry/04-provider-wire-payloads.toml",
			fixtures: []sourceRange{
				{repository: "pig", path: "parity/testdata/provider-wire-pi.mjs", semanticID: "provider-wire.pi-fixture", start: "#!/usr/bin/env node", end: ""},
				{repository: "pig", path: "parity/testdata/provider-wire-pig/main.go", semanticID: "provider-wire.pig-fixture", start: "package main", end: ""},
			},
			asserts: []providerWireAssertionBinding{
				{behavior: "sampling-precedence", facet: "order", class: "A3", oracle: a3},
				{behavior: "sampling-precedence", facet: "result", class: "A3", oracle: a3},
				{behavior: "sampling-precedence", facet: "wire", class: "A3", oracle: a3},
				{behavior: "baseten-chat-template", facet: "result", class: "A3", oracle: a3},
				{behavior: "baseten-chat-template", facet: "wire", class: "A3", oracle: a3},
				{behavior: "vllm-thinking-budget", facet: "result", class: "A3", oracle: a3},
				{behavior: "vllm-thinking-budget", facet: "wire", class: "A3", oracle: a3},
				{behavior: "finish-reason-errors", facet: "compat", class: "A3", oracle: a3},
				{behavior: "finish-reason-errors", facet: "error", class: "A3", oracle: a3},
				{behavior: "finish-reason-errors", facet: "result", class: "A3", oracle: a3},
			},
		},
		{
			id:  "sampling-completions",
			pin: &sourceRange{repository: "pig", path: "ai/openai_test.go", semanticID: "ai.TestOpenAICompletionsModelSamplingDefaultsMergeBeforeRequestOverrides", start: "func TestOpenAICompletionsModelSamplingDefaultsMergeBeforeRequestOverrides(", end: "func TestOpenAICompletionsBasetenAndVLLMThinkingPayload("},
			asserts: []providerWireAssertionBinding{
				{behavior: "sampling-precedence", facet: "order", class: "A2", oracle: a2},
				{behavior: "sampling-precedence", facet: "result", class: "A2", oracle: a2},
			},
		},
		{
			id:      "sampling-responses",
			pin:     &sourceRange{repository: "pig", path: "ai/openai_responses_test.go", semanticID: "ai.TestOpenAIResponsesSamplingParamsOverrideNamedFields", start: "func TestOpenAIResponsesSamplingParamsOverrideNamedFields(", end: "func TestOpenAIResponsesCanSuppressMaxOutputTokens("},
			asserts: []providerWireAssertionBinding{{behavior: "sampling-precedence", facet: "result", class: "A2", oracle: a2}},
		},
		{
			id:  "thinking-payload",
			pin: &sourceRange{repository: "pig", path: "ai/openai_test.go", semanticID: "ai.TestOpenAICompletionsBasetenAndVLLMThinkingPayload", start: "func TestOpenAICompletionsBasetenAndVLLMThinkingPayload(", end: "// reasoning is on and compat.supportsReasoningEffort"},
			asserts: []providerWireAssertionBinding{
				{behavior: "baseten-chat-template", facet: "result", class: "A2", oracle: a2},
				{behavior: "vllm-thinking-budget", facet: "result", class: "A2", oracle: a2},
			},
		},
		{
			id:  "finish-reason",
			pin: &sourceRange{repository: "pig", path: "ai/openai_test.go", semanticID: "ai.TestMapOAIFinishReasonCompatibility", start: "func TestMapOAIFinishReasonCompatibility(", end: "func captureOpenAIRequestMap("},
			asserts: []providerWireAssertionBinding{
				{behavior: "finish-reason-errors", facet: "compat", class: "A2", oracle: a2},
				{behavior: "finish-reason-errors", facet: "error", class: "A2", oracle: a2},
				{behavior: "finish-reason-errors", facet: "result", class: "A2", oracle: a2},
			},
		},
		{
			id:       "nullable-headers-differential",
			pin:      &sourceRange{repository: "pig", path: "internal/codingagent/model_registry_parity_test.go", semanticID: "codingagent.TestMergeHeadersMatchesPinnedPiSource", start: "func TestMergeHeadersMatchesPinnedPiSource(", end: ""},
			fixtures: []sourceRange{{repository: "pig", path: "parity/testdata/provider-headers-pi.mjs", semanticID: "provider-headers.pi-fixture", start: "#!/usr/bin/env node", end: ""}},
			asserts: []providerWireAssertionBinding{
				{behavior: "nullable-header-deletion", facet: "compat", class: "A3", oracle: a3},
				{behavior: "nullable-header-deletion", facet: "wire", class: "A3", oracle: a3},
			},
		},
		{
			id:  "nullable-headers",
			pin: &sourceRange{repository: "pig", path: "internal/codingagent/model_registry_test.go", semanticID: "codingagent.TestMergeHeadersSupportsCaseInsensitiveDeletionMarkers", start: "func TestMergeHeadersSupportsCaseInsensitiveDeletionMarkers(", end: "func TestModelRegistrySamplingParamsMergePerKey("},
			asserts: []providerWireAssertionBinding{
				{behavior: "nullable-header-deletion", facet: "compat", class: "A2", oracle: a2},
				{behavior: "nullable-header-deletion", facet: "result", class: "A2", oracle: a2},
			},
		},
	}
}

func findFactByID(records []Record, id string) *Fact {
	for _, record := range records {
		fact, ok := record.(*Fact)
		if ok && fact.ID == id {
			return fact
		}
	}
	return nil
}
