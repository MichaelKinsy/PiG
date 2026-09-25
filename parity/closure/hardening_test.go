package closure

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/parity/correspondence"
)

// footprintGraph hosts one production pin at sharedPath belonging to a target
// mapped to behavior:other, whose origin boundary is "other:boundary".
func footprintGraph(sharedPath string) *Graph {
	records := map[string]Record{
		"pin:prod":       &Pin{Kind: KindPin, ID: "pin:prod", Repository: "pig", Path: sharedPath, SemanticID: "sym.shared"},
		"pin:origin":     &Pin{Kind: KindPin, ID: "pin:origin", Repository: "upstream", Path: "packages/other.ts", SemanticID: "other:boundary"},
		"target:shared":  &Target{Kind: KindTarget, ID: "target:shared", PinIDs: []string{"pin:prod"}, Language: "go"},
		"behavior:other": &Behavior{Kind: KindBehavior, ID: "behavior:other", OriginPinIDs: []string{"pin:origin"}},
		"mapping:other":  &Mapping{Kind: KindMapping, ID: "mapping:other", BehaviorID: "behavior:other", TargetIDs: []string{"target:shared"}},
	}
	return &Graph{Records: records}
}

func TestConfineFactDiffRejectsFootprintOutsideLease(t *testing.T) {
	graph := footprintGraph("shared.go")
	// The lease owns behavior:mine, not behavior:other, so a change to shared.go
	// touches a boundary the unit does not own.
	unit := WorkUnit{ID: "work-unit:mine", BehaviorID: "behavior:mine", SemanticBoundaryIDs: []string{"mine:boundary"}}
	if err := ConfineFactDiffToUnit(graph, unit, []string{"shared.go"}); err == nil || !strings.Contains(err.Error(), "other:boundary") {
		t.Fatalf("footprint escape error = %v, want other:boundary", err)
	}
	// A change to a file that hosts no other behavior's target is confined.
	if err := ConfineFactDiffToUnit(graph, unit, []string{"private.go"}); err != nil {
		t.Fatalf("confined change rejected: %v", err)
	}
	// When the unit owns the boundary the changed path touches, it passes.
	owning := WorkUnit{ID: "work-unit:other", BehaviorID: "behavior:other", SemanticBoundaryIDs: []string{"other:boundary"}}
	if err := ConfineFactDiffToUnit(graph, owning, []string{"shared.go"}); err != nil {
		t.Fatalf("owning unit rejected: %v", err)
	}
}

func TestDedupTranslationEditsCollapsesIdenticalAndFailsCollisions(t *testing.T) {
	identical := correspondence.TranslationEdit{Path: "gen.go", OriginalHash: "sha256:a", Replacement: "same"}
	deduped, err := dedupTranslationEdits([]correspondence.TranslationEdit{identical, identical})
	if err != nil {
		t.Fatalf("dedup identical = %v", err)
	}
	if len(deduped) != 1 || deduped[0].Path != "gen.go" {
		t.Fatalf("deduped = %#v, want a single gen.go edit", deduped)
	}
	conflicting := []correspondence.TranslationEdit{
		{Path: "gen.go", OriginalHash: "sha256:a", Replacement: "one"},
		{Path: "gen.go", OriginalHash: "sha256:a", Replacement: "two"},
	}
	if _, err := dedupTranslationEdits(conflicting); err == nil || !strings.Contains(err.Error(), "conflicting edits to generated path gen.go") {
		t.Fatalf("dedup collision error = %v", err)
	}
}

func TestWorkerScratchPathClassification(t *testing.T) {
	scratch := []string{"tmp", "tmp/x.go", "pig/tmp", "pig/tmp/deep/y.go"}
	for _, path := range scratch {
		if !isWorkerScratchPath(path) {
			t.Fatalf("isWorkerScratchPath(%q) = false, want true", path)
		}
	}
	production := []string{"ai/openai.go", "example.go", "tmpfile.go", "atmp/x.go", "pig/tmpx.go"}
	for _, path := range production {
		if isWorkerScratchPath(path) {
			t.Fatalf("isWorkerScratchPath(%q) = true, want false", path)
		}
	}
	if !allCoveredRangesScratch([]EvidenceCoveredRange{{Path: "tmp/a.go"}, {Path: "pig/tmp/b.go"}}) {
		t.Fatal("all-scratch ranges classified as production")
	}
	if allCoveredRangesScratch([]EvidenceCoveredRange{{Path: "tmp/a.go"}, {Path: "ai/openai.go"}}) {
		t.Fatal("mixed ranges classified as scratch-only")
	}
}

func TestBuildRejectsScratchOnlyWitness(t *testing.T) {
	if _, err := Build(provedFixture(t)); err != nil {
		t.Fatalf("control: proved fixture must build clean: %v", err)
	}
	records := provedFixture(t)
	for _, record := range records {
		if witness, ok := record.(*ExecutionWitness); ok {
			for i := range witness.CoveredRanges {
				witness.CoveredRanges[i].Path = "tmp/example.go"
			}
		}
	}
	if _, err := Build(records); err == nil || !strings.Contains(err.Error(), "covers only worker scratch") {
		t.Fatalf("scratch-only witness error = %v, want scratch rejection", err)
	}
}
