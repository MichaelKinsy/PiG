// Pi Pocket's Harness usage, composed the way src/server does it (app.ts, commands.ts, extensions/codemode.ts,
// extensions/plan.ts, schedules.ts). The Pi tests prove each call alone; these cases prove the compositions Pocket
// relies on and no Pi test has: session-scoped documents next to conversation documents in one SQLite store, a commit
// subscriber that tracks busy state and spend from document changes, a fork that copies and trims documents in the
// creating commit, a ToolTask hook read from Extension.Hooks and called by a host tool with a HookApi that overrides
// memo, and storage reads on the Storage object the host kept.

package examples_test

import (
	"context"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

type pocketSessions struct {
	Items map[string]map[string]any `json:"items"`
}

type pocketPlan struct {
	On bool `json:"on"`
}

type pocketChat struct {
	Messages []string `json:"messages"`
}

type pocketArtifact struct {
	Content string `json:"content"`
}

var (
	// SessionsDoc: scope "session", read as harness.snapshot(SessionsDoc, context).
	pocketSessionsDoc = durable.DefineDoc(durable.DocDefinition[pocketSessions]{
		CommonDocDefinition: durable.CommonDocDefinition[pocketSessions]{Kind: "pocket.sessions", Version: 1, Initial: func() pocketSessions { return pocketSessions{Items: map[string]map[string]any{}} }},
		DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeSession},
	})
	// PlanDoc and ChatDoc: conversation scope, latest history, a fork keeps the parent's current value.
	pocketPlanDoc = durable.DefineDoc(durable.DocDefinition[pocketPlan]{
		CommonDocDefinition: durable.CommonDocDefinition[pocketPlan]{Kind: "pocket.plan", Version: 1, Initial: func() pocketPlan { return pocketPlan{} }},
		DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryLatest, Fork: durable.ForkCurrent},
	})
	pocketChatDoc = durable.DefineDoc(durable.DocDefinition[pocketChat]{
		CommonDocDefinition: durable.CommonDocDefinition[pocketChat]{Kind: "pocket.chat", Version: 1, Initial: func() pocketChat { return pocketChat{Messages: []string{}} }},
		DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryLatest, Fork: durable.ForkCurrent},
	})
	// ArtifactBodyDoc: a family keyed by artifact id with a seed.
	pocketBodyDoc = durable.DefineDocFamily(durable.DocFamilyDefinition[pocketArtifact, pocketArtifact]{
		Family: true,
		Kind:   "pocket.artifact-body", Version: 1,
		DocumentSemantics: durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryLatest, Fork: durable.ForkCurrent},
		Initial:           func(seed pocketArtifact) pocketArtifact { return seed },
	})
)

// 1. open over SQLite with conversationCreated, a session document, a family document, reopen and scan.
func TestPocketOpenCreatesDocumentsAndSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pocket.sqlite")
	var kept durable.Storage
	var reports []error
	open := func() harness.Harness {
		store, err := sqlitenode.OpenNodeSqliteStorage(path, sqlitenode.NodeSqliteStorageOptions{})
		if err != nil {
			t.Fatal(err)
		}
		kept = store
		opened, err := harness.OpenHarness(background, store, harness.HarnessOptions{
			Models:   fauxModels(fauxAnswer("Hi.")),
			Registry: harness.CreateRegistry(),
			Now:      func() float64 { return 1_700_000_000_000 },
			// Every conversation gets the app's documents up front.
			ConversationCreated: func(tx durable.Tx, conversation durable.ConversationRecord) error {
				_, err := durable.TxDoc[pocketChat](tx, pocketChatDoc, conversation.Id)
				return err
			},
			OnReport: func(err error) { reports = append(reports, err) },
		})
		if err != nil {
			t.Fatal(err)
		}
		return opened
	}
	opened := open()
	root := must(opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(fauxModel)}}))
	if chat := must(durable.Snapshot[pocketChat](background, opened, pocketChatDoc, root.Id())); chat == nil {
		t.Fatal("conversationCreated did not create the chat document in the creating commit")
	}
	commit(t, opened, func(tx durable.Tx) (bool, error) {
		sessions, err := durable.TxDoc[pocketSessions](tx, pocketSessionsDoc)
		if err != nil {
			return false, err
		}
		if err := sessions.Object("items").Set("1", map[string]any{"cwd": "/work", "title": "first"}); err != nil {
			return false, err
		}
		body, err := durable.TxDoc[pocketArtifact](tx, pocketBodyDoc, root.Id(), "a1", pocketArtifact{Content: "v1"})
		if err != nil {
			return false, err
		}
		return false, body.Set("content", "v2")
	})
	say(t, root, "hello")
	closeSession(t, opened)

	opened = open()
	sessions := must(durable.Snapshot[pocketSessions](background, opened, pocketSessionsDoc))
	if sessions == nil || sessions.Items["1"]["title"] != "first" {
		t.Fatalf("sessions after reopen: %+v", sessions)
	}
	body := must(durable.Snapshot[pocketArtifact](background, opened, pocketBodyDoc, root.Id(), "a1"))
	if body == nil || body.Content != "v2" {
		t.Fatalf("artifact body after reopen: %+v", body)
	}
	// Startup recovery: page conversations in a commit, then read submissions on the Storage the host kept.
	var seen []durable.ConversationId
	var cursor durable.Cursor
	for {
		page := commit(t, opened, func(tx durable.Tx) (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
			return tx.ScanConversations(durable.ConversationQuery{}, 1, cursor)
		})
		for _, record := range page.Items {
			seen = append(seen, record.Id)
		}
		if page.Next == nil {
			break
		}
		cursor = *page.Next
	}
	expectEqual(t, "conversations", seen, []durable.ConversationId{root.Id()})
	submissions, err := kept.ScanSubmissions(background, durable.SubmissionQuery{ConversationId: new(root.Id())}, 256, nil)
	if err != nil || len(submissions.Items) != 1 || submissions.Items[0].Status != durable.SubmissionDone {
		t.Fatalf("submissions on the kept Storage: %+v, %v", submissions, err)
	}
	if len(reports) != 0 {
		t.Fatalf("reports: %v", reports)
	}
	closeSession(t, opened)
}

// 2. subscribeCommits: busy state from pi.live document changes, spend from pi.usage, submissions from table changes.
func TestPocketCommitSubscriberTracksBusySpendAndSubmissions(t *testing.T) {
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{Models: fauxModels(fauxAnswer("Done.")), Registry: harness.CreateRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	busyChanges := []bool{}
	var usageSeen, submissionsSeen int
	var busy bool
	unsubscribe := opened.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
		mu.Lock()
		defer mu.Unlock()
		for _, change := range publication.Changes {
			switch change := change.(type) {
			case durable.DocumentChange:
				if change.ConversationId == nil {
					continue
				}
				switch change.Record.Kind {
				case harness.UsageDoc.AnyDefinition().Kind:
					usageSeen++
				case "pi.live":
					live, err := durable.DecodeDoc[harness.LiveState](change.Value)
					if err != nil {
						t.Error(err)
						continue
					}
					nowBusy := live != nil && live.Run != nil
					if nowBusy != busy {
						busy = nowBusy
						busyChanges = append(busyChanges, busy)
					}
				}
			case durable.SubmissionWrite:
				submissionsSeen++
				if change.Value.ConversationId == 0 {
					t.Error("submission change without conversation")
				}
			}
		}
	})
	defer unsubscribe()
	root := must(opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(fauxModel)}}))
	opened.Resume()
	say(t, root, "go")
	// Pi resumes code awaiting submission.wait after the synchronous commit listeners ran, so the subscriber has seen
	// the run end by the time say returns.
	mu.Lock()
	defer mu.Unlock()
	expectEqual(t, "busy transitions", busyChanges, []bool{true, false})
	if usageSeen == 0 || submissionsSeen == 0 {
		t.Fatalf("usage changes %d, submission changes %d", usageSeen, submissionsSeen)
	}
	closeSession(t, opened)
}

// 3. A fork copies documents in the creating commit (init) and a fork at an entry trims what the parent wrote later.
func TestPocketForkInitCopiesAndTrimsDocuments(t *testing.T) {
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{Models: fauxModels(fauxAnswer("A."), fauxAnswer("B.")), Registry: harness.CreateRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	root := must(opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(fauxModel)}}))
	say(t, root, "one")
	commit(t, root, func(tx durable.Tx) (bool, error) {
		plan, err := durable.TxDoc[pocketPlan](tx, pocketPlanDoc, root.Id())
		if err != nil {
			return false, err
		}
		chat, err := durable.TxDoc[pocketChat](tx, pocketChatDoc, root.Id())
		if err != nil {
			return false, err
		}
		if err := plan.Set("on", true); err != nil {
			return false, err
		}
		_, err = chat.Array("messages").Push("parent says hi")
		return false, err
	})
	page := must(root.Entries(background, durable.EntryQuery{}, 20, nil))
	at := page.Items[0].Id
	forked := must(root.Fork(background, at, harness.ConversationCreateOptions{
		Ownership: ownerless.Ownership,
		Agent:     &harness.AgentChange{Model: harness.SetTo(fauxModel)},
		Init: func(tx durable.Tx, id durable.ConversationId) error {
			// The chat is the parent's conversation, not this one's: cleared in the forking commit.
			chat, err := durable.TxDoc[pocketChat](tx, pocketChatDoc, id)
			if err != nil {
				return err
			}
			for chat.Array("messages").Len() > 0 {
				chat.Array("messages").Pop()
			}
			sessions, err := durable.TxDoc[pocketSessions](tx, pocketSessionsDoc)
			if err != nil {
				return err
			}
			return sessions.Object("items").Set(strconv.FormatInt(int64(id), 10), map[string]any{"forkedFrom": float64(root.Id())})
		},
	}))
	if plan := must(durable.Snapshot[pocketPlan](background, opened, pocketPlanDoc, forked.Id())); plan == nil || !plan.On {
		t.Fatalf("a fork keeps the parent's current plan: %+v", plan)
	}
	if chat := must(durable.Snapshot[pocketChat](background, opened, pocketChatDoc, forked.Id())); chat == nil || len(chat.Messages) != 0 {
		t.Fatalf("the fork's chat was not cleared: %+v", chat)
	}
	sessions := must(durable.Snapshot[pocketSessions](background, opened, pocketSessionsDoc))
	if sessions == nil || sessions.Items[strconv.FormatInt(int64(forked.Id()), 10)]["forkedFrom"] != float64(root.Id()) {
		t.Fatalf("session document written by the fork's init: %+v", sessions)
	}
	if chat := must(durable.Snapshot[pocketChat](background, opened, pocketChatDoc, root.Id())); len(chat.Messages) != 1 {
		t.Fatalf("the parent's chat changed: %+v", chat)
	}
	closeSession(t, opened)
}

// 4. A ToolTask hook is read from Extension.Hooks and called by a host tool with a HookApi that overrides memo
// (codemode.ts toolHooks and its `{...api, memo}`), and the plan-mode hook blocks a call through the Harness.
// Pi source: packages/durable/src/harness/types.ts
// mutation-checked: dropping the reads and writes of ToolHooks.BeforeTool fails it
// upstream: packages/durable/src/harness/types.ts:608-609 memo on the hook API and types.ts:192-193 on the tool execution API.
func TestPocketToolHooksAreIntrospectableAndCallableFromATool(t *testing.T) {
	var planOn bool
	guard := new(durable.Extension{
		Name: "plan-guard",
		Hooks: []durable.HookRegistration{harness.Hook(harness.ToolTask, &harness.ToolHooks{
			BeforeTool: func(ctx context.Context, call ai.ToolCall, api harness.HookApi) (*harness.BeforeToolResult, error) {
				if !planOn || call.Name != "write_file" {
					return nil, nil
				}
				return &harness.BeforeToolResult{Block: new("Plan mode: " + call.Name + " is not allowed.")}, nil
			},
		})},
		Tools: []*durable.ToolRegistration{
			{ToolSchema: ai.ToolSchema{Name: "write_file", Description: "writes", Parameters: emptyParameters}, Execute: func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				return textResult("written"), nil
			}},
		},
	})
	var memoNames []string
	var nestedDecision *harness.BeforeToolResult
	runner := new(durable.Extension{Name: "runner", Tools: []*durable.ToolRegistration{{
		ToolSchema: ai.ToolSchema{Name: "run", Description: "runs a nested call through the hooks", Parameters: emptyParameters},
		Replay:     durable.ReplayUnsafe,
		Execute: func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			agent, err := api.Agent(ctx)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			for _, extension := range agent.Extensions {
				for _, registration := range extension.Hooks {
					if registration.Task != harness.ToolTask.AnyDefinition().Name {
						continue
					}
					handlers := registration.Handlers.(*harness.ToolHooks)
					// `{...api, memo}`: the call gets its own memo names.
					hookApi := memoPrefixed{ToolExecutionApi: api, prefix: "codemode/1/", names: &memoNames}
					decision, err := handlers.BeforeTool(ctx, ai.ToolCall{ID: api.CallId() + "/1", Name: "write_file", Arguments: map[string]any{}}, hookApi)
					if err != nil {
						return durable.ToolExecutionResult{}, err
					}
					nestedDecision = decision
					if _, _, err := hookApi.Memo(ctx, "probe"); err != nil {
						return durable.ToolExecutionResult{}, err
					}
				}
			}
			return textResult("ran"), nil
		},
	}}})
	registry := harness.CreateRegistry()
	installed(t, registry, guard)
	installed(t, registry, runner)
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{Registry: registry, Models: fauxModels(
		fauxToolTurn("write_file", map[string]any{}, "w1"), fauxAnswer("blocked."),
		fauxToolTurn("run", map[string]any{}, "r1"), fauxAnswer("ran."),
	)})
	if err != nil {
		t.Fatal(err)
	}
	root := must(opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(fauxModel)}}))
	planOn = true
	say(t, root, "write")
	if got := lastToolResultText(t, root); !strings.Contains(got, "Plan mode: write_file is not allowed.") {
		t.Fatalf("blocked tool result: %q", got)
	}
	say(t, root, "run")
	if nestedDecision == nil || nestedDecision.Block == nil || !strings.Contains(*nestedDecision.Block, "Plan mode") {
		t.Fatalf("the hook read from Extension.Hooks decided %+v", nestedDecision)
	}
	if !slices.Equal(memoNames, []string{"codemode/1/probe"}) {
		t.Fatalf("memo names through the overriding HookApi: %v", memoNames)
	}
	closeSession(t, opened)
}

// memoPrefixed is `{ ...api, memo }`: every method of the ToolExecutionApi except the memo names, which it prefixes.
type memoPrefixed struct {
	durable.ToolExecutionApi
	prefix string
	names  *[]string
}

func (api memoPrefixed) Memo(ctx context.Context, name string) (durable.JsonValue, bool, error) {
	*api.names = append(*api.names, api.prefix+name)
	return api.ToolExecutionApi.Memo(ctx, api.prefix+name)
}

func (api memoPrefixed) MemoCandidate(ctx context.Context, name string, candidate durable.JsonValue) (durable.JsonValue, error) {
	return api.ToolExecutionApi.MemoCandidate(ctx, api.prefix+name, candidate)
}
