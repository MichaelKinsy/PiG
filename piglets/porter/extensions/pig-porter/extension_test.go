package pigporter

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/parity/closure"
	"github.com/MichaelKinsy/PiG/parity/porter"
)

func TestExtensionIdentityMatchesPigletSelection(t *testing.T) {
	if got := Extension().Name(); got != "pig-porter" {
		t.Fatalf("extension name = %q, want pig-porter", got)
	}
}

func TestLinkedContextPropagatesCancellationAndStops(t *testing.T) {
	done := make(chan struct{})
	requestCtx, stop := linkedContext(done)
	close(done)
	select {
	case <-requestCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("linked context did not cancel")
	}
	stopped := make(chan struct{})
	go func() {
		stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("linked context watcher did not stop")
	}
}

func TestLinkedContextWithoutHostCancellationCanStop(t *testing.T) {
	requestCtx, stop := linkedContext(nil)
	stop()
	select {
	case <-requestCtx.Done():
	default:
		t.Fatal("stop did not cancel request context")
	}
}

func TestToolUsesTheSameResponseAsTheDirectPorterAdapter(t *testing.T) {
	root := t.TempDir()
	hash := closure.HashBytes([]byte("fixture"))
	pin := &closure.Pin{Kind: closure.KindPin, ID: "pin:extension", SnapshotID: "snapshot:extension", Repository: "pig", Commit: strings.Repeat("a", 40), Path: "extension.go", SemanticID: "extension", StartLine: 1, EndLine: 1, QuoteHash: hash}
	snapshot := &closure.Snapshot{Kind: closure.KindSnapshot, ID: "snapshot:extension", UpstreamCommit: strings.Repeat("b", 40), TargetCommit: strings.Repeat("a", 40), ToolchainHash: hash, EnvironmentHash: hash}
	behavior := &closure.Behavior{Kind: closure.KindBehavior, ID: "behavior:extension", Name: "extension", OriginPinIDs: []string{pin.ID}, Profile: "application"}
	facet := &closure.Facet{Kind: closure.KindFacet, ID: "facet:extension-result", Name: "result"}
	rule := &closure.Rule{Kind: closure.KindRule, ID: "rule:extension", Name: "extension", DefinitionHash: hash}
	obligation := &closure.Obligation{Kind: closure.KindObligation, ID: "obligation:extension", BehaviorID: behavior.ID, FacetID: facet.ID, RuleID: rule.ID, OriginPinIDs: []string{pin.ID}}
	target := &closure.Target{Kind: closure.KindTarget, ID: "target:extension", SnapshotID: snapshot.ID, PinIDs: []string{pin.ID}, Language: "go", Symbol: "extension"}
	decision := &closure.Decision{Kind: closure.KindDecision, ID: "decision:extension", DecisionType: "mapping", ScopeIDs: []string{behavior.ID}, Rationale: "exact extension target", Authority: "reviewer"}
	mapping := &closure.Mapping{Kind: closure.KindMapping, ID: "mapping:extension", BehaviorID: behavior.ID, TargetIDs: []string{target.ID}, Status: "decided", DecisionID: decision.ID}
	reachability := &closure.Reachability{Kind: closure.KindReachability, ID: "reachability:extension", BehaviorID: behavior.ID, TargetID: target.ID, Class: "prod-reachable", Method: "test", RootPinIDs: []string{pin.ID}}
	testRecord := &closure.Test{Kind: closure.KindTest, ID: "test:extension", SnapshotID: snapshot.ID, PinID: pin.ID, FixturePinIDs: []string{}, DefinitionHash: hash, MutationPolicy: "required"}
	assertion := &closure.Assertion{Kind: closure.KindAssertion, ID: "assertion:extension", TestID: testRecord.ID, Class: "A2", BehaviorID: behavior.ID, FacetID: facet.ID, Oracle: "contract"}
	edit := closure.MutationEdit{Path: pin.Path, OriginalHash: hash, Before: "before", After: "after", MutatedHash: closure.HashBytes([]byte("mutated"))}
	mutant, err := closure.NewMutant(snapshot.ID, obligation.ID, testRecord.ID, target.ID, pin.ID, "result", "automatic", "change-value", edit, "TestExtension", closure.HashBytes([]byte("failure")))
	if err != nil {
		t.Fatal(err)
	}
	mutationRequest, err := closure.NewMutationRequest(snapshot.ID, obligation.ID, testRecord.ID, []*closure.Mutant{mutant}, []closure.EvidenceCommand{{Name: "go", Args: []string{"test", "-json", "./...", "-run", "^TestExtension$"}}}, []string{}, 30)
	if err != nil {
		t.Fatal(err)
	}
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
	graph, err := closure.Build([]closure.Record{snapshot, pin, behavior, facet, rule, obligation, target, decision, mapping, reachability, testRecord, assertion, mutant, mutationRequest, order, fact, claim})
	if err != nil {
		t.Fatal(err)
	}
	if err := closure.RebuildStore(context.Background(), filepath.Join(root, "closure.db"), graph); err != nil {
		t.Fatal(err)
	}
	request := porter.Request{Operation: porter.OperationStatus, Root: root, Database: "closure.db"}
	direct, err := porter.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	params := map[string]any{"operation": request.Operation, "root": request.Root, "database": request.Database}
	toolResult, err := runTool(sdk.Context{}, params)
	if err != nil {
		t.Fatal(err)
	}
	extensionResponse, ok := toolResult.(porter.Response)
	if !ok {
		t.Fatalf("tool result type = %T", toolResult)
	}
	if !reflect.DeepEqual(extensionResponse, direct) {
		t.Fatalf("extension response = %#v, direct response = %#v", extensionResponse, direct)
	}
	directJSON, err := json.Marshal(direct)
	if err != nil {
		t.Fatal(err)
	}
	extensionJSON, err := json.Marshal(extensionResponse)
	if err != nil {
		t.Fatal(err)
	}
	if string(extensionJSON) != string(directJSON) {
		t.Fatalf("extension JSON = %s, direct JSON = %s", extensionJSON, directJSON)
	}

	reportRequest := porter.Request{Operation: porter.OperationReport, Root: root, Database: "closure.db", Dataset: closure.PortMapDataset}
	directReport, err := porter.Execute(context.Background(), reportRequest)
	if err != nil {
		t.Fatal(err)
	}
	reportParams := map[string]any{"operation": reportRequest.Operation, "root": reportRequest.Root, "database": reportRequest.Database, "dataset": reportRequest.Dataset}
	reportToolResult, err := runTool(sdk.Context{}, reportParams)
	if err != nil {
		t.Fatal(err)
	}
	reportResponse, ok := reportToolResult.(porter.Response)
	if !ok {
		t.Fatalf("report tool result type = %T", reportToolResult)
	}
	if !reflect.DeepEqual(reportResponse, directReport) {
		t.Fatalf("report extension response = %#v, direct response = %#v", reportResponse, directReport)
	}
	reportDirectJSON, err := json.Marshal(directReport)
	if err != nil {
		t.Fatal(err)
	}
	reportExtensionJSON, err := json.Marshal(reportResponse)
	if err != nil {
		t.Fatal(err)
	}
	if string(reportExtensionJSON) != string(reportDirectJSON) {
		t.Fatalf("report extension JSON = %s, direct JSON = %s", reportExtensionJSON, reportDirectJSON)
	}
	readinessRequest := porter.Request{Operation: porter.OperationMutationReadiness, Root: root, Database: "closure.db"}
	directReadiness, err := porter.Execute(context.Background(), readinessRequest)
	if err != nil {
		t.Fatal(err)
	}
	readinessResult, err := runTool(sdk.Context{}, map[string]any{"operation": readinessRequest.Operation, "root": readinessRequest.Root, "database": readinessRequest.Database})
	if err != nil {
		t.Fatal(err)
	}
	readinessResponse, ok := readinessResult.(porter.Response)
	if !ok || !reflect.DeepEqual(readinessResponse, directReadiness) {
		t.Fatalf("mutation readiness extension response = %#v, direct response = %#v", readinessResult, directReadiness)
	}
	mutationRequestEnvelope := porter.Request{Operation: porter.OperationRequestMutation, Root: root, Database: "closure.db", RecordID: mutationRequest.ID}
	directMutationRequest, err := porter.Execute(context.Background(), mutationRequestEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	mutationRequestResult, err := runTool(sdk.Context{}, map[string]any{"operation": mutationRequestEnvelope.Operation, "root": mutationRequestEnvelope.Root, "database": mutationRequestEnvelope.Database, "recordId": mutationRequestEnvelope.RecordID})
	if err != nil {
		t.Fatal(err)
	}
	mutationRequestResponse, ok := mutationRequestResult.(porter.Response)
	if !ok || !reflect.DeepEqual(mutationRequestResponse, directMutationRequest) {
		t.Fatalf("mutation request extension response = %#v, direct response = %#v", mutationRequestResult, directMutationRequest)
	}
}
