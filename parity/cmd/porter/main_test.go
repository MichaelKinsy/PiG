package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/parity/closure"
	"github.com/MichaelKinsy/PiG/parity/porter"
)

func TestRunRejectsMalformedRequest(t *testing.T) {
	var output bytes.Buffer
	if err := run(context.Background(), strings.NewReader(`{"operation":"status","extra":true}`), &output); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("run() error = %v", err)
	}
}

func TestRunRequiresExistingStore(t *testing.T) {
	var output bytes.Buffer
	if err := run(context.Background(), strings.NewReader(`{"operation":"status","root":"/missing","database":"closure.db"}`), &output); err == nil {
		t.Fatal("run() accepted a missing root")
	}
	if output.Len() != 0 {
		t.Fatalf("run() wrote output on failure: %s", output.String())
	}
}

func TestRunUsesTheSharedPorterResponseContract(t *testing.T) {
	root := t.TempDir()
	hash := closure.HashBytes([]byte("fixture"))
	pin := &closure.Pin{Kind: closure.KindPin, ID: "pin:run", SnapshotID: "snapshot:run", Repository: "pig", Commit: strings.Repeat("a", 40), Path: "run.go", SemanticID: "run", StartLine: 1, EndLine: 1, QuoteHash: hash}
	snapshot := &closure.Snapshot{Kind: closure.KindSnapshot, ID: "snapshot:run", UpstreamCommit: strings.Repeat("b", 40), TargetCommit: strings.Repeat("a", 40), ToolchainHash: hash, EnvironmentHash: hash}
	behavior := &closure.Behavior{Kind: closure.KindBehavior, ID: "behavior:run", Name: "run", OriginPinIDs: []string{pin.ID}, Profile: "application"}
	graph, err := closure.Build([]closure.Record{snapshot, pin, behavior})
	if err != nil {
		t.Fatal(err)
	}
	if err := closure.RebuildStore(context.Background(), filepath.Join(root, "closure.db"), graph); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	request, err := json.Marshal(porter.Request{Operation: porter.OperationStatus, Root: root, Database: "closure.db"})
	if err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), bytes.NewReader(request), &output); err != nil {
		t.Fatal(err)
	}
	var response porter.Response
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Operation != porter.OperationStatus || !strings.Contains(response.Output, "open") {
		t.Fatalf("response = %#v", response)
	}
}
