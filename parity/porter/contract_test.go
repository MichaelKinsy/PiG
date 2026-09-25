package porter

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/parity/closure"
)

func TestRequestValidationIsStrictAndOperationScoped(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{name: "unknown field", body: `{"operation":"status","unknown":true}`, want: "unknown field"},
		{name: "unknown operation", body: `{"operation":"accept"}`, want: "unsupported"},
		{name: "missing record", body: `{"operation":"explain"}`, want: "requires recordId"},
		{name: "trailing value", body: `{"operation":"status"}{}`, want: "trailing JSON"},
		{name: "whitespace root", body: `{"operation":"status","root":" /repo"}`, want: "surrounding whitespace"},
		{name: "escaping database", body: `{"operation":"status","database":"../closure.db"}`, want: "repository-relative"},
		{name: "inventory needs pinned source", body: `{"operation":"inventory"}`, want: "upstreamVersion"},
		{name: "work packet needs role", body: fmt.Sprintf(`{"operation":"work-packet","upstreamVersion":%q,"targetCommit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","snapshotId":"snapshot:test"}`, coding.UpstreamVersion), want: "supported role"},
		{name: "evidence request needs request record", body: `{"operation":"request-evidence","recordId":"obligation:test"}`, want: "evidence request"},
		{name: "mutation request needs request record", body: `{"operation":"request-mutation","recordId":"mutant:test"}`, want: "mutation request"},
		{name: "report rejects unsupported dataset", body: `{"operation":"report","dataset":"scenario"}`, want: "unsupported"},
		{name: "status rejects dataset", body: `{"operation":"status","dataset":"port-map"}`, want: "does not accept dataset"},
		{name: "absolute submission input", body: `{"operation":"submit-bundle","packetPath":"/tmp/packet.json","bundlePath":"bundle.json"}`, want: "repository-relative"},
		{name: "translator needs graph scope", body: fmt.Sprintf(`{"operation":"prompt","upstreamVersion":%q,"targetCommit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","role":"translator","snapshotId":"snapshot:test"}`, coding.UpstreamVersion), want: "requires database"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := DecodeRequest(strings.NewReader(test.body)); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("DecodeRequest() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRequestValidationAcceptsTheSharedReadOnlyEnvelope(t *testing.T) {
	tests := []Request{
		{Operation: OperationMutationReadiness},
		{Operation: OperationInventory, UpstreamVersion: coding.UpstreamVersion, TargetCommit: strings.Repeat("a", 40)},
		{Operation: OperationPlan, UpstreamVersion: coding.UpstreamVersion, TargetCommit: strings.Repeat("a", 40)},
		{Operation: OperationWorkPacket, UpstreamVersion: coding.UpstreamVersion, TargetCommit: strings.Repeat("a", 40), Role: "adversary", SnapshotID: "snapshot:test"},
		{Operation: OperationPrompt, Database: "closure.db", UpstreamVersion: coding.UpstreamVersion, TargetCommit: strings.Repeat("a", 40), Role: "translator", SnapshotID: "snapshot:test"},
		{Operation: OperationReport, Dataset: closure.PortMapDataset},
		{Operation: OperationReport, Dataset: closure.CoverageDataset},
		{Operation: OperationReport, Dataset: closure.SemanticMappingDataset},
		{Operation: OperationReport, Dataset: closure.SemanticDeltaDataset},
		{Operation: OperationReport, Dataset: closure.InputRenderMappingDataset},
		{Operation: OperationReport, Dataset: closure.AsyncContractDataset},
		{Operation: OperationReport, Dataset: closure.ChangedSourceDataset},
		{Operation: OperationReport, Dataset: closure.BehaviorContractDataset},
		{Operation: OperationReport, Dataset: closure.FormatOwnershipDataset},
		{Operation: OperationReport, Dataset: closure.FamilyCoverageDataset},
		{Operation: OperationReport, Dataset: closure.ScenarioQualityDataset},
		{Operation: OperationReport, Dataset: closure.DivergenceDashboardDataset},
		{Operation: OperationReport, Dataset: closure.FoundationDashboardDataset},
	}
	for _, request := range tests {
		if err := request.Validate(); err != nil {
			t.Fatalf("Request.Validate(%q) error = %v", request.Operation, err)
		}
	}
}

func TestExecuteUsesOneDeterministicContract(t *testing.T) {
	root := t.TempDir()
	hash := closure.HashBytes([]byte("fixture"))
	pin := &closure.Pin{Kind: closure.KindPin, ID: "pin:status", SnapshotID: "snapshot:status", Repository: "pig", Commit: strings.Repeat("a", 40), Path: "status.go", SemanticID: "status", StartLine: 1, EndLine: 1, QuoteHash: hash}
	snapshot := &closure.Snapshot{Kind: closure.KindSnapshot, ID: "snapshot:status", UpstreamCommit: strings.Repeat("b", 40), TargetCommit: strings.Repeat("a", 40), ToolchainHash: hash, EnvironmentHash: hash}
	behavior := &closure.Behavior{Kind: closure.KindBehavior, ID: "behavior:status", Name: "status", OriginPinIDs: []string{pin.ID}, Profile: "application"}
	graph, err := closure.Build([]closure.Record{snapshot, pin, behavior})
	if err != nil {
		t.Fatal(err)
	}
	database := "closure.db"
	if err := closure.RebuildStore(context.Background(), filepath.Join(root, database), graph); err != nil {
		t.Fatal(err)
	}
	request := Request{Operation: OperationStatus, Root: root, Database: database}
	response, err := Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if response.Operation != OperationStatus || !strings.Contains(response.Output, "open") {
		t.Fatalf("response = %#v", response)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" {
		t.Fatal("empty response")
	}
}

func TestDecodeRequestRejectsOversizeInput(t *testing.T) {
	if _, err := DecodeRequest(strings.NewReader(strings.Repeat("x", maxRequestBytes+1))); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("DecodeRequest() error = %v", err)
	}
}

func TestExecuteReadsGraphDerivedReport(t *testing.T) {
	root := t.TempDir()
	hash := closure.HashBytes([]byte("report"))
	snapshot := &closure.Snapshot{Kind: closure.KindSnapshot, ID: "snapshot:denominator", UpstreamCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("b", 40), ToolchainHash: hash, EnvironmentHash: hash}
	pin := &closure.Pin{Kind: closure.KindPin, ID: "pin:denominator", SnapshotID: snapshot.ID, Repository: "pig", Commit: strings.Repeat("b", 40), Path: "PORT_MAP.md", SemanticID: "denominator:port-map", StartLine: 1, EndLine: 2, QuoteHash: hash}
	value, err := json.Marshal(map[string]any{"fields": []string{"`packages/example.ts`", "`example.go`", "✅"}})
	if err != nil {
		t.Fatal(err)
	}
	orderValue, err := json.Marshal(map[string]any{"subjects": []string{"packages/example.ts"}})
	if err != nil {
		t.Fatal(err)
	}
	fact := &closure.Fact{Kind: closure.KindFact, ID: "fact:denominator:port-map:example", SnapshotID: snapshot.ID, FactType: "denominator:port-map", SubjectID: "packages/example.ts", Resolution: "observed", PinIDs: []string{pin.ID}, Value: value}
	order := &closure.Fact{Kind: closure.KindFact, ID: "fact:denominator-order:port-map:rows", SnapshotID: snapshot.ID, FactType: "denominator-order:port-map", SubjectID: "rows", Resolution: "resolved", PinIDs: []string{pin.ID}, Value: orderValue}
	claim := &closure.ProvisionalClaim{Kind: closure.KindProvisionalClaim, ID: "provisional:denominator:port-map:example", SnapshotID: snapshot.ID, SubjectID: fact.SubjectID, SourcePinID: pin.ID, Status: "✅"}
	graph, err := closure.Build([]closure.Record{snapshot, pin, order, fact, claim})
	if err != nil {
		t.Fatal(err)
	}
	if err := closure.RebuildStore(context.Background(), filepath.Join(root, "closure.db"), graph); err != nil {
		t.Fatal(err)
	}
	response, err := Execute(context.Background(), Request{Operation: OperationReport, Root: root, Database: "closure.db", Dataset: closure.PortMapDataset})
	if err != nil {
		t.Fatal(err)
	}
	if response.Operation != OperationReport || !strings.Contains(response.Output, "| `packages/example.ts` | `example.go` | ✅ | 0 | 0 | 1 | 0 | 0 | imported status ✅ |") {
		t.Fatalf("response = %#v", response)
	}
}

func TestExecuteReadsOneValidatedEvidenceRequest(t *testing.T) {
	root := t.TempDir()
	hash := closure.HashBytes([]byte("fixture"))
	commit := strings.Repeat("a", 40)
	upstreamCommit := strings.Repeat("b", 40)
	snapshot := &closure.Snapshot{Kind: closure.KindSnapshot, ID: "snapshot:evidence", UpstreamCommit: upstreamCommit, TargetCommit: commit, ToolchainHash: hash, EnvironmentHash: hash}
	pin := &closure.Pin{Kind: closure.KindPin, ID: "pin:evidence", SnapshotID: snapshot.ID, Repository: "pig", Commit: commit, Path: "evidence.go", SemanticID: "evidence", StartLine: 1, EndLine: 1, QuoteHash: hash}
	behavior := &closure.Behavior{Kind: closure.KindBehavior, ID: "behavior:evidence", Name: "evidence", OriginPinIDs: []string{pin.ID}, Profile: "application"}
	facet := &closure.Facet{Kind: closure.KindFacet, ID: "facet:result", Name: "result"}
	rule := &closure.Rule{Kind: closure.KindRule, ID: "rule:evidence", Name: "evidence", DefinitionHash: hash}
	obligation := &closure.Obligation{Kind: closure.KindObligation, ID: "obligation:evidence", BehaviorID: behavior.ID, FacetID: facet.ID, RuleID: rule.ID, OriginPinIDs: []string{pin.ID}}
	target := &closure.Target{Kind: closure.KindTarget, ID: "target:evidence", SnapshotID: snapshot.ID, PinIDs: []string{pin.ID}, Language: "go", Symbol: "evidence"}
	decision := &closure.Decision{Kind: closure.KindDecision, ID: "decision:evidence", DecisionType: "mapping", ScopeIDs: []string{behavior.ID}, Rationale: "exact evidence target", Authority: "reviewer"}
	mapping := &closure.Mapping{Kind: closure.KindMapping, ID: "mapping:evidence", BehaviorID: behavior.ID, TargetIDs: []string{target.ID}, Status: "decided", DecisionID: decision.ID}
	reachability := &closure.Reachability{Kind: closure.KindReachability, ID: "reachability:evidence", BehaviorID: behavior.ID, TargetID: target.ID, Class: "prod-reachable", Method: "test", RootPinIDs: []string{pin.ID}}
	test := &closure.Test{Kind: closure.KindTest, ID: "test:evidence", SnapshotID: snapshot.ID, PinID: pin.ID, FixturePinIDs: []string{}, DefinitionHash: hash}
	assertion := &closure.Assertion{Kind: closure.KindAssertion, ID: "assertion:evidence", TestID: test.ID, Class: "A2", BehaviorID: behavior.ID, FacetID: facet.ID, Oracle: "contract"}
	request := &closure.EvidenceRequest{Kind: closure.KindEvidenceRequest, ID: "request:evidence", SnapshotID: snapshot.ID, ObligationIDs: []string{obligation.ID}, AssertionIDs: []string{assertion.ID}, Commands: []closure.EvidenceCommand{{Name: "go", Args: []string{"test"}}}, Environment: []string{}, Witnesses: []closure.EvidenceWitnessRequest{}, Durability: 1, Comparator: "exit-zero", TimeoutSeconds: 1}
	edit := closure.MutationEdit{Path: pin.Path, OriginalHash: hash, Before: "before", After: "after", MutatedHash: closure.HashBytes([]byte("mutated"))}
	mutant, err := closure.NewMutant(snapshot.ID, obligation.ID, test.ID, target.ID, pin.ID, "result", "automatic", "change-value", edit, "TestEvidence", closure.HashBytes([]byte("failure")))
	if err != nil {
		t.Fatal(err)
	}
	mutationRequest, err := closure.NewMutationRequest(snapshot.ID, obligation.ID, test.ID, []*closure.Mutant{mutant}, []closure.EvidenceCommand{{Name: "go", Args: []string{"test", "-json", "./...", "-run", "^TestEvidence$"}}}, []string{}, 30)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := closure.Build([]closure.Record{snapshot, pin, behavior, facet, rule, obligation, target, decision, mapping, reachability, test, assertion, request, mutant, mutationRequest})
	if err != nil {
		t.Fatal(err)
	}
	if err := closure.RebuildStore(context.Background(), filepath.Join(root, "closure.db"), graph); err != nil {
		t.Fatal(err)
	}
	response, err := Execute(context.Background(), Request{Operation: OperationRequestEvidence, Root: root, Database: "closure.db", RecordID: request.ID})
	if err != nil {
		t.Fatal(err)
	}
	if response.Operation != OperationRequestEvidence || !strings.Contains(response.Output, `"id": "request:evidence"`) {
		t.Fatalf("response = %#v", response)
	}
	mutationResponse, err := Execute(context.Background(), Request{Operation: OperationRequestMutation, Root: root, Database: "closure.db", RecordID: mutationRequest.ID})
	if err != nil {
		t.Fatal(err)
	}
	if mutationResponse.Operation != OperationRequestMutation || !strings.Contains(mutationResponse.Output, mutationRequest.ID) {
		t.Fatalf("mutation response = %#v", mutationResponse)
	}
}

func TestExecutePlanUnitsReturnsDerivedWorkUnits(t *testing.T) {
	root := t.TempDir()
	h := func(v string) string { return closure.HashBytes([]byte(v)) }
	snapshot := &closure.Snapshot{Kind: closure.KindSnapshot, ID: "snapshot:plan", UpstreamCommit: strings.Repeat("b", 40), TargetCommit: strings.Repeat("a", 40), ToolchainHash: h("t"), EnvironmentHash: h("e")}
	originPin := &closure.Pin{Kind: closure.KindPin, ID: "pin:origin", SnapshotID: snapshot.ID, Repository: "upstream", Commit: snapshot.UpstreamCommit, Path: "packages/x/wire.ts", SemanticID: "wire:shape", StartLine: 1, EndLine: 1, QuoteHash: h("q")}
	prodPin := &closure.Pin{Kind: closure.KindPin, ID: "pin:prod", SnapshotID: snapshot.ID, Repository: "pig", Commit: snapshot.TargetCommit, Path: "ai/openai.go", SemanticID: "sym.wire", StartLine: 1, EndLine: 1, QuoteHash: h("p"), APIHash: h("a"), BodyHash: h("b")}
	target := &closure.Target{Kind: closure.KindTarget, ID: "target:wire", SnapshotID: snapshot.ID, PinIDs: []string{prodPin.ID}, Language: "go", Symbol: "sym.wire"}
	behavior := &closure.Behavior{Kind: closure.KindBehavior, ID: "behavior:wire", Name: "wire", OriginPinIDs: []string{originPin.ID}, Profile: "application"}
	facet := &closure.Facet{Kind: closure.KindFacet, ID: "facet:wire", Name: "wire"}
	rule := &closure.Rule{Kind: closure.KindRule, ID: "rule:wire", Name: "wire", DefinitionHash: h("r")}
	obligation := &closure.Obligation{Kind: closure.KindObligation, ID: "obligation:wire", BehaviorID: behavior.ID, FacetID: facet.ID, RuleID: rule.ID, OriginPinIDs: []string{originPin.ID}}
	reachability := &closure.Reachability{Kind: closure.KindReachability, ID: "reachability:wire", BehaviorID: behavior.ID, TargetID: target.ID, Class: "prod-reachable", Method: "production-call-path", RootPinIDs: []string{prodPin.ID}}
	graph, err := closure.Build([]closure.Record{snapshot, originPin, prodPin, target, behavior, facet, rule, obligation, reachability})
	if err != nil {
		t.Fatal(err)
	}
	database := "closure.db"
	if err := closure.RebuildStore(context.Background(), filepath.Join(root, database), graph); err != nil {
		t.Fatal(err)
	}
	response, err := Execute(context.Background(), Request{Operation: OperationPlanUnits, Root: root, Database: database})
	if err != nil {
		t.Fatalf("Execute(plan-units) = %v", err)
	}
	if response.Operation != OperationPlanUnits {
		t.Fatalf("operation = %s", response.Operation)
	}
	for _, want := range []string{"behavior:wire", "ai/openai.go", "wire:shape", "writePaths", "semanticBoundaryIds"} {
		if !strings.Contains(response.Output, want) {
			t.Fatalf("plan-units output missing %q:\n%s", want, response.Output)
		}
	}
}

func TestPlanUnitsRejectsDisallowedFields(t *testing.T) {
	if err := (Request{Operation: OperationPlanUnits, RecordID: "x"}).Validate(); err == nil || !strings.Contains(err.Error(), "recordId") {
		t.Fatalf("plan-units recordId error = %v", err)
	}
	if err := (Request{Operation: OperationPlanUnits, Dataset: "port-map"}).Validate(); err == nil {
		t.Fatalf("plan-units dataset should be rejected")
	}
}

func leaseFixtureStore(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	h := func(v string) string { return closure.HashBytes([]byte(v)) }
	snapshot := &closure.Snapshot{Kind: closure.KindSnapshot, ID: "snapshot:lease", UpstreamCommit: strings.Repeat("b", 40), TargetCommit: strings.Repeat("a", 40), ToolchainHash: h("t"), EnvironmentHash: h("e")}
	originPin := &closure.Pin{Kind: closure.KindPin, ID: "pin:origin", SnapshotID: snapshot.ID, Repository: "upstream", Commit: snapshot.UpstreamCommit, Path: "packages/x/wire.ts", SemanticID: "wire:shape", StartLine: 1, EndLine: 1, QuoteHash: h("q")}
	prodPin := &closure.Pin{Kind: closure.KindPin, ID: "pin:prod", SnapshotID: snapshot.ID, Repository: "pig", Commit: snapshot.TargetCommit, Path: "ai/openai.go", SemanticID: "sym.wire", StartLine: 1, EndLine: 1, QuoteHash: h("p"), APIHash: h("a"), BodyHash: h("b")}
	target := &closure.Target{Kind: closure.KindTarget, ID: "target:wire", SnapshotID: snapshot.ID, PinIDs: []string{prodPin.ID}, Language: "go", Symbol: "sym.wire"}
	behavior := &closure.Behavior{Kind: closure.KindBehavior, ID: "behavior:wire", Name: "wire", OriginPinIDs: []string{originPin.ID}, Profile: "application"}
	facet := &closure.Facet{Kind: closure.KindFacet, ID: "facet:wire", Name: "wire"}
	rule := &closure.Rule{Kind: closure.KindRule, ID: "rule:wire", Name: "wire", DefinitionHash: h("r")}
	obligation := &closure.Obligation{Kind: closure.KindObligation, ID: "obligation:wire", BehaviorID: behavior.ID, FacetID: facet.ID, RuleID: rule.ID, OriginPinIDs: []string{originPin.ID}}
	reachability := &closure.Reachability{Kind: closure.KindReachability, ID: "reachability:wire", BehaviorID: behavior.ID, TargetID: target.ID, Class: "prod-reachable", Method: "production-call-path", RootPinIDs: []string{prodPin.ID}}
	graph, err := closure.Build([]closure.Record{snapshot, originPin, prodPin, target, behavior, facet, rule, obligation, reachability})
	if err != nil {
		t.Fatal(err)
	}
	database := "closure.db"
	if err := closure.RebuildStore(context.Background(), filepath.Join(root, database), graph); err != nil {
		t.Fatal(err)
	}
	units, err := closure.PlanWorkUnits(graph)
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 1 {
		t.Fatalf("expected 1 derived work unit, got %d", len(units))
	}
	return root, database, units[0].ID
}

func TestExecuteGrantLeaseThenIntegrate(t *testing.T) {
	root, database, workUnitID := leaseFixtureStore(t)
	grant, err := Execute(context.Background(), Request{Operation: OperationGrantLease, Root: root, Database: database, WorkUnitID: workUnitID, Holder: "worker-1"})
	if err != nil {
		t.Fatalf("Execute(grant-lease) = %v", err)
	}
	var lease struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(grant.Output), &lease); err != nil {
		t.Fatalf("grant output: %v\n%s", err, grant.Output)
	}
	if !strings.HasPrefix(lease.ID, "lease:") || lease.State != "active" {
		t.Fatalf("granted lease = %#v", lease)
	}

	// Granting the same unit again is rejected by the overlap-admission gate.
	if _, err := Execute(context.Background(), Request{Operation: OperationGrantLease, Root: root, Database: database, WorkUnitID: workUnitID, Holder: "worker-2"}); err == nil || !strings.Contains(err.Error(), "already leased") {
		t.Fatalf("second grant error = %v", err)
	}

	integrate, err := Execute(context.Background(), Request{Operation: OperationIntegrate, Root: root, Database: database, RecordID: lease.ID})
	if err != nil {
		t.Fatalf("Execute(integrate) = %v", err)
	}
	var result struct {
		LeaseID     string `json:"leaseId"`
		State       string `json:"state"`
		DeltaBriefs []any  `json:"deltaBriefs"`
	}
	if err := json.Unmarshal([]byte(integrate.Output), &result); err != nil {
		t.Fatalf("integrate output: %v\n%s", err, integrate.Output)
	}
	if result.LeaseID != lease.ID || result.State != "integrated" || len(result.DeltaBriefs) != 0 {
		t.Fatalf("integrate result = %#v", result)
	}

	// An integrated lease no longer contends: the unit can be granted again.
	if _, err := Execute(context.Background(), Request{Operation: OperationGrantLease, Root: root, Database: database, WorkUnitID: workUnitID, Holder: "worker-3"}); err != nil {
		t.Fatalf("regrant after integrate = %v", err)
	}
	// Re-integrating the now-inactive lease is rejected.
	if _, err := Execute(context.Background(), Request{Operation: OperationIntegrate, Root: root, Database: database, RecordID: lease.ID}); err == nil || !strings.Contains(err.Error(), "not active") {
		t.Fatalf("re-integrate error = %v", err)
	}
}

func TestGrantLeaseAndIntegrateValidation(t *testing.T) {
	cases := []struct {
		name string
		req  Request
		want string
	}{
		{"grant needs work unit and holder", Request{Operation: OperationGrantLease, Holder: "w"}, "workUnitId and holder"},
		{"grant rejects dataset", Request{Operation: OperationGrantLease, WorkUnitID: "u", Holder: "w", Dataset: "port-map"}, "dataset"},
		{"integrate needs lease recordId", Request{Operation: OperationIntegrate, RecordID: "request:x"}, "lease recordId"},
		{"integrate rejects bare prefix", Request{Operation: OperationIntegrate, RecordID: "lease:"}, "lease recordId"},
		{"work unit only on grant", Request{Operation: OperationStatus, WorkUnitID: "u"}, "does not accept workUnitId"},
		{"holder only on grant or campaign", Request{Operation: OperationPlanUnits, Holder: "w"}, "does not accept holder"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.req.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestExecuteCampaignStepDrivesFrontierAndResumes(t *testing.T) {
	root, database, _ := leaseFixtureStore(t)
	ctx := context.Background()
	req := Request{Operation: OperationCampaignStep, Root: root, Database: database, UpstreamVersion: coding.UpstreamVersion, TargetCommit: strings.Repeat("a", 40), Holder: "worker-1"}

	first, err := Execute(ctx, req)
	if err != nil {
		t.Fatalf("Execute(campaign-step) = %v", err)
	}
	var firstResult struct {
		GrantedLeases []struct {
			ID    string `json:"ID"`
			State string `json:"State"`
		} `json:"GrantedLeases"`
	}
	if err := json.Unmarshal([]byte(first.Output), &firstResult); err != nil {
		t.Fatalf("campaign-step output: %v\n%s", err, first.Output)
	}
	if len(firstResult.GrantedLeases) != 1 || firstResult.GrantedLeases[0].State != "active" {
		t.Fatalf("first campaign step granted = %#v", firstResult.GrantedLeases)
	}

	// The single unit is now held; a second step grants nothing.
	second, err := Execute(ctx, req)
	if err != nil {
		t.Fatalf("Execute(campaign-step) second = %v", err)
	}
	var secondResult struct {
		GrantedLeases []any `json:"GrantedLeases"`
		Plan          struct {
			Held []string `json:"Held"`
		} `json:"Plan"`
	}
	if err := json.Unmarshal([]byte(second.Output), &secondResult); err != nil {
		t.Fatal(err)
	}
	if len(secondResult.GrantedLeases) != 0 || len(secondResult.Plan.Held) != 1 {
		t.Fatalf("second campaign step = %s", second.Output)
	}
}

func TestCampaignStepRequestValidation(t *testing.T) {
	root, database, _ := leaseFixtureStore(t)
	base := Request{Operation: OperationCampaignStep, Root: root, Database: database, UpstreamVersion: coding.UpstreamVersion, TargetCommit: strings.Repeat("a", 40), Holder: "worker-1"}
	// Missing holder.
	missing := base
	missing.Holder = ""
	if _, err := Execute(context.Background(), missing); err == nil || !strings.Contains(err.Error(), "requires holder") {
		t.Fatalf("missing holder error = %v", err)
	}
	// workUnitId is grant-lease only.
	withUnit := base
	withUnit.WorkUnitID = "work-unit:x"
	if _, err := Execute(context.Background(), withUnit); err == nil || !strings.Contains(err.Error(), "does not accept workUnitId") {
		t.Fatalf("workUnitId rejection = %v", err)
	}
	// Wrong upstream version.
	badVersion := base
	badVersion.UpstreamVersion = "0.1.0"
	if _, err := Execute(context.Background(), badVersion); err == nil || !strings.Contains(err.Error(), "does not match pinned") {
		t.Fatalf("bad version error = %v", err)
	}
}
