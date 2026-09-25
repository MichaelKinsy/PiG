package closure

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type providerWireBehavior struct {
	id             string
	name           string
	facets         []string
	upstreamFacts  []string
	upstreamRanges []sourceRange
	targets        []providerWireTarget
}

type providerWireTarget struct {
	id       string
	symbol   string
	goFact   string
	rangeRef sourceRange
	rootRef  sourceRange
}

type sourceRange struct {
	repository string
	path       string
	semanticID string
	start      string
	end        string
}

func AddProviderWireBehaviors(root string, snapshot *Snapshot, records []Record) ([]Record, error) {
	if snapshot == nil {
		return nil, fmt.Errorf("add provider-wire behaviors: snapshot is nil")
	}
	definitions := providerWireDefinitions()
	result := slices.Clone(records)
	facets := make(map[string]string)
	for _, definition := range definitions {
		for _, facet := range definition.facets {
			facets[facet] = "facet:" + facet
		}
	}
	facetNames := make([]string, 0, len(facets))
	for name := range facets {
		facetNames = append(facetNames, name)
	}
	slices.Sort(facetNames)
	for _, name := range facetNames {
		result = append(result,
			&Facet{Kind: KindFacet, ID: facets[name], Name: name},
			&Rule{Kind: KindRule, ID: "rule:provider-wire:" + name, Name: "provider-wire " + name, DefinitionHash: HashBytes([]byte(providerWireRule(name)))},
		)
	}

	pins := make(map[string]*Pin)
	targets := make(map[string]struct{})
	for _, definition := range definitions {
		behaviorPinIDs, err := addSourceRanges(root, snapshot, pins, definition.upstreamRanges)
		if err != nil {
			return nil, err
		}
		factIDs, err := findDenominatorFacts(records, definition.upstreamFacts)
		if err != nil {
			return nil, fmt.Errorf("add %s: %w", definition.id, err)
		}
		behaviorID := "behavior:provider-wire:" + definition.id
		result = append(result, &Behavior{
			Kind: KindBehavior, ID: behaviorID, Name: definition.name, OriginPinIDs: behaviorPinIDs,
			FactIDs: factIDs, Profile: "application",
		})

		targetIDs := make([]string, 0, len(definition.targets))
		for _, targetDefinition := range definition.targets {
			targetPinIDs, err := addSourceRanges(root, snapshot, pins, []sourceRange{targetDefinition.rangeRef})
			if err != nil {
				return nil, err
			}
			rootPinIDs, err := addSourceRanges(root, snapshot, pins, []sourceRange{targetDefinition.rootRef})
			if err != nil {
				return nil, err
			}
			targetFactIDs, err := findDenominatorFacts(records, []string{"go-interface\x00" + targetDefinition.goFact})
			if err != nil {
				return nil, fmt.Errorf("add target %s: %w", targetDefinition.id, err)
			}
			targetID := "target:provider-wire:" + targetDefinition.id
			targetIDs = append(targetIDs, targetID)
			if _, exists := targets[targetID]; !exists {
				targets[targetID] = struct{}{}
				result = append(result, &Target{Kind: KindTarget, ID: targetID, SnapshotID: snapshot.ID, PinIDs: targetPinIDs, FactIDs: targetFactIDs, Language: "go", Symbol: targetDefinition.symbol})
			}
			result = append(result, &Reachability{
				Kind: KindReachability, ID: "reachability:provider-wire:" + definition.id + ":" + targetDefinition.id,
				BehaviorID: behaviorID, TargetID: targetID, Class: "prod-reachable", Method: "production-call-path", RootPinIDs: rootPinIDs,
			})
		}
		slices.Sort(targetIDs)
		mapping := &Mapping{
			Kind: KindMapping, ID: "mapping:provider-wire:" + definition.id, BehaviorID: behaviorID,
			TargetIDs: targetIDs, Status: "hypothesis",
		}
		decisionID := "decision:provider-wire:" + definition.id
		if decisionCoversBehavior(records, decisionID, behaviorID) {
			mapping.Status = "decided"
			mapping.DecisionID = decisionID
		}
		result = append(result, mapping)
		for _, facet := range definition.facets {
			result = append(result, &Obligation{
				Kind: KindObligation, ID: "obligation:provider-wire:" + definition.id + ":" + facet,
				BehaviorID: behaviorID, FacetID: facets[facet], RuleID: "rule:provider-wire:" + facet,
				OriginPinIDs: behaviorPinIDs,
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

func providerWireDefinitions() []providerWireBehavior {
	openAIRoot := sourceRange{repository: "pig", path: "cmd/pig/model.go", semanticID: "cmd.pig.buildModel.openai", start: "provider = ai.NewOpenAIProvider(ai.OpenAIConfig{", end: "\n\t\t\t})"}
	openAISampling := providerWireTarget{
		id: "openai-completions-sampling", symbol: "ai.openAIProvider.Stream.sampling", goFact: "go:github.com/MichaelKinsy/PiG/ai#openAIProvider.Stream",
		rangeRef: sourceRange{repository: "pig", path: "ai/openai.go", semanticID: "ai.openAIProvider.Stream.sampling", start: "\tpayload := any(req)", end: "\tif opts.OnPayload != nil"},
		rootRef:  openAIRoot,
	}
	basetenTarget := providerWireTarget{
		id: "openai-baseten-chat-template", symbol: "ai.openAIProvider.Stream.baseten", goFact: "go:github.com/MichaelKinsy/PiG/ai#openAIProvider.Stream",
		rangeRef: sourceRange{repository: "pig", path: "ai/openai.go", semanticID: "ai.openAIProvider.Stream.baseten", start: "\t\tcase \"baseten\":", end: "\t\tcase \"deepseek\":"},
		rootRef:  openAIRoot,
	}
	vllmTarget := providerWireTarget{
		id: "openai-vllm-thinking-budget", symbol: "ai.openAIProvider.Stream.thinkingTokenBudget", goFact: "go:github.com/MichaelKinsy/PiG/ai#openAIProvider.Stream",
		rangeRef: sourceRange{repository: "pig", path: "ai/openai.go", semanticID: "ai.openAIProvider.Stream.thinkingTokenBudget", start: "\t\tif reasoningOn && p.compatBool(func(c *OpenAICompat) *bool { return c.SupportsThinkingTokenBudget }, false) {", end: "\t// OpenRouter provider routing preferences."},
		rootRef:  openAIRoot,
	}
	responsesStream := providerWireTarget{
		id: "openai-responses-sampling", symbol: "ai.openAIResponsesProvider.Stream.sampling", goFact: "go:github.com/MichaelKinsy/PiG/ai#openAIResponsesProvider.Stream",
		rangeRef: sourceRange{repository: "pig", path: "ai/openai_responses.go", semanticID: "ai.openAIResponsesProvider.Stream.sampling", start: "\tpayload := any(req)", end: "\tif opts.OnPayload != nil"},
		rootRef:  sourceRange{repository: "pig", path: "cmd/pig/model.go", semanticID: "cmd.pig.buildModel.openai-responses", start: "provider = ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{", end: "\n\t\t\t})"},
	}
	return []providerWireBehavior{
		{
			id: "sampling-precedence", name: "model sampling defaults merge before request overrides", facets: []string{"order", "result", "wire"},
			upstreamFacts: []string{
				"semantic-interface\x00pkg:ai/api/openai-completions#OpenAICompletionsOptions::property:samplingParams",
				"semantic-interface\x00pkg:ai/api/openai-responses#OpenAIResponsesOptions::property:samplingParams",
			},
			upstreamRanges: []sourceRange{
				{repository: "upstream", path: "packages/ai/src/api/openai-completions.ts", semanticID: "openai-completions.buildParams", start: "function buildParams(", end: "function buildChatTemplateValues("},
				{repository: "upstream", path: "packages/ai/src/api/openai-responses.ts", semanticID: "openai-responses.buildParams", start: "function buildParams(", end: "function getServiceTierCostMultiplier("},
			},
			targets: []providerWireTarget{openAISampling, responsesStream},
		},
		{
			id: "nullable-header-deletion", name: "nullable inherited headers delete case-insensitively", facets: []string{"compat", "result", "wire"},
			upstreamFacts: []string{
				"semantic-interface\x00pkg:ai/.#ProviderHeaders",
				"semantic-interface\x00pkg:ai/compat#ProviderHeaders",
			},
			upstreamRanges: []sourceRange{
				{repository: "upstream", path: "packages/coding-agent/src/core/model-runtime.ts", semanticID: "model-runtime.mergeHeaders", start: "function mergeHeaders(", end: "/** Configured pi-ai Models collection"},
				{repository: "upstream", path: "packages/ai/src/utils/headers.ts", semanticID: "headers.providerHeadersToRecord", start: "export function providerHeadersToRecord(", end: ""},
			},
			targets: []providerWireTarget{{
				id: "model-registry-merge-headers", symbol: "codingagent.mergeHeadersOrdered", goFact: "go:github.com/MichaelKinsy/PiG/internal/codingagent#mergeHeadersOrdered",
				rangeRef: sourceRange{repository: "pig", path: "internal/codingagent/model_registry.go", semanticID: "codingagent.mergeHeadersOrdered", start: "func mergeHeadersOrdered(", end: "// mergeCompat merges"},
				rootRef:  sourceRange{repository: "pig", path: "internal/codingagent/model_registry.go", semanticID: "codingagent.composeRequestHeadersLocked", start: "func (r *ModelRegistry) composeRequestHeadersLocked(", end: "func generatedModelEntry("},
			}},
		},
		{
			id: "baseten-chat-template", name: "Baseten chat template arguments resolve from thinking state", facets: []string{"result", "wire"},
			upstreamFacts:  []string{"semantic-interface\x00pkg:ai/api/openai-completions#streamSimple"},
			upstreamRanges: []sourceRange{{repository: "upstream", path: "packages/ai/src/api/openai-completions.ts", semanticID: "openai-completions.buildChatTemplateValues", start: "function buildChatTemplateValues(", end: "function resolveChatTemplateKwargValue("}},
			targets:        []providerWireTarget{basetenTarget},
		},
		{
			id: "vllm-thinking-budget", name: "vLLM thinking token budget respects level and output ceiling", facets: []string{"result", "wire"},
			upstreamFacts:  []string{"semantic-interface\x00pkg:ai/api/openai-completions#streamSimple"},
			upstreamRanges: []sourceRange{{repository: "upstream", path: "packages/ai/src/api/openai-completions.ts", semanticID: "openai-completions.buildParams.thinkingTokenBudget", start: "function buildParams(", end: "function buildChatTemplateValues("}},
			targets:        []providerWireTarget{vllmTarget},
		},
		{
			id: "finish-reason-errors", name: "unknown provider finish reasons remain errors", facets: []string{"compat", "error", "result"},
			upstreamFacts:  []string{"semantic-interface\x00pkg:ai/api/openai-completions#stream"},
			upstreamRanges: []sourceRange{{repository: "upstream", path: "packages/ai/src/api/openai-completions.ts", semanticID: "openai-completions.mapStopReason", start: "function mapStopReason(", end: "function detectCompat("}},
			targets: []providerWireTarget{{
				id: "openai-finish-reason", symbol: "ai.mapOAIFinishReason", goFact: "go:github.com/MichaelKinsy/PiG/ai#mapOAIFinishReason",
				rangeRef: sourceRange{repository: "pig", path: "ai/openai.go", semanticID: "ai.mapOAIFinishReason", start: "func mapOAIFinishReason(", end: ""},
				rootRef:  sourceRange{repository: "pig", path: "ai/openai.go", semanticID: "ai.openAIProvider.parseSSE.finishReason", start: "stopReason, errorMessage := mapOAIFinishReason(finishReason)", end: "\n\tcase !supportsFinishReason:"},
			}},
		},
	}
}

func providerWireRule(facet string) string {
	return "provider-wire:" + facet + ":decided-mapping+production-reachability+bound-A2-or-A3+execution-witness"
}

func addSourceRanges(root string, snapshot *Snapshot, pins map[string]*Pin, ranges []sourceRange) ([]string, error) {
	ids := make([]string, 0, len(ranges))
	for _, reference := range ranges {
		pin, err := makeSourceRangePin(root, snapshot, reference)
		if err != nil {
			return nil, err
		}
		pins[pin.ID] = pin
		ids = append(ids, pin.ID)
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

func makeSourceRangePin(root string, snapshot *Snapshot, reference sourceRange) (*Pin, error) {
	base := root
	commit := snapshot.TargetCommit
	if reference.repository == "upstream" {
		base = filepath.Join(root, ".upstream", "current")
		commit = snapshot.UpstreamCommit
	} else if reference.repository != "pig" {
		return nil, fmt.Errorf("source range %s has invalid repository %s", reference.semanticID, reference.repository)
	}
	baseAbsolute, err := filepath.Abs(base)
	if err != nil {
		return nil, fmt.Errorf("source range %s: %w", reference.semanticID, err)
	}
	baseReal, err := filepath.EvalSymlinks(baseAbsolute)
	if err != nil {
		return nil, fmt.Errorf("source range %s: %w", reference.semanticID, err)
	}
	pathReal, err := filepath.EvalSymlinks(filepath.Join(baseReal, filepath.FromSlash(reference.path)))
	if err != nil {
		return nil, fmt.Errorf("source range %s: %w", reference.semanticID, err)
	}
	relative, err := filepath.Rel(baseReal, pathReal)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("source range %s escapes repository root", reference.semanticID)
	}
	data, err := os.ReadFile(pathReal)
	if err != nil {
		return nil, fmt.Errorf("source range %s: %w", reference.semanticID, err)
	}
	if reference.repository == "pig" {
		if _, gitErr := repositoryGitOutput(context.Background(), root, "rev-parse", "--show-toplevel"); gitErr == nil {
			prefix, prefixErr := repositoryGitOutput(context.Background(), root, "rev-parse", "--show-prefix")
			if prefixErr != nil {
				return nil, fmt.Errorf("source range %s repository prefix: %w", reference.semanticID, prefixErr)
			}
			// The commit is pinned permanently, so resolve the path as it was
			// at that commit rather than where the repository sits today.
			object, objectErr := resolveCommitObject(root, commit, prefix, reference.path)
			if objectErr != nil {
				return nil, fmt.Errorf("source range %s: %w", reference.semanticID, objectErr)
			}
			pinned, pinnedErr := repositoryGitOutputBytes(context.Background(), root, "show", object)
			if pinnedErr != nil {
				return nil, fmt.Errorf("source range %s at %s: %w", reference.semanticID, commit, pinnedErr)
			}
			data = pinned
		}
	}
	start := bytes.Index(data, []byte(reference.start))
	if start < 0 {
		return nil, fmt.Errorf("source range %s start marker not found", reference.semanticID)
	}
	end := len(data)
	if reference.end != "" {
		relativeEnd := bytes.Index(data[start+len(reference.start):], []byte(reference.end))
		if relativeEnd < 0 {
			return nil, fmt.Errorf("source range %s end marker not found", reference.semanticID)
		}
		end = start + len(reference.start) + relativeEnd
	}
	quote := data[start:end]
	startLine := bytes.Count(data[:start], []byte{'\n'}) + 1
	endLine := startLine + bytes.Count(quote, []byte{'\n'})
	return &Pin{
		Kind: KindPin, ID: "pin:source:" + idDigest(reference.repository+"\x00"+reference.path+"\x00"+reference.semanticID),
		SnapshotID: snapshot.ID, Repository: reference.repository, Commit: commit, Path: reference.path,
		SemanticID: reference.semanticID, StartLine: startLine, EndLine: endLine, QuoteHash: HashBytes(quote),
		APIHash: HashBytes([]byte(reference.start)), BodyHash: HashBytes(quote),
	}, nil
}

func decisionCoversBehavior(records []Record, decisionID, behaviorID string) bool {
	for _, record := range records {
		decision, ok := record.(*Decision)
		if ok && decision.ID == decisionID && decision.DecisionType == "mapping" && slices.Contains(decision.ScopeIDs, behaviorID) {
			return true
		}
	}
	return false
}

func findDenominatorFacts(records []Record, references []string) ([]string, error) {
	ids := make([]string, 0, len(references))
	for _, reference := range references {
		dataset, subject, ok := strings.Cut(reference, "\x00")
		if !ok {
			return nil, fmt.Errorf("invalid fact reference %q", reference)
		}
		matches := 0
		for _, record := range records {
			fact, factOK := record.(*Fact)
			if factOK && fact.FactType == "denominator:"+dataset && fact.SubjectID == subject {
				ids = append(ids, fact.ID)
				matches++
			}
		}
		if matches != 1 {
			return nil, fmt.Errorf("fact %s/%s matched %d records", dataset, subject, matches)
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}
