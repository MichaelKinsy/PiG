package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

const testShapeHash = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func testInventory() inventory {
	return inventory{
		UpstreamVersion:   "0.83.0",
		TypeScriptVersion: "5.9.3",
		Origin:            "published",
		Interfaces: []inventoryEntry{
			{ID: "pkg:coding-agent/.#ExtensionAPI", Kind: "interface", ShapeHash: testShapeHash},
			{ID: "pkg:coding-agent/.#main", Kind: "function", ShapeHash: testShapeHash},
		},
	}
}

func testGoInventory(t *testing.T) goInventory {
	t.Helper()
	shape := json.RawMessage(`{"type":"interface{}"}`)
	hash, err := semanticShapeHash(shape)
	if err != nil {
		t.Fatal(err)
	}
	return goInventory{Packages: []string{"example"}, Interfaces: []goInterface{{
		ID: "go:example#ExtensionAPI", Package: "example", Name: "ExtensionAPI", Kind: "type", Shape: shape, ShapeHash: hash,
	}}}
}

func TestValidateInventoryRejectsShapeTamperingAndMissingMemberParent(t *testing.T) {
	shape := json.RawMessage(`{"type":"string"}`)
	hash, err := semanticShapeHash(shape)
	if err != nil {
		t.Fatal(err)
	}
	upstream := testInventory()
	upstream.Interfaces = []inventoryEntry{
		{ID: "pkg:coding-agent/.#Demo", Kind: "interface", Shape: shape, ShapeHash: hash},
		{
			ID: "pkg:coding-agent/.#Demo::property:run", ParentID: "pkg:coding-agent/.#Missing", Role: "property",
			Kind: "property", Shape: shape, ShapeHash: hash,
		},
	}
	problems := validateInventory(upstream)
	for _, want := range []string{"references missing parent"} {
		if !slices.ContainsFunc(problems, func(problem string) bool { return strings.Contains(problem, want) }) {
			t.Fatalf("problems = %v, want containing %q", problems, want)
		}
	}

	upstream.Interfaces[0].Shape = json.RawMessage(`{"type":"number"}`)
	problems = validateInventory(upstream)
	if !slices.ContainsFunc(problems, func(problem string) bool { return strings.Contains(problem, "computed") }) {
		t.Fatalf("tampered-shape problems = %v", problems)
	}
}

func TestAC36RejectsMissingUnclassifiedAndUnreachableInterfaces(t *testing.T) {
	upstream := testInventory()
	tests := []struct {
		name    string
		ledger  mappingLedger
		strict  bool
		contain []string
	}{
		{
			name: "missing mapping",
			ledger: mappingLedger{UpstreamVersion: "0.83.0", Mappings: []mappingEntry{
				{ID: "pkg:coding-agent/.#main", Disposition: "pending"},
			}},
			contain: []string{"missing mapping for pkg:coding-agent/.#ExtensionAPI"},
		},
		{
			name: "unclassified",
			ledger: mappingLedger{UpstreamVersion: "0.83.0", Mappings: []mappingEntry{
				{ID: "pkg:coding-agent/.#ExtensionAPI", Disposition: "unknown"},
				{ID: "pkg:coding-agent/.#main", Disposition: "pending"},
			}},
			contain: []string{`has unknown disposition "unknown"`},
		},
		{
			name: "same ID changed shape invalidates review",
			ledger: mappingLedger{UpstreamVersion: "0.83.0", Mappings: []mappingEntry{
				{ID: "pkg:coding-agent/.#ExtensionAPI", Disposition: "pending", UpstreamShapeHash: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
				{ID: "pkg:coding-agent/.#main", Disposition: "pending", UpstreamShapeHash: testShapeHash},
			}},
			contain: []string{"mapping shape hash", "current upstream"},
		},
		{
			name: "strict pending",
			ledger: mappingLedger{UpstreamVersion: "0.83.0", Mappings: []mappingEntry{
				{ID: "pkg:coding-agent/.#ExtensionAPI", Disposition: "pending"},
				{ID: "pkg:coding-agent/.#main", Disposition: "pending"},
			}},
			strict:  true,
			contain: []string{"remains pending"},
		},
		{
			name: "declaration only is not ported",
			ledger: mappingLedger{UpstreamVersion: "0.83.0", Mappings: []mappingEntry{
				{ID: "pkg:coding-agent/.#ExtensionAPI", Disposition: "ported", UpstreamShapeHash: testShapeHash, PigTargets: []string{"coding/extension/api.go"}, Layers: map[string]string{"api": "complete"}, Evidence: []string{"test:x"}},
				{ID: "pkg:coding-agent/.#main", Disposition: "designed-out", UpstreamShapeHash: testShapeHash, Rationale: "library-only fixture"},
			}},
			contain: []string{"ported without production reachability"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			problems := validateMappings(upstream, tc.ledger, behaviorLedger{}, tc.strict, t.TempDir())
			for _, want := range tc.contain {
				if !slices.ContainsFunc(problems, func(problem string) bool { return strings.Contains(problem, want) }) {
					t.Fatalf("problems = %v, want containing %q", problems, want)
				}
			}
		})
	}
}

func TestPortedParentCannotHidePendingMember(t *testing.T) {
	parentID := "pkg:coding-agent/.#ExtensionAPI"
	childID := parentID + "::property:on"
	problems := validateParentClosure(
		[]inventoryEntry{{ID: parentID}, {ID: childID, ParentID: parentID}},
		map[string]mappingEntry{
			parentID: {ID: parentID, Disposition: "ported"},
			childID:  {ID: childID, Disposition: "pending"},
		},
	)
	if !slices.ContainsFunc(problems, func(problem string) bool { return strings.Contains(problem, "ported while child") }) {
		t.Fatalf("problems = %v", problems)
	}
}

func TestPortedMappingsRequireKnownKindSpecificLayersAndAsyncContract(t *testing.T) {
	entry := mappingEntry{
		ID: "cli:pi/--approve", Disposition: "ported", PigTargets: []string{"cmd/pig/args.go"},
		Layers:     map[string]string{"anything": "complete", "parser": "complete"},
		Production: []string{"call:cmd/pig/main.go"}, Evidence: []string{"scenario:parity/scenarios/project-trust/03-approve-loads-project-extension.toml"},
	}
	upstream := inventoryEntry{ID: entry.ID, Shape: json.RawMessage(`{"returns":"Promise<void>"}`)}
	problems := validateMappingClosure(entry, upstream, false)
	for _, want := range []string{"required layer help", "required layer consumer", "required layer behavior", "without an async contract"} {
		if !slices.ContainsFunc(problems, func(problem string) bool { return strings.Contains(problem, want) }) {
			t.Fatalf("problems = %v, want containing %q", problems, want)
		}
	}
}

func TestExtensionEventMappingsRequireFullSDKAndHostClosure(t *testing.T) {
	entry := mappingEntry{
		ID: "pkg:coding-agent/.#ExtensionAPI::property:on::call:project_trust", Disposition: "ported",
		PigTargets: []string{"coding/extension/api.go"}, Layers: map[string]string{
			"shape": "complete", "api": "complete", "production": "complete", "behavior": "complete",
		},
		Production: []string{"registry:coding/extension/api.go"}, Evidence: []string{"conformance:tests/extension-conformance/conformance_test.go"},
	}
	problems := validateMappingClosure(entry, inventoryEntry{ID: entry.ID}, false)
	for _, want := range []string{"wire", "host-dispatch", "sdk-rust", "sdk-python", "isolated-conformance", "packed-conformance"} {
		if !slices.ContainsFunc(problems, func(problem string) bool { return strings.Contains(problem, "required layer "+want) }) {
			t.Fatalf("problems = %v, want required layer %s", problems, want)
		}
	}
}

func TestValidateMappingsAcceptsClosedAndExplainedRecords(t *testing.T) {
	repoRoot := t.TempDir()
	files := map[string]string{
		"coding/extension/api.go":                  "package extension\ntype API struct{}\n",
		"coding/runtime.go":                        "package coding\nfunc NewRuntime() {}\n",
		"tests/upstream-parity/api_parity_test.go": "package upstreamparity\nfunc TestAPIParity() {}\n",
	}
	for file, content := range files {
		path := filepath.Join(repoRoot, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ledger := mappingLedger{UpstreamVersion: "0.83.0", Mappings: []mappingEntry{
		{
			ID:          "pkg:coding-agent/.#ExtensionAPI",
			Disposition: "ported", UpstreamShapeHash: testShapeHash,
			PigTargets: []string{"coding/extension/api.go#API"},
			Layers:     map[string]string{"shape": "complete", "production": "complete", "behavior": "complete", "api": "complete", "wire": "n/a"},
			Production: []string{"call:coding/runtime.go#NewRuntime"},
			Evidence:   []string{"test:tests/upstream-parity/api_parity_test.go#TestAPIParity"},
		},
		{ID: "pkg:coding-agent/.#main", Disposition: "designed-out", UpstreamShapeHash: testShapeHash, Rationale: "Go binary entrypoint is mapped through CLI inventory"},
	}}
	if problems := validateMappings(testInventory(), ledger, behaviorLedger{}, true, repoRoot); len(problems) != 0 {
		t.Fatalf("validateMappings() = %v", problems)
	}
	ledger.Mappings[0].Layers["invented"] = "complete"
	problems := validateMappings(testInventory(), ledger, behaviorLedger{}, true, repoRoot)
	if !slices.ContainsFunc(problems, func(problem string) bool { return strings.Contains(problem, "unknown closure layer") }) {
		t.Fatalf("unknown-layer problems = %v", problems)
	}
	delete(ledger.Mappings[0].Layers, "invented")
	ledger.Mappings[0].PigTargets[0] = "coding/extension/api.go#MissingAPI"
	problems = validateMappings(testInventory(), ledger, behaviorLedger{}, true, repoRoot)
	if !slices.ContainsFunc(problems, func(problem string) bool { return strings.Contains(problem, "does not name a Go declaration") }) {
		t.Fatalf("missing-fragment problems = %v", problems)
	}
}

func TestMappingReferencesRejectEscapesAndMissingFragments(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("package outside\nfunc Proof() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	problems := validatePathReference("pkg:test/.#X", "Pig target", "../../outside.go#Proof", root, nil)
	if !slices.ContainsFunc(problems, func(problem string) bool { return strings.Contains(problem, "escapes repository root") }) {
		t.Fatalf("lexical-escape problems = %v", problems)
	}
	link := filepath.Join(root, "link.go")
	testenv.Symlink(t, outside, link)
	problems = validatePathReference("pkg:test/.#X", "Pig target", "link.go#Proof", root, nil)
	if !slices.ContainsFunc(problems, func(problem string) bool { return strings.Contains(problem, "symlink escapes repository root") }) {
		t.Fatalf("symlink-escape problems = %v", problems)
	}
	inside := filepath.Join(root, "inside.go")
	if err := os.WriteFile(inside, []byte("package inside\ntype ProofType struct { Value string }\nfunc (ProofType) Run() {}\nfunc Proof() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	problems = validatePathReference("pkg:test/.#X", "Pig target", "inside.go", root, nil)
	if !slices.ContainsFunc(problems, func(problem string) bool { return strings.Contains(problem, "no concrete fragment") }) {
		t.Fatalf("missing-fragment problems = %v", problems)
	}
	for _, reference := range []string{"inside.go#ProofType.Value", "inside.go#ProofType.Run"} {
		if problems := validatePathReference("pkg:test/.#X", "Pig target", reference, root, nil); len(problems) != 0 {
			t.Fatalf("reference %s problems = %v", reference, problems)
		}
	}
	problems = validatePathReference("pkg:test/.#X", "evidence", "scenario:inside.go#Proof", root, map[string]struct{}{"scenario": {}})
	if !slices.ContainsFunc(problems, func(problem string) bool {
		return strings.Contains(problem, "scenario proof must reference a TOML scenario")
	}) {
		t.Fatalf("proof-kind problems = %v", problems)
	}
}

func TestDivergenceMappingsRequireApprovedLedgerAndCallSiteMarker(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "coding", "runtime.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("package coding\nfunc Run() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := mappingEntry{ID: "pkg:coding-agent/.#main", Divergence: "D25", PigTargets: []string{"coding/runtime.go#Run"}}
	divergences := filepath.Join(root, "DIVERGENCES.md")
	if err := os.WriteFile(divergences, []byte("## D25 retry behavior\nSCRUTINIZED:pending\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	problems := validateDivergenceReference(entry, root)
	if !slices.ContainsFunc(problems, func(problem string) bool { return strings.Contains(problem, "not SCRUTINIZED:approved") }) {
		t.Fatalf("pending-divergence problems = %v", problems)
	}
	if err := os.WriteFile(divergences, []byte("## D25 retry behavior\nSCRUTINIZED:approved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	problems = validateDivergenceReference(entry, root)
	if !slices.ContainsFunc(problems, func(problem string) bool { return strings.Contains(problem, "no call-site marker") }) {
		t.Fatalf("missing-marker problems = %v", problems)
	}
	// The marker is assembled so the source text does not contain a literal
	// "pig divergence (Dnn)" that the divergence-consistency scanner would treat
	// as a real, ledger-coupled call site.
	marker := "// pig divergence (" + entry.Divergence + "): retry behavior"
	if err := os.WriteFile(target, []byte("package coding\n"+marker+"\nfunc Run() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if problems := validateDivergenceReference(entry, root); len(problems) != 0 {
		t.Fatalf("approved divergence problems = %v", problems)
	}
}

func TestValidateObservableCLIInventory(t *testing.T) {
	root := t.TempDir()
	for _, file := range []string{"packages/coding-agent/src/cli/args.ts", "packages/coding-agent/src/package-manager-cli.ts"} {
		path := filepath.Join(root, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	observable := observableInventory{
		UpstreamVersion: "0.83.0",
		Kind:            "cli",
		Interfaces: []observableInterface{{
			ID: "cli:pi/--approve", Kind: "cli-flag", ShapeHash: testShapeHash, Command: "pi", Flag: "--approve", Aliases: []string{"-a"},
			Parser: sourceLocation{Path: "packages/coding-agent/src/cli/args.ts", Line: 10},
			Help:   &sourceLocation{Path: "packages/coding-agent/src/cli/args.ts", Line: 20},
		}},
	}
	problems, entries := validateObservable(testInventory(), observable, root)
	if len(problems) != 0 {
		t.Fatalf("validateObservable() = %v", problems)
	}
	if len(entries) != 1 || entries[0].ID != "cli:pi/--approve" {
		t.Fatalf("entries = %+v", entries)
	}

	observable.Interfaces[0].Help = nil
	problems, _ = validateObservable(testInventory(), observable, root)
	if !slices.ContainsFunc(problems, func(problem string) bool { return strings.Contains(problem, "has no help declaration") }) {
		t.Fatalf("missing-help problems = %v", problems)
	}
}

func TestAC37RecommendationsRemainNonAuthoritative(t *testing.T) {
	upstream := testInventory()
	pig := testGoInventory(t)
	recommendations := recommendationLedger{
		UpstreamVersion: "0.83.0", GeneratedBy: "agent:test",
		Recommendations: []interfaceRecommendation{{
			ID: "pkg:coding-agent/.#ExtensionAPI", UpstreamShapeHash: testShapeHash,
			RecommendedDisposition: "ported", Confidence: "high",
			Basis: []string{"candidate matched"}, Alternatives: []string{"partial"},
			PigCandidates: []string{"go:example#ExtensionAPI"}, MissingClosure: []string{"behavioral-evidence"},
			Provenance: []string{"agent:test"},
		}},
	}
	problems := validateRecommendations(upstream, recommendations, pig)
	if !slices.ContainsFunc(problems, func(problem string) bool { return strings.Contains(problem, "cannot recommend authoritative ported") }) {
		t.Fatalf("ported recommendation problems = %v", problems)
	}
	recommendations.Recommendations[0].RecommendedDisposition = "partial"
	recommendations.Recommendations[0].Alternatives = []string{"ported", "pending"}
	if problems := validateRecommendations(upstream, recommendations, pig); len(problems) != 0 {
		t.Fatalf("validateRecommendations() = %v", problems)
	}
	mapping := mappingLedger{UpstreamVersion: "0.83.0", Mappings: []mappingEntry{
		{ID: "pkg:coding-agent/.#ExtensionAPI", Disposition: "pending", UpstreamShapeHash: testShapeHash},
		{ID: "pkg:coding-agent/.#main", Disposition: "pending", UpstreamShapeHash: testShapeHash},
	}}
	problems = validateMappings(upstream, mapping, behaviorLedger{}, true, t.TempDir())
	if !slices.ContainsFunc(problems, func(problem string) bool { return strings.Contains(problem, "remains pending") }) {
		t.Fatalf("recommendation incorrectly satisfied strict mapping: %v", problems)
	}

	recommendations.Recommendations[0].UpstreamShapeHash = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	problems = validateRecommendations(upstream, recommendations, pig)
	if !slices.ContainsFunc(problems, func(problem string) bool { return strings.Contains(problem, "stale for current upstream shape") }) {
		t.Fatalf("stale recommendation problems = %v", problems)
	}
}

func TestRecommendationsRejectTamperedGoCandidateShape(t *testing.T) {
	pig := testGoInventory(t)
	pig.Interfaces[0].ShapeHash = testShapeHash
	ledger := recommendationLedger{UpstreamVersion: "0.83.0", GeneratedBy: "agent:test"}
	problems := validateRecommendations(testInventory(), ledger, pig)
	if !slices.ContainsFunc(problems, func(problem string) bool { return strings.Contains(problem, "shape hash does not match shape") }) {
		t.Fatalf("candidate-shape problems = %v", problems)
	}
	pig = testGoInventory(t)
	pig.Interfaces[0].ID = "go:other#ExtensionAPI"
	problems = validateRecommendations(testInventory(), ledger, pig)
	if !slices.ContainsFunc(problems, func(problem string) bool { return strings.Contains(problem, "ID does not match package/name") }) {
		t.Fatalf("candidate-ID problems = %v", problems)
	}
}

func TestGeneratedRecommendationsMustCoverEveryInterface(t *testing.T) {
	pig := testGoInventory(t)
	ledger := recommendationLedger{
		UpstreamVersion: "0.83.0", GeneratedBy: "pig-interface-recommend-v2",
		Recommendations: []interfaceRecommendation{{
			ID: "pkg:coding-agent/.#ExtensionAPI", UpstreamShapeHash: testShapeHash,
			RecommendedDisposition: "partial", Confidence: "medium", Basis: []string{"candidate"},
			Alternatives: []string{"ported", "pending"}, PigCandidates: []string{"go:example#ExtensionAPI"},
			MissingClosure: []string{"behavioral-evidence"}, Provenance: []string{"generator:test"},
		}},
	}
	problems := validateRecommendations(testInventory(), ledger, pig)
	if !slices.ContainsFunc(problems, func(problem string) bool {
		return strings.Contains(problem, "generated recommendations missing pkg:coding-agent/.#main")
	}) {
		t.Fatalf("problems = %v", problems)
	}
}

func TestRecommendationsRejectUnknownCandidatesAndClosure(t *testing.T) {
	recommendations := recommendationLedger{
		UpstreamVersion: "0.83.0", GeneratedBy: "agent:test",
		Recommendations: []interfaceRecommendation{{
			ID: "pkg:coding-agent/.#ExtensionAPI", UpstreamShapeHash: testShapeHash,
			RecommendedDisposition: "partial", Confidence: "medium",
			Basis: []string{"name match"}, Alternatives: []string{"ported", "ported"},
			PigCandidates: []string{"go:missing#ExtensionAPI"}, MissingClosure: []string{"unknown-gap"},
			Provenance: []string{"agent:test"},
		}},
	}
	problems := validateRecommendations(testInventory(), recommendations, goInventory{})
	for _, want := range []string{"unknown Pig candidate", `alternatives repeats "ported"`, "missingClosure contains unknown value"} {
		if !slices.ContainsFunc(problems, func(problem string) bool { return strings.Contains(problem, want) }) {
			t.Fatalf("problems = %v, want containing %q", problems, want)
		}
	}
}

func TestDecodeJSONFileRejectsUnknownAndTrailingData(t *testing.T) {
	dir := t.TempDir()
	unknown := filepath.Join(dir, "unknown.json")
	if err := os.WriteFile(unknown, []byte(`{"schema`+`Version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var got inventory
	if err := decodeJSONFile(unknown, &got); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown-field error = %v", err)
	}

	trailing := filepath.Join(dir, "trailing.json")
	if err := os.WriteFile(trailing, []byte(`{}{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := decodeJSONFile(trailing, &got); err == nil || !strings.Contains(err.Error(), "trailing JSON value") {
		t.Fatalf("trailing-data error = %v", err)
	}
}
