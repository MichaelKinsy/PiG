package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/test/parity/upstreampackages"
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

func TestPortedParentMayCarryReviewedClosedMembers(t *testing.T) {
	parentID := "pkg:client/.#ServerError"
	for _, closed := range []string{"designed-out", "divergence"} {
		childID := parentID + "::property:stack"
		problems := validateParentClosure(
			[]inventoryEntry{{ID: parentID}, {ID: childID, ParentID: parentID}},
			map[string]mappingEntry{
				parentID: {ID: parentID, Disposition: "ported"},
				childID:  {ID: childID, Disposition: closed},
			},
		)
		if len(problems) != 0 {
			t.Fatalf("a %s member blocked its ported parent: %v", closed, problems)
		}
	}
}

// A designed-out child is a closed decision: it does not hold a ported owner open, while a pending sibling still does.
func TestPortedParentClosesOverADesignedOutMember(t *testing.T) {
	parentID := "pkg:server/.#ServerError"
	stack, code := parentID+"::property:stack", parentID+"::property:code"
	upstream := []inventoryEntry{{ID: parentID}, {ID: stack, ParentID: parentID}, {ID: code, ParentID: parentID}}
	mapped := map[string]mappingEntry{
		parentID: {ID: parentID, Disposition: "ported"},
		stack:    {ID: stack, Disposition: "designed-out"},
		code:     {ID: code, Disposition: "ported"},
	}
	if problems := validateParentClosure(upstream, mapped); len(problems) != 0 {
		t.Fatalf("a designed-out member kept its owner open: %v", problems)
	}
	mapped[code] = mappingEntry{ID: code, Disposition: "pending"}
	if problems := validateParentClosure(upstream, mapped); len(problems) != 1 || !strings.Contains(problems[0], code) {
		t.Fatalf("a pending sibling must still be reported: %v", problems)
	}
}

func TestPortedMappingsRequireKnownKindSpecificLayersAndAsyncContract(t *testing.T) {
	entry := mappingEntry{
		ID: "cli:pi/--approve", Disposition: "ported", PigTargets: []string{"coding/cli/args.go"},
		Layers:     map[string]string{"anything": "complete", "parser": "complete"},
		Production: []string{"call:coding/cli/main.go"}, Evidence: []string{"scenario:test/parity/scenarios/project-trust/03-approve-loads-project-extension.toml"},
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
		Production: []string{"registry:coding/extension/api.go"}, Evidence: []string{"conformance:test/extension-conformance/conformance_test.go"},
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
		"coding/extension/api.go":                 "package extension\ntype API struct{}\n",
		"coding/runtime.go":                       "package coding\nfunc NewRuntime() {}\n",
		"test/upstream-parity/api_parity_test.go": "package upstreamparity\nfunc TestAPIParity() {}\n",
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
			Evidence:   []string{"test:test/upstream-parity/api_parity_test.go#TestAPIParity"},
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
	if err := os.MkdirAll(filepath.Join(root, "docs", "parity"), 0o755); err != nil {
		t.Fatal(err)
	}
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

func publicAPIEntry() mappingEntry {
	return mappingEntry{
		ID: "pkg:coding-agent/.#RpcClient::property:abort", Disposition: "ported", UpstreamShapeHash: testShapeHash,
		PigTargets: []string{"client/client.go#Client.Abort"},
		Layers:     map[string]string{"shape": "complete", "production": "public-api-tested", "behavior": "complete"},
		Production: []string{"public-api:client/client.go#Client.Abort"},
		Evidence:   []string{"test:client/client_test.go#TestAbort"},
	}
}

func publicAPIRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for file, content := range map[string]string{
		"client/client.go":      "package client\ntype Client struct{}\nfunc (Client) Abort() {}\nfunc (Client) abort() {}\nfunc Exported() {}\nfunc hidden() {}\n",
		"client/client_test.go": "package client\nfunc TestAbort() {}\n",
	} {
		path := filepath.Join(root, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func hasProblem(problems []string, want string) bool {
	return slices.ContainsFunc(problems, func(problem string) bool { return strings.Contains(problem, want) })
}

// A public library API whose Pi counterpart is a public export may close on test callers, but only under the
// distinct public-api-tested production status, so the ledger never reports it as production-reachable.
func TestPublicAPITestedClosureIsDistinctFromProduction(t *testing.T) {
	root := publicAPIRoot(t)
	entry := publicAPIEntry()
	upstream := inventoryEntry{ID: entry.ID}
	if problems := append(validateMappingClosure(entry, upstream, true), validateMappingReferences(entry, root)...); len(problems) != 0 {
		t.Fatalf("a public-api-tested row is rejected: %v", problems)
	}

	claimed := publicAPIEntry()
	claimed.Layers["production"] = "complete"
	if problems := validateMappingClosure(claimed, upstream, true); !hasProblem(problems, "public-api production must be marked public-api-tested") {
		t.Fatalf("claiming production for public-api-only references: %v", problems)
	}

	mixed := publicAPIEntry()
	mixed.Production = append(mixed.Production, "call:client/client.go#Exported")
	if problems := validateMappingClosure(mixed, upstream, true); !hasProblem(problems, "public-api-tested production layer needs every production reference to be public-api") {
		t.Fatalf("mixing a call reference into a public-api-tested row: %v", problems)
	}

	untested := publicAPIEntry()
	untested.Evidence = []string{"scenario:test/parity/scenarios/x.toml"}
	if problems := validateMappingClosure(untested, upstream, true); !hasProblem(problems, "public-api-tested production layer needs a test reference") {
		t.Fatalf("public-api-tested without a test: %v", problems)
	}

	hidden := publicAPIEntry()
	hidden.Production = []string{"public-api:client/client.go#hidden"}
	if problems := validateMappingReferences(hidden, root); !hasProblem(problems, "is not an exported") {
		t.Fatalf("an unexported function: %v", problems)
	}
	method := publicAPIEntry()
	method.Production = []string{"public-api:client/client.go#Client.abort"}
	if problems := validateMappingReferences(method, root); !hasProblem(problems, "is not an exported") {
		t.Fatalf("an unexported method: %v", problems)
	}
	for _, path := range []string{"internal/codingagent/session.go", "coding/internal/x.go", "cmd/pig/main_default.go", "tui/testdata/x.go"} {
		if err := validatePublicAPIFragment(path, "Exported"); err == nil || !strings.Contains(err.Error(), "not in an importable public package") {
			t.Fatalf("a symbol in %s: %v", path, err)
		}
	}
	inTest := publicAPIEntry()
	inTest.Production = []string{"public-api:client/client_test.go#TestAbort"}
	if problems := validateMappingReferences(inTest, root); !hasProblem(problems, "test file") {
		t.Fatalf("a symbol declared in a test file: %v", problems)
	}
}

// A parity-harness probe runs only under PIG_PARITY_HARNESS=1, so a production reference to it does not prove that the binary reaches the symbol.
func TestProductionReferenceRejectsParityHarnessProbe(t *testing.T) {
	root := publicAPIRoot(t)
	harness := filepath.Join(root, "internal", "codingagent", "parity_harness.go")
	if err := os.MkdirAll(filepath.Dir(harness), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(harness, []byte("package codingagent\ntype InteractiveMode struct{}\nfunc (*InteractiveMode) probeLoader() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := mappingEntry{
		ID: "pkg:tui/.#Loader::property:render", Disposition: "ported", UpstreamShapeHash: testShapeHash,
		PigTargets: []string{"client/client.go#Client.Abort"},
		Layers:     map[string]string{"shape": "complete", "production": "complete", "behavior": "complete"},
		Production: []string{"call:internal/codingagent/parity_harness.go#InteractiveMode.probeLoader"},
		Evidence:   []string{"test:client/client_test.go#TestAbort"},
	}
	if problems := validateMappingReferences(entry, root); !hasProblem(problems, "is a parity-harness probe") {
		t.Fatalf("a parity-harness production reference: %v", problems)
	}
	entry.Production = []string{"call:client/client.go#Exported"}
	if problems := validateMappingReferences(entry, root); len(problems) != 0 {
		t.Fatalf("an ordinary call reference is rejected: %v", problems)
	}
}

func TestDivergenceMappingsRequireApprovedLedgerAndCallSiteMarker(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs", "parity"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "coding", "runtime.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("package coding\nfunc Run() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := mappingEntry{ID: "pkg:coding-agent/.#main", Divergence: "D25", PigTargets: []string{"coding/runtime.go#Run"}}
	divergences := filepath.Join(root, "docs/parity/DIVERGENCES.md")
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
	if err := os.MkdirAll(filepath.Join(root, "docs", "parity"), 0o755); err != nil {
		t.Fatal(err)
	}
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

func TestValidateInventoryRequiresEveryListedPackage(t *testing.T) {
	packageProblems := func(keys ...string) []string {
		upstream := testInventory()
		byKey := map[string]upstreampackages.Package{}
		for _, listed := range upstreampackages.All() {
			byKey[listed.Key] = listed
		}
		for _, key := range keys {
			upstream.Packages = append(upstream.Packages, inventoryPackage{Key: key, Name: byKey[key].Name, Version: upstream.UpstreamVersion, Origin: "published"})
		}
		return slices.DeleteFunc(validateInventory(upstream), func(problem string) bool {
			return !strings.Contains(problem, "inventory package") && !strings.Contains(problem, "tracked package")
		})
	}
	var all []string
	for _, listed := range upstreampackages.All() {
		all = append(all, listed.Key)
	}
	if problems := packageProblems(all...); len(problems) != 0 {
		t.Fatalf("every listed package = %v", problems)
	}
	without := slices.DeleteFunc(slices.Clone(all), func(key string) bool { return key == "env" })
	if problems := packageProblems(without...); len(problems) != 1 || !strings.Contains(problems[0], "missing tracked package env") {
		t.Fatalf("a package missing from the inventory must be reported, got %v", problems)
	}
	upstream := testInventory()
	upstream.Packages = []inventoryPackage{{Key: "unlisted", Name: "@earendil-works/unlisted", Version: upstream.UpstreamVersion, Origin: "published"}}
	if problems := validateInventory(upstream); !slices.ContainsFunc(problems, func(problem string) bool {
		return strings.Contains(problem, `unexpected key/name "@earendil-works/unlisted"`)
	}) {
		t.Fatalf("an unlisted package must be rejected, got %v", problems)
	}
	upstream = testInventory()
	upstream.Packages = []inventoryPackage{{Key: "agent", Name: "@earendil-works/pi-agent-core", Version: upstream.UpstreamVersion}}
	if problems := validateInventory(upstream); !slices.ContainsFunc(problems, func(problem string) bool { return strings.Contains(problem, "origin") }) {
		t.Fatalf("a package without an origin must be rejected, got %v", problems)
	}
}

func TestPendingMappingLedgerCarriesEveryRowWithAnUnchangedShape(t *testing.T) {
	const oldHash = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	previous := filepath.Join(t.TempDir(), "mapping-old.json")
	// A version leap never resets status: every row with an unchanged shape is carried, and the gap detector re-derives it.
	ported := `{"id":"pkg:ai/.#ported","disposition":"ported","upstreamShapeHash":"` + testShapeHash + `","pigTargets":["ai/kept.go#Kept"],"layers":{"shape":"complete"}}`
	partial := `{"id":"pkg:ai/.#partial","disposition":"partial","upstreamShapeHash":"` + testShapeHash + `","pigTargets":["ai/partial.go#Partial"]}`
	divergence := `{"id":"pkg:ai/.#divergence","disposition":"divergence","upstreamShapeHash":"` + testShapeHash + `","divergence":"D1","rationale":"r"}`
	deferred := `{"id":"pkg:ai/.#deferred","disposition":"deferred","upstreamShapeHash":"` + testShapeHash + `","rationale":"outside 0.3.x"}`
	designed := `{"id":"pkg:ai/.#designed","disposition":"designed-out","upstreamShapeHash":"` + testShapeHash + `","rationale":"Go typing"}`
	changed := `{"id":"pkg:ai/.#changed","disposition":"deferred","upstreamShapeHash":"` + oldHash + `","rationale":"old shape"}`
	removed := `{"id":"pkg:ai/.#removed","disposition":"deferred","upstreamShapeHash":"` + testShapeHash + `","rationale":"gone"}`
	if err := os.WriteFile(previous, []byte(`{"upstreamVersion":"0.84.0","mappings":[`+strings.Join([]string{ported, partial, divergence, deferred, designed, changed, removed}, ",")+`]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	carried, err := readCarriedMappings(previous)
	if err != nil {
		t.Fatal(err)
	}
	upstream := testInventory()
	upstream.UpstreamVersion = "0.99.2"
	upstream.Interfaces = []inventoryEntry{
		{ID: "pkg:ai/.#added", ShapeHash: testShapeHash},
		{ID: "pkg:ai/.#changed", ShapeHash: testShapeHash},
		{ID: "pkg:ai/.#deferred", ShapeHash: testShapeHash},
		{ID: "pkg:ai/.#designed", ShapeHash: testShapeHash},
		{ID: "pkg:ai/.#divergence", ShapeHash: testShapeHash},
		{ID: "pkg:ai/.#partial", ShapeHash: testShapeHash},
		{ID: "pkg:ai/.#ported", ShapeHash: testShapeHash},
	}
	ledger := pendingMappingLedger(upstream, carried)
	if ledger.UpstreamVersion != "0.99.2" {
		t.Fatalf("upstreamVersion = %q", ledger.UpstreamVersion)
	}
	pending := func(id string) string {
		return `{"id":"` + id + `","disposition":"pending","upstreamShapeHash":"` + testShapeHash + `"}`
	}
	want := []string{
		pending("pkg:ai/.#added"), pending("pkg:ai/.#changed"), deferred, designed, divergence, partial, ported,
	}
	got := make([]string, len(ledger.Mappings))
	for i, row := range ledger.Mappings {
		got[i] = string(row)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("rows =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestReferenceFragmentNamesMethodsOfGenericTypes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.go")
	src := "package p\n\ntype State[T any] struct{}\n\nfunc (s *State[T]) Dispose() {}\n\ntype Pair[K comparable, V any] struct{}\n\nfunc (p Pair[K, V]) Key() K { var k K; return k }\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"State.Dispose", "Pair.Key", "State"} {
		if err := validateReferenceFragment(path, fragment); err != nil {
			t.Errorf("%s: %v", fragment, err)
		}
	}
	for _, fragment := range []string{"State.Missing", "Pair.Dispose", "Other.Dispose"} {
		if err := validateReferenceFragment(path, fragment); err == nil {
			t.Errorf("%s must not name a declaration", fragment)
		}
	}
}
