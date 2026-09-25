package conformance

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	sessiontesting "github.com/MichaelKinsy/PiG/agent/harness/session/testing"
	"github.com/MichaelKinsy/PiG/ai"
)

// RepoAdapter binds one backend repository to the methods the session-repo
// groups exercise, so partial backends can run only the groups they support
// and backend-specific create/list options stay in the adapter.
type RepoAdapter struct {
	Create func(ctx context.Context, options session.SessionCreateOptions) (session.Session, error)
	Open   func(ctx context.Context, metadata session.SessionMetadata) (session.Session, error)
	List   func(ctx context.Context) ([]session.SessionMetadata, error)
	Delete func(ctx context.Context, metadata session.SessionMetadata) error
	Fork   func(ctx context.Context, source session.SessionMetadata, options session.ForkOptions) (session.Session, error)
}

// RepoFactory creates a fresh adapter for one case.
type RepoFactory func() (RepoAdapter, error)

const (
	rootID      = "00000000-0000-7000-8000-000000000001"
	childID     = "00000000-0000-7000-8000-000000000002"
	siblingID   = "00000000-0000-7000-8000-000000000003"
	usageID     = "00000000-0000-7000-8000-000000000004"
	operationID = "00000000-0000-7000-8000-000000000005"
	pendingID   = "00000000-0000-7000-8000-000000000006"
	unknownID   = "00000000-0000-7000-8000-000000000007"
)

var (
	applicationValue = session.MustValue[session.JsonValue]("test.application.value", "")
	applicationList  = session.MustList[session.JsonValue]("test.application.list", "")
	configuration    = session.LaneConfiguration{
		Model: session.ModelRef{Provider: "provider", ModelID: "model"}, ThinkingLevel: ai.ThinkingOff, ActiveToolNames: []string{"read"},
	}
)

func idleLaneState() session.LaneState { return session.LaneState{Inbox: []session.InboxItem{}} }

type repoTest func(t *testing.T, repo RepoAdapter)

func repoCase(factory RepoFactory, onClose func() error, group, name string, test repoTest) sessiontesting.ConformanceCase {
	return sessiontesting.ConformanceCase{Group: group, Name: name, Run: func(t *testing.T) {
		repo, err := factory()
		if err != nil {
			t.Fatal(err)
		}
		if onClose != nil {
			defer func() {
				if err := onClose(); err != nil {
					t.Error(err)
				}
			}()
		}
		test(t, repo)
	}}
}

type namedRepoTest struct {
	group, name string
	test        repoTest
}

func repoCases(factory RepoFactory, onClose func() error, tests []namedRepoTest) []sessiontesting.ConformanceCase {
	out := make([]sessiontesting.ConformanceCase, 0, len(tests))
	for _, test := range tests {
		out = append(out, repoCase(factory, onClose, test.group, test.name, test.test))
	}
	return out
}

func branchTip(t *testing.T, target session.Session, name string) (*string, bool) {
	t.Helper()
	branch := must(target.Branch(background, name))(t)
	if branch == nil {
		return nil, false
	}
	return must(branch.GetTipID(background))(t), true
}

func assertTip(t *testing.T, target session.Session, name string, want *string) {
	t.Helper()
	tip, ok := branchTip(t, target, name)
	if !ok {
		t.Fatalf("branch %q is absent", name)
	}
	assertJSONEqual(t, tip, want)
}

func assertNoBranch(t *testing.T, target session.Session, name string) {
	t.Helper()
	if _, ok := branchTip(t, target, name); ok {
		t.Fatalf("branch %q exists", name)
	}
}

func assertAbsent[T any](t *testing.T, target session.SessionReader, address session.Value[T]) {
	t.Helper()
	if value := getValue(t, target, address); value != nil {
		t.Fatalf("%s/%s = %#v, want absent", address.Namespace, address.Key, value)
	}
}

func closeAll(t *testing.T, sessions ...session.Session) {
	t.Helper()
	for _, target := range sessions {
		if target != nil {
			if err := target.Close(background); err != nil {
				t.Error(err)
			}
		}
	}
}

func mutateCommit(t *testing.T, target session.Session, writes ...session.Write) session.CommitResult {
	t.Helper()
	return must(session.Mutate(background, target, func(ctx context.Context, mutator session.SessionMutator) (session.CommitResult, error) {
		return mutator.Commit(ctx, writes)
	}))(t)
}

func listedIDs(t *testing.T, repo RepoAdapter) []string {
	t.Helper()
	metadata := must(repo.List(background))(t)
	ids := make([]string, 0, len(metadata))
	for _, item := range metadata {
		ids = append(ids, item.ID)
	}
	slices.Sort(ids)
	return ids
}

func ascEntryIDs(t *testing.T, target session.Session) []string {
	t.Helper()
	return entryIDs(must(target.FindEntries(background, &session.EntryQuery{Order: session.OrderAsc}))(t))
}

func assistantMessage(stopReason ai.StopReason) agent.AgentMessage {
	content := []ai.AssistantContentBlock{ai.TextContent{Text: string(stopReason)}}
	if stopReason == ai.StopReasonToolUse {
		content = []ai.AssistantContentBlock{ai.ToolCall{ID: "call", Name: "read", Arguments: ai.JsonObject{}}}
	}
	message := &agent.AssistantMessage{
		Role: agent.RoleAssistant, Content: content, API: ai.APIAnthropicMessages, Provider: "anthropic",
		ModelID: "claude-sonnet-4-5", Usage: &ai.Usage{}, StopReason: stopReason, Timestamp: 1,
	}
	if stopReason == ai.StopReasonDeferred {
		message.Deferred = &ai.DeferredHandle{Provider: "anthropic", ModelID: "claude-sonnet-4-5", API: ai.APIAnthropicMessages, ID: "job"}
	}
	return agent.AgentMessage{Assistant: message}
}

// CreateSessionRepoLifecycleConformance creates lifecycle cases for
// repositories that support creation, discovery, open, and deletion.
func CreateSessionRepoLifecycleConformance(factory RepoFactory, onClose func() error) []sessiontesting.ConformanceCase {
	return repoCases(factory, onClose, []namedRepoTest{
		{"lifecycle", "creates a session with no implicit branch and rejects duplicate ids", testCreateNoImplicitBranch},
		{"lifecycle", "close drains an acquired scope and rejects a queued mutation callback", testCloseDrainsScope},
		{"lifecycle", "lists metadata and preserves state across close and reopen", testListAndReopen},
		{"lifecycle", "deletes closed sessions without affecting other sessions", testDeleteClosed},
	})
}

func testCreateNoImplicitBranch(t *testing.T, repo RepoAdapter) {
	created := must(repo.Create(background, session.SessionCreateOptions{ID: "session"}))(t)
	metadata := created.Metadata()
	equal(t, metadata.ID, "session")
	if metadata.CreatedAt < 0 || metadata.CreatedAt > 1<<53-1 {
		t.Fatalf("createdAt = %d", metadata.CreatedAt)
	}
	equal(t, metadata.StorageVersion, 1)
	assertNoBranch(t, created, "main")
	assertAbsent(t, created, session.LaneStateValue("main"))
	assertAbsent(t, created, session.LaneConfig("main"))
	_, err := repo.Create(background, session.SessionCreateOptions{ID: "session"})
	mustErr(t, err, "duplicate create")
	closeAll(t, created)
}

func testCloseDrainsScope(t *testing.T, repo RepoAdapter) {
	created := must(repo.Create(background, session.SessionCreateOptions{ID: "session"}))(t)
	active := must(created.BeginMutation(background))(t)
	queuedStarted := false
	queued := make(chan error, 1)
	go func() {
		_, err := created.Mutate(background, func(context.Context, session.SessionMutator) (any, error) {
			queuedStarted = true
			return nil, nil
		})
		queued <- err
	}()
	closing := make(chan error, 1)
	go func() { closing <- created.Close(background) }()
	waitBlocked(t, closing)
	if err := active.End(background); err != nil {
		t.Fatal(err)
	}
	mustErr(t, <-queued, "queued mutation after close")
	if err := <-closing; err != nil {
		t.Fatal(err)
	}
	if queuedStarted {
		t.Fatal("queued callback ran after close")
	}
}

func testListAndReopen(t *testing.T, repo RepoAdapter) {
	first := must(repo.Create(background, session.SessionCreateOptions{ID: "first"}))(t)
	if err := first.SetName(background, new("preserved")); err != nil {
		t.Fatal(err)
	}
	second := must(repo.Create(background, session.SessionCreateOptions{ID: "second", ParentSessionID: "parent"}))(t)
	closeAll(t, second)
	listed := must(repo.List(background))(t)
	type identity struct{ ID, Parent string }
	identities := make([]identity, 0, len(listed))
	for _, item := range listed {
		identities = append(identities, identity{item.ID, item.ParentSessionID})
	}
	slices.SortFunc(identities, func(left, right identity) int { return compareStrings(left.ID, right.ID) })
	assertJSONEqual(t, identities, []identity{{"first", ""}, {"second", "parent"}})
	closeAll(t, first)
	_, err := first.GetName(background)
	mustErr(t, err, "read after close")
	reopened := must(repo.Open(background, first.Metadata()))(t)
	if reopened == first {
		t.Fatal("open returned the closed handle")
	}
	name := must(reopened.GetName(background))(t)
	if name == nil || *name != "preserved" {
		t.Fatalf("name = %v", name)
	}
	closeAll(t, reopened)
}

func compareStrings(left, right string) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func testDeleteClosed(t *testing.T, repo RepoAdapter) {
	removed := must(repo.Create(background, session.SessionCreateOptions{ID: "removed"}))(t)
	retained := must(repo.Create(background, session.SessionCreateOptions{ID: "retained"}))(t)
	closeAll(t, removed, retained)
	if err := repo.Delete(background, removed.Metadata()); err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, listedIDs(t, repo), []string{"retained"})
	_, err := repo.Open(background, removed.Metadata())
	mustErr(t, err, "open deleted session")
	mustErr(t, repo.Delete(background, removed.Metadata()), "delete deleted session")
}

// CreateSessionRepoOwnershipConformance creates exclusive-open cases for
// repositories that own active session handles.
func CreateSessionRepoOwnershipConformance(factory RepoFactory, onClose func() error) []sessiontesting.ConformanceCase {
	return repoCases(factory, onClose, []namedRepoTest{
		{"ownership", "rejects opening an already-open session", testExclusiveOpen},
	})
}

func testExclusiveOpen(t *testing.T, repo RepoAdapter) {
	created := must(repo.Create(background, session.SessionCreateOptions{ID: "session"}))(t)
	_, err := repo.Open(background, created.Metadata())
	mustErr(t, err, "open of an open session")
	closeAll(t, created)
	reopened := must(repo.Open(background, created.Metadata()))(t)
	_, err = repo.Open(background, created.Metadata())
	mustErr(t, err, "second open")
	closeAll(t, reopened)
}

// CreateSessionRepoMessageConformance creates message cases for repositories
// that support session creation.
func CreateSessionRepoMessageConformance(factory RepoFactory, onClose func() error) []sessiontesting.ConformanceCase {
	return repoCases(factory, onClose, []namedRepoTest{
		{"messages", "rejects pending assistant messages without changing the tree", testRejectPendingAssistant},
		{"messages", "preserves every settled assistant stop reason", testSettledStopReasons},
	})
}

func testRejectPendingAssistant(t *testing.T, repo RepoAdapter) {
	created := must(repo.Create(background, session.SessionCreateOptions{ID: "session"}))(t)
	branch := must(created.CreateBranch(background, "main", nil))(t)
	_, err := branch.AppendMessage(background, assistantMessage(ai.StopReasonPending))
	mustErr(t, err, "pending assistant append")
	assertTip(t, created, "main", nil)
	assertJSONEqual(t, must(created.FindEntries(background, nil))(t), []session.Entry{})
	closeAll(t, created)
}

func testSettledStopReasons(t *testing.T, repo RepoAdapter) {
	created := must(repo.Create(background, session.SessionCreateOptions{ID: "session"}))(t)
	branch := must(created.CreateBranch(background, "main", nil))(t)
	var messages []agent.AgentMessage
	var ids []string
	for _, reason := range []ai.StopReason{ai.StopReasonStop, ai.StopReasonLength, ai.StopReasonToolUse, ai.StopReasonError, ai.StopReasonAborted, ai.StopReasonDeferred} {
		message := assistantMessage(reason)
		messages = append(messages, message)
		ids = append(ids, must(branch.AppendMessage(background, message))(t))
	}
	entries := must(created.FindEntries(background, &session.EntryQuery{Order: session.OrderAsc, Type: session.EntryTypeMessage}))(t)
	assertJSONEqual(t, entryIDs(entries), ids)
	for index, entry := range entries {
		assertJSONEqual(t, entry.Message, messages[index])
	}
	assertTip(t, created, "main", &ids[len(ids)-1])
	closeAll(t, created)
}

// CreateSessionRepoForkBehaviorConformance creates fork-content cases that do
// not require concurrent repository coordination.
func CreateSessionRepoForkBehaviorConformance(factory RepoFactory, onClose func() error) []sessiontesting.ConformanceCase {
	return repoCases(factory, onClose, []namedRepoTest{
		{"forks", "tree-forks a fresh session before first attachment", testForkFresh},
		{"forks", "rejects a data-only branch and releases its destination id", testForkDataOnlyBranch},
		{"forks", "forks one named configured branch with scoped values and a zero ledger", testForkNamedBranch},
		{"forks", "enforces branch ancestry for at and before placement", testForkAncestry},
		{"forks", "forks a closed source session", testForkClosedSource},
		{"forks", "forks the whole configured tree with fresh lane state", testForkTree},
		{"forks", "rejects only surviving unknown reserved scalar state", testForkReservedState},
	})
}

func testForkFresh(t *testing.T, repo RepoAdapter) {
	source := must(repo.Create(background, session.SessionCreateOptions{ID: "source"}))(t)
	fork := must(repo.Fork(background, source.Metadata(), session.ForkOptions{ID: "fork", Scope: session.ForkScopeTree}))(t)
	equal(t, fork.Metadata().ID, "fork")
	equal(t, fork.Metadata().ParentSessionID, "source")
	assertNoBranch(t, fork, "main")
	assertAbsent(t, fork, session.LaneConfig("main"))
	assertAbsent(t, fork, session.LaneStateValue("main"))
	assertJSONEqual(t, must(fork.FindEntries(background, nil))(t), []session.Entry{})
	assertJSONEqual(t, must(fork.GetStats(background))(t), zeroStats())
	closeAll(t, source, fork)
}

func testForkDataOnlyBranch(t *testing.T, repo RepoAdapter) {
	source := must(repo.Create(background, session.SessionCreateOptions{ID: "source"}))(t)
	must(source.CreateBranch(background, "data", nil))(t)
	_, err := repo.Fork(background, source.Metadata(), session.ForkOptions{ID: "destination", Scope: session.ForkScopeBranch, Branch: "data"})
	mustErr(t, err, "fork of a data-only branch")
	assertJSONEqual(t, listedIDs(t, repo), []string{"source"})
	destination := must(repo.Create(background, session.SessionCreateOptions{ID: "destination"}))(t)
	closeAll(t, source, destination)
}

func namedBranchSourceWrites() []session.Write {
	return []session.Write{
		session.InsertEntry(bareCustom(rootID, nil, "root")),
		session.InsertEntry(session.Entry{ID: childID, ParentID: new(rootID), Type: session.EntryTypeMessage, Message: userMessage("child", 1)}),
		session.InsertEntry(bareCustom(siblingID, new(rootID), "sibling")),
		session.SetValue(session.BranchTip("main"), new(siblingID)),
		session.SetValue(session.BranchTip("review"), new(childID)),
		session.SetValue(session.LaneConfig("review"), configuration),
		session.SetValue(session.LaneStateValue("review"), session.LaneState{CurrentOperationID: new(operationID), LastOperationID: new("previous"), Inbox: []session.InboxItem{{EntryID: pendingID, Kind: session.InboxWrite}}}),
		session.SetValue(session.OperationResult("previous"), session.OperationResultRecord{OperationID: "previous", Kind: session.OperationKindNavigation, Status: session.TerminalCompleted, FromTipID: new(rootID), TipID: new(childID), StartedAt: 1, EndedAt: 2}),
		session.SetValue(session.SessionName, "source name"),
		session.SetValue(applicationValue, session.JsonValue(map[string]any{"copied": false})),
		session.AppendList(applicationList, session.JsonValue(map[string]any{"copied": false})),
		session.SetValue(session.EntryLabel(rootID), "root label"),
		session.SetValue(session.EntryLabel(siblingID), "sibling label"),
		session.SetValue(session.PendingEntryValue(pendingID), session.PendingEntry{Type: session.PendingEntryCustom, CustomType: "pending"}),
		session.SetValue(session.OperationMetaValue(operationID), session.OperationMeta{OperationID: operationID, Lane: "review", SourceTipID: new(childID), StartedAt: 1, Intent: session.OperationIntent{Kind: session.OperationKindCompaction}}),
		session.SetValue(session.OperationStateValue(operationID), summaryDecidingState()),
		session.SetValue(session.OperationToolArgs(operationID, rootID, 0), map[string]session.JsonValue{"argument": true}),
		session.SetValue(session.OperationPreparation(operationID, operationID), session.DurableStructuralPreparation{
			Kind: session.PreparationCompaction, FileOps: session.DurableFileOperations{},
			Settings: harness.CompactionSettings{Enabled: true, ReserveTokens: 1, KeepRecentTokens: 1},
		}),
		session.InsertUsage(session.UsageRow{ID: usageID, Adjustment: true, Usage: ai.Usage{Input: 1, Output: 2, TotalTokens: 3}}),
	}
}

func summaryDecidingState() session.OperationState {
	return session.OperationState{
		OperationScope: session.OperationScope{
			Control: session.Control{Status: session.ControlRunning},
			Settings: session.RunSettings{
				Compaction:   harness.CompactionSettings{Enabled: true, ReserveTokens: 1, KeepRecentTokens: 1},
				SteeringMode: agent.QueueModeAll, FollowUpMode: agent.QueueModeAll, ToolExecution: session.ToolExecutionSequential,
			},
		},
		At:   session.AtSummaryDeciding,
		Task: session.SummaryTask{TaskID: operationID, Reason: session.SummaryReasonManual, Boundary: session.ResultBoundary{Kind: session.BoundaryFinish}},
	}
}

func testForkNamedBranch(t *testing.T, repo RepoAdapter) {
	source := must(repo.Create(background, session.SessionCreateOptions{ID: "source"}))(t)
	mutateCommit(t, source, namedBranchSourceWrites()...)
	fork := must(repo.Fork(background, source.Metadata(), session.ForkOptions{ID: "fork", Scope: session.ForkScopeBranch, Branch: "review", EntryID: new(childID), Position: session.ForkPositionAt}))(t)
	assertJSONEqual(t, ascEntryIDs(t, fork), []string{rootID, childID})
	assertNoBranch(t, fork, "main")
	assertTip(t, fork, "review", new(childID))
	assertJSONEqual(t, getValue(t, fork, session.LaneConfig("review")).Value, configuration)
	assertJSONEqual(t, getValue(t, fork, session.LaneStateValue("review")).Value, idleLaneState())
	assertAbsent(t, fork, session.LaneConfig("main"))
	assertAbsent(t, fork, session.LaneStateValue("main"))
	assertJSONEqual(t, must(fork.GetName(background))(t), "source name")
	assertAbsent(t, fork, applicationValue)
	assertJSONEqual(t, must(session.ReadList(background, fork, applicationList, nil))(t), []any{})
	assertJSONEqual(t, must(fork.GetLabel(background, rootID))(t), "root label")
	if label := must(fork.GetLabel(background, siblingID))(t); label != nil {
		t.Fatalf("sibling label copied: %q", *label)
	}
	assertAbsent(t, fork, session.OperationResult("previous"))
	assertAbsent(t, fork, session.PendingEntryValue(pendingID))
	assertAbsent(t, fork, session.OperationMetaValue(operationID))
	assertAbsent(t, fork, session.OperationStateValue(operationID))
	assertAbsent(t, fork, session.OperationToolArgs(operationID, rootID, 0))
	assertAbsent(t, fork, session.OperationPreparation(operationID, operationID))
	stats := must(fork.GetStats(background))(t)
	equal(t, stats.MessageCount, 1)
	assertJSONEqual(t, stats.Usage, ai.Usage{})
	closeAll(t, source, fork)
}

func configuredLaneWrites(name string, tip *string) []session.Write {
	return []session.Write{
		session.SetValue(session.BranchTip(name), tip),
		session.SetValue(session.LaneConfig(name), configuration),
		session.SetValue(session.LaneStateValue(name), idleLaneState()),
	}
}

func testForkAncestry(t *testing.T, repo RepoAdapter) {
	source := must(repo.Create(background, session.SessionCreateOptions{ID: "source"}))(t)
	writes := []session.Write{
		session.InsertEntry(bareCustom(rootID, nil, "root")),
		session.InsertEntry(bareCustom(childID, new(rootID), "child")),
		session.InsertEntry(bareCustom(siblingID, new(rootID), "sibling")),
	}
	writes = append(writes, configuredLaneWrites("main", new(childID))...)
	writes = append(writes, configuredLaneWrites("empty", nil)...)
	mutateCommit(t, source, writes...)
	fork := func(id, branch string, entryID *string, position string) session.Session {
		return must(repo.Fork(background, source.Metadata(), session.ForkOptions{ID: id, Scope: session.ForkScopeBranch, Branch: branch, EntryID: entryID, Position: position}))(t)
	}
	before := fork("before", "main", new(childID), session.ForkPositionBefore)
	assertTip(t, before, "main", new(rootID))
	assertJSONEqual(t, ascEntryIDs(t, before), []string{rootID})
	mid := fork("mid", "main", new(rootID), session.ForkPositionAt)
	assertTip(t, mid, "main", new(rootID))
	assertJSONEqual(t, ascEntryIDs(t, mid), []string{rootID})
	beforeRoot := fork("before-root", "main", new(rootID), session.ForkPositionBefore)
	assertTip(t, beforeRoot, "main", nil)
	assertJSONEqual(t, ascEntryIDs(t, beforeRoot), []string{})
	empty := fork("empty", "empty", nil, "")
	assertTip(t, empty, "empty", nil)
	assertJSONEqual(t, ascEntryIDs(t, empty), []string{})
	for _, invalid := range []struct{ id, branch, entryID string }{{"off-branch", "main", siblingID}, {"unknown", "main", unknownID}, {"null-tip", "empty", rootID}} {
		_, err := repo.Fork(background, source.Metadata(), session.ForkOptions{ID: invalid.id, Scope: session.ForkScopeBranch, Branch: invalid.branch, EntryID: new(invalid.entryID)})
		mustErr(t, err, "fork "+invalid.id)
	}
	assertJSONEqual(t, listedIDs(t, repo), []string{"before", "before-root", "empty", "mid", "source"})
	closeAll(t, source, before, beforeRoot, empty, mid)
}

func testForkClosedSource(t *testing.T, repo RepoAdapter) {
	source := must(repo.Create(background, session.SessionCreateOptions{ID: "source"}))(t)
	writes := append([]session.Write{session.InsertEntry(bareCustom(rootID, nil, "root"))}, configuredLaneWrites("main", new(rootID))...)
	writes = append(writes, session.SetValue(applicationValue, session.JsonValue("excluded")), session.AppendList(applicationList, session.JsonValue("excluded")))
	mutateCommit(t, source, writes...)
	closeAll(t, source)
	fork := must(repo.Fork(background, source.Metadata(), session.ForkOptions{ID: "fork", Scope: session.ForkScopeBranch, Branch: "main"}))(t)
	assertTip(t, fork, "main", new(rootID))
	assertJSONEqual(t, getValue(t, fork, session.LaneConfig("main")).Value, configuration)
	assertJSONEqual(t, getValue(t, fork, session.LaneStateValue("main")).Value, idleLaneState())
	assertAbsent(t, fork, applicationValue)
	assertJSONEqual(t, must(session.ReadList(background, fork, applicationList, nil))(t), []any{})
	closeAll(t, fork)
}

func testForkTree(t *testing.T, repo RepoAdapter) {
	source := must(repo.Create(background, session.SessionCreateOptions{ID: "source"}))(t)
	writes := []session.Write{
		session.InsertEntry(bareCustom(rootID, nil, "root")),
		session.InsertEntry(bareCustom(childID, new(rootID), "child")),
		session.InsertEntry(bareCustom(siblingID, new(rootID), "sibling")),
	}
	writes = append(writes, configuredLaneWrites("main", new(childID))...)
	writes = append(writes, configuredLaneWrites("review", new(siblingID))...)
	writes = append(writes, session.SetValue(session.BranchTip("notes"), new(rootID)), session.SetValue(applicationValue, session.JsonValue(map[string]any{"copied": true})))
	mutateCommit(t, source, writes...)
	fork := must(repo.Fork(background, source.Metadata(), session.ForkOptions{ID: "fork", Scope: session.ForkScopeTree}))(t)
	assertJSONEqual(t, ascEntryIDs(t, fork), []string{rootID, childID, siblingID})
	assertTip(t, fork, "main", new(childID))
	assertTip(t, fork, "review", new(siblingID))
	assertTip(t, fork, "notes", new(rootID))
	assertAbsent(t, fork, session.LaneConfig("notes"))
	assertAbsent(t, fork, session.LaneStateValue("notes"))
	assertJSONEqual(t, getValue(t, fork, session.LaneConfig("review")).Value, configuration)
	assertJSONEqual(t, getValue(t, fork, session.LaneStateValue("review")).Value, idleLaneState())
	assertJSONEqual(t, getValue(t, fork, applicationValue).Value, map[string]any{"copied": true})
	closeAll(t, source, fork)
}

func testForkReservedState(t *testing.T, repo RepoAdapter) {
	source := must(repo.Create(background, session.SessionCreateOptions{ID: "source"}))(t)
	must(source.CreateBranch(background, "main", nil))(t)
	mutateCommit(t, source, session.SetValue(session.LaneConfig("main"), configuration), session.SetValue(session.LaneStateValue("main"), idleLaneState()))
	for _, namespace := range []string{"pi", "pi.unknown"} {
		address := session.MustValue[session.JsonValue](namespace, "")
		if err := source.SetValue(background, address.StoredAddressBase, true); err != nil {
			t.Fatal(err)
		}
		_, err := repo.Fork(background, source.Metadata(), session.ForkOptions{ID: "tree", Scope: session.ForkScopeTree})
		mustErr(t, err, "tree fork with "+namespace)
		_, err = repo.Fork(background, source.Metadata(), session.ForkOptions{ID: "branch", Scope: session.ForkScopeBranch, Branch: "main"})
		mustErr(t, err, "branch fork with "+namespace)
		if err := source.DeleteValue(background, address.StoredAddressBase); err != nil {
			t.Fatal(err)
		}
	}
	closeAll(t, source)
	tree := must(repo.Fork(background, source.Metadata(), session.ForkOptions{ID: "tree", Scope: session.ForkScopeTree}))(t)
	branch := must(repo.Fork(background, source.Metadata(), session.ForkOptions{ID: "branch", Scope: session.ForkScopeBranch, Branch: "main"}))(t)
	closeAll(t, tree, branch)
}

// CreateSessionRepoStreamingForkConformance creates the application-list,
// branch application-state, and lane-validation fork cases.
func CreateSessionRepoStreamingForkConformance(factory RepoFactory, onClose func() error) []sessiontesting.ConformanceCase {
	var tests []namedRepoTest
	for _, sourceState := range []string{"open", "closed"} {
		closeSource := sourceState == "closed"
		listGroup := "fork application lists (" + sourceState + " source)"
		stateGroup := "branch fork application state (" + sourceState + " source)"
		tests = append(tests,
			namedRepoTest{listGroup, "tree fork copies lists at distinct addresses", withSourceState(closeSource, testForkListAddresses)},
			namedRepoTest{listGroup, "tree fork copies only survivors after list deletion and reappend", withSourceState(closeSource, testForkListSurvivors)},
			namedRepoTest{listGroup, "tree fork preserves list element sequences including gaps", withSourceState(closeSource, testForkListSequences)},
			namedRepoTest{listGroup, "tree fork continues asc pagination using source cursors", withSourceState(closeSource, forkPagination(session.OrderAsc))},
			namedRepoTest{listGroup, "tree fork continues desc pagination using source cursors", withSourceState(closeSource, forkPagination(session.OrderDesc))},
			namedRepoTest{stateGroup, "excludes overwritten and unchanged application values", withSourceState(closeSource, testBranchForkValues)},
			namedRepoTest{stateGroup, "excludes deleted/reappended and untouched application lists", withSourceState(closeSource, testBranchForkLists)},
		)
	}
	tests = append(tests, namedRepoTest{"fork lane validation", "ignores malformed unrelated lanes", testForkIgnoresUnrelatedLanes})
	return repoCases(factory, onClose, tests)
}

type sourceStateTest func(t *testing.T, repo RepoAdapter, source session.Session, closeSource bool)

func withSourceState(closeSource bool, test sourceStateTest) repoTest {
	return func(t *testing.T, repo RepoAdapter) {
		source := must(repo.Create(background, session.SessionCreateOptions{ID: "source"}))(t)
		defer closeAll(t, source)
		test(t, repo, source, closeSource)
	}
}

func treeFork(t *testing.T, repo RepoAdapter, source session.Session, closeSource bool) session.Session {
	t.Helper()
	if closeSource {
		closeAll(t, source)
	}
	return must(repo.Fork(background, source.Metadata(), session.ForkOptions{ID: "fork", Scope: session.ForkScopeTree}))(t)
}

func appendString(t *testing.T, target session.Session, address session.ValueList[string], value string) {
	t.Helper()
	if err := target.AppendList(background, address.StoredAddressBase, value); err != nil {
		t.Fatal(err)
	}
}

func stringList(t *testing.T, target session.SessionReader, address session.ValueList[string], options *session.ListReadOptions) []session.ListElement[string] {
	t.Helper()
	return must(session.ReadList(background, target, address, options))(t)
}

func stringValues(elements []session.ListElement[string]) []string {
	out := []string{}
	for _, element := range elements {
		out = append(out, element.Value)
	}
	return out
}

var events = session.MustList[string]("test.application.events", "")

func testForkListAddresses(t *testing.T, repo RepoAdapter, source session.Session, closeSource bool) {
	sibling := session.MustList[string](events.Namespace, "other")
	otherNamespace := session.MustList[string]("pi2.events", "")
	absent := session.MustList[string](events.Namespace, "absent")
	appendString(t, source, events, "event")
	appendString(t, source, sibling, "sibling")
	appendString(t, source, otherNamespace, "other namespace")
	fork := treeFork(t, repo, source, closeSource)
	defer closeAll(t, fork)
	assertJSONEqual(t, stringValues(stringList(t, fork, events, nil)), []string{"event"})
	assertJSONEqual(t, stringValues(stringList(t, fork, sibling, nil)), []string{"sibling"})
	assertJSONEqual(t, stringValues(stringList(t, fork, otherNamespace, nil)), []string{"other namespace"})
	assertJSONEqual(t, stringList(t, fork, absent, nil), []any{})
}

func testForkListSurvivors(t *testing.T, repo RepoAdapter, source session.Session, closeSource bool) {
	deleted := session.MustList[string](events.Namespace, "deleted")
	appendString(t, source, events, "old")
	appendString(t, source, deleted, "removed")
	mutateCommit(t, source,
		session.DeleteList(events), session.AppendList(events, "temporary"), session.DeleteList(events),
		session.AppendList(events, "first survivor"), session.DeleteList(deleted),
	)
	appendString(t, source, events, "second survivor")
	fork := treeFork(t, repo, source, closeSource)
	defer closeAll(t, fork)
	assertJSONEqual(t, stringValues(stringList(t, fork, events, nil)), []string{"first survivor", "second survivor"})
	assertJSONEqual(t, stringList(t, fork, deleted, nil), []any{})
}

func testForkListSequences(t *testing.T, repo RepoAdapter, source session.Session, closeSource bool) {
	committed := mutateCommit(t, source,
		session.SetValue(session.SessionName, "before"), session.AppendList(events, "first"),
		session.SetValue(session.SessionName, "between"), session.AppendList(events, "second"),
	)
	fork := treeFork(t, repo, source, closeSource)
	defer closeAll(t, fork)
	assertJSONEqual(t, stringList(t, fork, events, nil), []session.ListElement[string]{{Seq: committed.Seqs[1], Value: "first"}, {Seq: committed.Seqs[3], Value: "second"}})
}

func forkPagination(order string) sourceStateTest {
	return func(t *testing.T, repo RepoAdapter, source session.Session, closeSource bool) {
		for _, item := range []string{"first", "second", "third"} {
			appendString(t, source, events, item)
		}
		firstPage := stringList(t, source, events, &session.ListReadOptions{Order: order, Limit: new(2)})
		equal(t, len(firstPage), 2)
		cursor := &session.ListCursor{Seq: firstPage[1].Seq}
		lastPage := stringList(t, source, events, &session.ListReadOptions{Order: order, Cursor: cursor, Limit: new(2)})
		want := "first"
		if order == session.OrderAsc {
			want = "third"
		}
		assertJSONEqual(t, stringValues(lastPage), []string{want})
		endCursor := &session.ListCursor{Seq: lastPage[0].Seq}
		fork := treeFork(t, repo, source, closeSource)
		defer closeAll(t, fork)
		assertJSONEqual(t, stringList(t, fork, events, &session.ListReadOptions{Order: order, Limit: new(2)}), firstPage)
		assertJSONEqual(t, stringList(t, fork, events, &session.ListReadOptions{Order: order, Cursor: cursor, Limit: new(2)}), lastPage)
		assertJSONEqual(t, stringList(t, fork, events, &session.ListReadOptions{Order: order, Cursor: endCursor, Limit: new(2)}), []any{})
	}
}

func reviewLaneAt(t *testing.T, source session.Session, writes ...session.Write) string {
	t.Helper()
	branch := must(source.CreateBranch(background, "review", nil))(t)
	mutateCommit(t, source, append([]session.Write{session.SetValue(session.LaneConfig("review"), configuration), session.SetValue(session.LaneStateValue("review"), idleLaneState())}, writes...)...)
	return must(branch.AppendCustomEntry(background, "fork-point", nil))(t)
}

func branchForkAt(t *testing.T, repo RepoAdapter, source session.Session, closeSource bool, entryID string) session.Session {
	t.Helper()
	if closeSource {
		closeAll(t, source)
	}
	fork := must(repo.Fork(background, source.Metadata(), session.ForkOptions{Scope: session.ForkScopeBranch, Branch: "review", EntryID: &entryID}))(t)
	assertTip(t, fork, "review", &entryID)
	return fork
}

func testBranchForkValues(t *testing.T, repo RepoAdapter, source session.Session, closeSource bool) {
	state := session.MustValue[string]("test.application.state", "")
	unchanged := session.MustValue[string]("test.application.settings", "")
	entryID := reviewLaneAt(t, source, session.SetValue(state, "v1"), session.SetValue(unchanged, "predates fork point"))
	if err := source.SetValue(background, state.StoredAddressBase, "v2"); err != nil {
		t.Fatal(err)
	}
	equal(t, getValue(t, source, state).Value, "v2")
	fork := branchForkAt(t, repo, source, closeSource, entryID)
	defer closeAll(t, fork)
	assertAbsent(t, fork, state)
	assertAbsent(t, fork, unchanged)
}

func testBranchForkLists(t *testing.T, repo RepoAdapter, source session.Session, closeSource bool) {
	untouched := session.MustList[string](events.Namespace, "untouched")
	entryID := reviewLaneAt(t, source, session.AppendList(events, "old first"), session.AppendList(events, "old second"), session.AppendList(untouched, "predates fork point"))
	if err := source.DeleteList(background, events.StoredAddressBase); err != nil {
		t.Fatal(err)
	}
	appendString(t, source, events, "new")
	assertJSONEqual(t, stringValues(stringList(t, source, events, nil)), []string{"new"})
	fork := branchForkAt(t, repo, source, closeSource, entryID)
	defer closeAll(t, fork)
	assertJSONEqual(t, stringList(t, fork, events, nil), []any{})
	assertJSONEqual(t, stringList(t, fork, untouched, nil), []any{})
}

func testForkIgnoresUnrelatedLanes(t *testing.T, repo RepoAdapter) {
	source := must(repo.Create(background, session.SessionCreateOptions{ID: "source"}))(t)
	defer closeAll(t, source)
	must(source.CreateBranch(background, "main", nil))(t)
	mutateCommit(t, source, session.SetValue(session.LaneConfig("main"), configuration), session.SetValue(session.LaneStateValue("main"), idleLaneState()), session.SetValue(session.LaneConfig("unrelated"), configuration))
	tree := must(repo.Fork(background, source.Metadata(), session.ForkOptions{ID: "tree", Scope: session.ForkScopeTree}))(t)
	branch := must(repo.Fork(background, source.Metadata(), session.ForkOptions{ID: "branch", Scope: session.ForkScopeBranch, Branch: "main"}))(t)
	assertTip(t, branch, "main", nil)
	closeAll(t, tree, branch)
}

// CreateSessionRepoForkDestinationReservationConformance creates fork cases
// that require destination reservation across create and fork. Go has no
// synchronous admission prefix for concurrent blocking calls, so each case
// races create and fork for one destination id and requires exactly one to
// publish it.
func CreateSessionRepoForkDestinationReservationConformance(factory RepoFactory, onClose func() error) []sessiontesting.ConformanceCase {
	return repoCases(factory, onClose, []namedRepoTest{
		{"fork coordination", "publishes create when it reserves a shared destination id first", reservationRace(false)},
		{"fork coordination", "publishes fork when it reserves a shared destination id first", reservationRace(true)},
	})
}

func reservationRace(forkFirst bool) repoTest {
	return func(t *testing.T, repo RepoAdapter) {
		source := must(repo.Create(background, session.SessionCreateOptions{ID: "source"}))(t)
		operations := []func() (session.Session, error){
			func() (session.Session, error) {
				return repo.Create(background, session.SessionCreateOptions{ID: "destination"})
			},
			func() (session.Session, error) {
				return repo.Fork(background, source.Metadata(), session.ForkOptions{ID: "destination", Scope: session.ForkScopeTree})
			},
		}
		if forkFirst {
			slices.Reverse(operations)
		}
		published := make([]session.Session, len(operations))
		errs := make([]error, len(operations))
		var group sync.WaitGroup
		for index, operation := range operations {
			group.Go(func() { published[index], errs[index] = operation() })
		}
		group.Wait()
		successes := 0
		for index := range operations {
			if errs[index] == nil {
				successes++
				closeAll(t, published[index])
			}
		}
		equal(t, successes, 1)
		closeAll(t, source)
	}
}

// CreateSessionRepoForkSourceSnapshotConformance creates fork cases that
// require a snapshot boundary on an active source storage queue.
func CreateSessionRepoForkSourceSnapshotConformance(factory RepoFactory, onClose func() error) []sessiontesting.ConformanceCase {
	return repoCases(factory, onClose, []namedRepoTest{
		{"fork coordination", "captures one coherent boundary between source commits", testForkSnapshotBoundary},
	})
}

// testForkSnapshotBoundary commits the first transaction under a held
// mutation, forks while the second mutation waits behind that barrier, and
// requires the fork to see exactly the first commit.
func testForkSnapshotBoundary(t *testing.T, repo RepoAdapter) {
	source := must(repo.Create(background, session.SessionCreateOptions{ID: "source"}))(t)
	first := must(source.BeginMutation(background))(t)
	writes := append([]session.Write{session.InsertEntry(bareCustom(rootID, nil, "first"))}, configuredLaneWrites("main", new(rootID))...)
	writes = append(writes, session.SetValue(session.SessionName, "first name"), session.SetValue(session.EntryLabel(rootID), "first label"))
	must(first.Commit(background, writes))(t)
	second := make(chan error, 1)
	go func() {
		_, err := source.Mutate(background, func(ctx context.Context, mutator session.SessionMutator) (any, error) {
			return mutator.Commit(ctx, []session.Write{
				session.InsertEntry(bareCustom(childID, new(rootID), "second")),
				session.SetValue(session.BranchTip("main"), new(childID)),
				session.SetValue(session.SessionName, "second name"),
				session.SetValue(session.EntryLabel(rootID), "second label"),
			})
		})
		second <- err
	}()
	forked := must(repo.Fork(background, source.Metadata(), session.ForkOptions{ID: "fork", Scope: session.ForkScopeBranch, Branch: "main"}))(t)
	if err := first.End(background); err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	assertTip(t, forked, "main", new(rootID))
	assertJSONEqual(t, ascEntryIDs(t, forked), []string{rootID})
	assertJSONEqual(t, must(forked.GetName(background))(t), "first name")
	assertJSONEqual(t, must(forked.GetLabel(background, rootID))(t), "first label")
	closeAll(t, source, forked)
}

// CreateSessionRepoForkCoordinationConformance creates every fork
// coordination case.
func CreateSessionRepoForkCoordinationConformance(factory RepoFactory, onClose func() error) []sessiontesting.ConformanceCase {
	return append(CreateSessionRepoForkDestinationReservationConformance(factory, onClose), CreateSessionRepoForkSourceSnapshotConformance(factory, onClose)...)
}

// CreateSessionRepoForkConformance creates every fork conformance case.
func CreateSessionRepoForkConformance(factory RepoFactory, onClose func() error) []sessiontesting.ConformanceCase {
	return append(CreateSessionRepoForkBehaviorConformance(factory, onClose), CreateSessionRepoForkCoordinationConformance(factory, onClose)...)
}

// CreateSessionRepoConformance creates every SessionRepo conformance case.
func CreateSessionRepoConformance(factory RepoFactory, onClose func() error) []sessiontesting.ConformanceCase {
	var cases []sessiontesting.ConformanceCase
	cases = append(cases, CreateSessionRepoLifecycleConformance(factory, onClose)...)
	cases = append(cases, CreateSessionRepoOwnershipConformance(factory, onClose)...)
	cases = append(cases, CreateSessionRepoMessageConformance(factory, onClose)...)
	return append(cases, CreateSessionRepoForkConformance(factory, onClose)...)
}
