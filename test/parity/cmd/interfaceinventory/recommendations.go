package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

type recommendationLedger struct {
	UpstreamVersion string                    `json:"upstreamVersion"`
	GeneratedBy     string                    `json:"generatedBy"`
	Recommendations []interfaceRecommendation `json:"recommendations"`
}

type interfaceRecommendation struct {
	ID                     string   `json:"id"`
	UpstreamShapeHash      string   `json:"upstreamShapeHash"`
	RecommendedDisposition string   `json:"recommendedDisposition"`
	Confidence             string   `json:"confidence"`
	Basis                  []string `json:"basis"`
	Alternatives           []string `json:"alternatives"`
	PigCandidates          []string `json:"pigCandidates"`
	MissingClosure         []string `json:"missingClosure"`
	Provenance             []string `json:"provenance"`
}

type goInventory struct {
	GoVersion  string        `json:"goVersion"`
	Packages   []string      `json:"packages"`
	Interfaces []goInterface `json:"interfaces"`
}

type goInterface struct {
	ID        string          `json:"id"`
	Package   string          `json:"package"`
	Name      string          `json:"name"`
	Kind      string          `json:"kind"`
	Exported  bool            `json:"exported"`
	Shape     json.RawMessage `json:"shape"`
	ShapeHash string          `json:"shapeHash"`
}

var recommendationClosureKinds = map[string]struct{}{
	"pig-cli-shape": {}, "pig-go-shape": {}, "candidate-disambiguation": {}, "shape-translation-review": {},
	"production-reachability": {}, "required-layers": {}, "behavioral-evidence": {}, "async-contract": {},
	"wire-shape": {}, "host-dispatch": {}, "sdk-go": {}, "sdk-rust": {}, "sdk-python": {},
	"isolated-conformance": {}, "packed-conformance": {},
}

func validateRecommendations(upstream inventory, ledger recommendationLedger, pig goInventory) []string {
	var problems []string
	if ledger.UpstreamVersion != upstream.UpstreamVersion {
		problems = append(problems, fmt.Sprintf("recommendation upstreamVersion = %q, inventory = %q", ledger.UpstreamVersion, upstream.UpstreamVersion))
	}
	if strings.TrimSpace(ledger.GeneratedBy) == "" {
		problems = append(problems, "recommendation generatedBy is empty")
	}
	pigPackages := make(map[string]struct{}, len(pig.Packages))
	for _, pkg := range pig.Packages {
		if strings.TrimSpace(pkg) == "" {
			problems = append(problems, "Go inventory contains an empty package")
			continue
		}
		if _, duplicate := pigPackages[pkg]; duplicate {
			problems = append(problems, fmt.Sprintf("duplicate Go inventory package %s", pkg))
		}
		pigPackages[pkg] = struct{}{}
	}
	pigIDs := make(map[string]struct{}, len(pig.Interfaces))
	for _, candidate := range pig.Interfaces {
		if strings.TrimSpace(candidate.ID) == "" || !validShapeHash(candidate.ShapeHash) {
			problems = append(problems, fmt.Sprintf("invalid Go candidate %q", candidate.ID))
			continue
		}
		if candidate.ID != "go:"+candidate.Package+"#"+candidate.Name {
			problems = append(problems, fmt.Sprintf("Go candidate %s ID does not match package/name", candidate.ID))
		}
		if _, exists := pigPackages[candidate.Package]; !exists {
			problems = append(problems, fmt.Sprintf("Go candidate %s references unknown package %s", candidate.ID, candidate.Package))
		}
		if strings.TrimSpace(candidate.Kind) == "" {
			problems = append(problems, fmt.Sprintf("Go candidate %s has empty kind", candidate.ID))
		}
		if actual, err := semanticShapeHash(candidate.Shape); err != nil {
			problems = append(problems, fmt.Sprintf("Go candidate %s has invalid shape: %v", candidate.ID, err))
		} else if candidate.ShapeHash != actual {
			problems = append(problems, fmt.Sprintf("Go candidate %s shape hash does not match shape", candidate.ID))
		}
		if _, duplicate := pigIDs[candidate.ID]; duplicate {
			problems = append(problems, fmt.Sprintf("duplicate Go candidate %s", candidate.ID))
		}
		pigIDs[candidate.ID] = struct{}{}
	}
	hashes := make(map[string]string, len(upstream.Interfaces))
	for _, entry := range upstream.Interfaces {
		hashes[entry.ID] = entry.ShapeHash
	}
	seen := make(map[string]struct{}, len(ledger.Recommendations))
	for _, recommendation := range ledger.Recommendations {
		hash, exists := hashes[recommendation.ID]
		if !exists {
			problems = append(problems, fmt.Sprintf("recommendation %s has no upstream interface", recommendation.ID))
		} else if recommendation.UpstreamShapeHash != hash {
			problems = append(problems, fmt.Sprintf("recommendation %s is stale for current upstream shape", recommendation.ID))
		}
		if _, duplicate := seen[recommendation.ID]; duplicate {
			problems = append(problems, fmt.Sprintf("duplicate recommendation for %s", recommendation.ID))
		}
		seen[recommendation.ID] = struct{}{}
		if _, valid := dispositions[recommendation.RecommendedDisposition]; !valid {
			problems = append(problems, fmt.Sprintf("recommendation %s has unknown disposition %q", recommendation.ID, recommendation.RecommendedDisposition))
		}
		if recommendation.RecommendedDisposition == "ported" {
			problems = append(problems, fmt.Sprintf("recommendation %s cannot recommend authoritative ported disposition", recommendation.ID))
		}
		if recommendation.Confidence != "low" && recommendation.Confidence != "medium" && recommendation.Confidence != "high" {
			problems = append(problems, fmt.Sprintf("recommendation %s has invalid confidence %q", recommendation.ID, recommendation.Confidence))
		}
		if len(recommendation.Basis) == 0 || len(recommendation.Provenance) == 0 {
			problems = append(problems, fmt.Sprintf("recommendation %s requires basis and provenance", recommendation.ID))
		}
		problems = append(problems, validateNonEmptyUnique(recommendation.ID, "basis", recommendation.Basis, nil)...)
		problems = append(problems, validateNonEmptyUnique(recommendation.ID, "provenance", recommendation.Provenance, nil)...)
		if len(recommendation.Alternatives) == 0 {
			problems = append(problems, fmt.Sprintf("recommendation %s requires alternatives", recommendation.ID))
		}
		problems = append(problems, validateNonEmptyUnique(recommendation.ID, "alternatives", recommendation.Alternatives, dispositions)...)
		if slices.Contains(recommendation.Alternatives, recommendation.RecommendedDisposition) {
			problems = append(problems, fmt.Sprintf("recommendation %s repeats its disposition in alternatives", recommendation.ID))
		}
		if recommendation.PigCandidates == nil {
			problems = append(problems, fmt.Sprintf("recommendation %s pigCandidates must be an array", recommendation.ID))
		}
		problems = append(problems, validateNonEmptyUnique(recommendation.ID, "pigCandidates", recommendation.PigCandidates, nil)...)
		for _, candidate := range recommendation.PigCandidates {
			if _, exists := pigIDs[candidate]; !exists {
				problems = append(problems, fmt.Sprintf("recommendation %s references unknown Pig candidate %s", recommendation.ID, candidate))
			}
		}
		if recommendation.MissingClosure == nil {
			problems = append(problems, fmt.Sprintf("recommendation %s missingClosure must be an array", recommendation.ID))
		} else if len(recommendation.MissingClosure) == 0 {
			problems = append(problems, fmt.Sprintf("recommendation %s must name missing closure", recommendation.ID))
		}
		problems = append(problems, validateNonEmptyUnique(recommendation.ID, "missingClosure", recommendation.MissingClosure, recommendationClosureKinds)...)
		if recommendation.RecommendedDisposition == "partial" && len(recommendation.PigCandidates) == 0 {
			problems = append(problems, fmt.Sprintf("recommendation %s is partial without a Pig candidate", recommendation.ID))
		}
	}
	if strings.HasPrefix(ledger.GeneratedBy, "pig-interface-recommend-") {
		for id := range hashes {
			if _, exists := seen[id]; !exists {
				problems = append(problems, fmt.Sprintf("generated recommendations missing %s", id))
			}
		}
	}
	return problems
}

func validateNonEmptyUnique(id, label string, values []string, allowed map[string]struct{}) []string {
	var problems []string
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, fmt.Sprintf("recommendation %s %s contains an empty value", id, label))
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			problems = append(problems, fmt.Sprintf("recommendation %s %s repeats %q", id, label, value))
		}
		seen[value] = struct{}{}
		if allowed != nil {
			if _, valid := allowed[value]; !valid {
				problems = append(problems, fmt.Sprintf("recommendation %s %s contains unknown value %q", id, label, value))
			}
		}
	}
	return problems
}
