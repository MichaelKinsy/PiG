package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	sessiontesting "github.com/MichaelKinsy/PiG/agent/harness/session/testing"
	"github.com/MichaelKinsy/PiG/ai"
)

func isolateHarnessTest(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"HOME", "USERPROFILE", "PIG_HOME", "PI_HOME", "PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(name, filepath.Join(root, name))
	}
}

func requireHarnessEqual[T any](t *testing.T, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
}

func requireHarnessValue[T any](t *testing.T, value T, err error) T {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func requireHarnessOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func readHarnessValue[T any](t *testing.T, reader session.SessionReader, address session.Value[T]) *session.StoredValue[T] {
	t.Helper()
	value, err := session.GetValue(context.Background(), reader, address)
	return requireHarnessValue(t, value, err)
}

func readHarnessEntry(t *testing.T, opened session.Session, id string) *session.Entry {
	t.Helper()
	entry, err := opened.GetEntry(context.Background(), id)
	requireHarnessOK(t, err)
	if entry == nil {
		t.Fatalf("entry %q is absent", id)
	}
	return entry
}

func scanHarnessBranch(t *testing.T, opened session.Session, tip string) []session.Entry {
	t.Helper()
	entries, err := opened.ScanBranch(context.Background(), session.StorageBranchScan{Start: tip, Order: session.OrderOldestFirst})
	return requireHarnessValue(t, entries, err)
}

func harnessEntryIDs(entries []session.Entry) []string {
	ids := make([]string, len(entries))
	for i, entry := range entries {
		ids[i] = entry.ID
	}
	return ids
}

func requireRunOperation(t *testing.T, lane *Lane) *session.Operation {
	t.Helper()
	operation := lane.SnapshotState().Operation
	if operation == nil || operation.Meta.Intent.Kind != "run" || operation.State.At != session.AtStarting {
		t.Fatalf("expected accepted run, got %#v", operation)
	}
	return operation
}

func harnessCommit(t *testing.T, opened session.Session, writes ...session.Write) {
	t.Helper()
	_, err := opened.Mutate(context.Background(), func(ctx context.Context, mutator session.SessionMutator) (any, error) {
		return mutator.Commit(ctx, writes)
	})
	requireHarnessOK(t, err)
}

func harnessSession(t *testing.T, id string, storage session.Storage) *session.StorageBackedSession {
	t.Helper()
	if storage == nil {
		storage = session.NewMemoryStorage(nil)
	}
	opened := session.NewStorageBackedSession(session.SessionMetadata{ID: id, CreatedAt: 1, StorageVersion: 1}, storage, nil)
	t.Cleanup(func() { requireHarnessOK(t, opened.Close(context.Background())) })
	return opened
}

func fauxHarnessOptions(opened session.Session, responses ...string) AgentHarnessOptions {
	faux := newLaneFaux()
	steps := make([]ai.FauxResponseStep, len(responses))
	for i, text := range responses {
		steps[i] = laneFauxResponse(text)
	}
	faux.setResponses(steps)
	return AgentHarnessOptions{Session: opened, Models: faux.models, Model: faux.model, ThinkingLevel: "medium", ActiveToolNames: []string{"read", "bash"}}
}

func attachedHarness(t *testing.T, opened session.Session) *Harness {
	t.Helper()
	if opened == nil {
		opened = harnessSession(t, "session-0", nil)
	}
	created, err := CreateAgentHarness(context.Background(), fauxHarnessOptions(opened))
	requireHarnessOK(t, err)
	t.Cleanup(func() { requireHarnessOK(t, created.Harness.Close(context.Background())) })
	return created.Harness
}

func acquireHarnessLane(t *testing.T, owner *Harness, name string) *Lane {
	t.Helper()
	lane, err := owner.Lane(context.Background(), name, nil)
	return requireHarnessValue(t, lane, err)
}

func listenHarness(t *testing.T, owner *Harness, kind agentharness.HarnessEventType, listener agentharness.EventListener) {
	t.Helper()
	unsubscribe, err := owner.Events().On(kind, listener)
	requireHarnessOK(t, err)
	t.Cleanup(unsubscribe)
}

var acceptanceConfiguration = session.LaneConfiguration{Model: session.ModelRef{Provider: "faux", ModelID: "faux-1"}, ThinkingLevel: "off", ActiveToolNames: []string{}}

func seedAcceptance(t *testing.T, opened session.Session) {
	t.Helper()
	harnessCommit(t, opened, session.SetValue(session.BranchTip("main"), (*string)(nil)), session.SetValue(session.LaneConfig("main"), acceptanceConfiguration), session.SetValue(session.LaneStateValue("main"), session.LaneState{Inbox: []session.InboxItem{}}))
}

type admissionFixture struct {
	owner   *Harness
	lane    *Lane
	session *session.StorageBackedSession
	storage *sessiontesting.InstrumentedStorage
	models  *observedHarnessModels
}

type observedHarnessModels struct {
	*ai.Models
	lookups atomic.Int64
}

func (models *observedHarnessModels) GetModel(provider, id string) *ai.Model {
	models.lookups.Add(1)
	return models.Models.GetModel(provider, id)
}

func newAdmissionFixture(t *testing.T, before func(session.Session), configure func(*AgentHarnessOptions), ids ...string) admissionFixture {
	t.Helper()
	storage := sessiontesting.NewInstrumentedStorage(session.NewMemoryStorage(nil))
	id := "accept-0"
	if len(ids) > 0 {
		id = ids[0]
	}
	opened := harnessSession(t, id, storage)
	seedAcceptance(t, opened)
	if before != nil {
		before(opened)
	}
	options := fauxHarnessOptions(opened)
	models := &observedHarnessModels{Models: options.Models.(*ai.Models)}
	options.Models = models
	if configure != nil {
		configure(&options)
	}
	created, err := CreateAgentHarness(context.Background(), options)
	requireHarnessOK(t, err)
	owner := created.Harness
	t.Cleanup(func() { requireHarnessOK(t, owner.Close(context.Background())) })
	lane := acquireHarnessLane(t, owner, "main")
	storage.ClearCommitAttempts()
	return admissionFixture{owner: owner, lane: lane, session: opened, storage: storage, models: models}
}

func acceptRequest(t *testing.T, lane *Lane, request agentharness.OperationRequest) agentharness.OperationAdmission {
	t.Helper()
	value, err := lane.Accept(context.Background(), request)
	return requireHarnessValue(t, value, err)
}

func promptRequest(text string) agentharness.OperationRequest {
	return agentharness.OperationRequest{Kind: agentharness.RequestPrompt, PromptText: &text}
}

func requireAdmissionError(t *testing.T, err error, tag, reason string) {
	t.Helper()
	var tagged harness.TaggedError
	if !errors.As(err, &tagged) || tagged.Tag() != tag {
		t.Fatalf("error = %v; want %s", err, tag)
	}
	if reason == "" {
		return
	}
	switch typed := tagged.(type) {
	case *harness.InvalidMessage:
		requireHarnessEqual(t, typed.Reason, reason)
	case *harness.InvalidNavigation:
		requireHarnessEqual(t, typed.Reason, reason)
	default:
		t.Fatalf("error %T has no asserted reason", tagged)
	}
}

func acceptanceWriteKinds(writes []session.Write) []string {
	kinds := make([]string, len(writes))
	for index, write := range writes {
		switch write.(type) {
		case session.EntryWrite:
			kinds[index] = "entry"
		case session.ValueSetWrite:
			kinds[index] = "value:set"
		case session.ValueDeleteWrite:
			kinds[index] = "value:delete"
		default:
			kinds[index] = "unexpected"
		}
	}
	return kinds
}
