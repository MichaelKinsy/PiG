// Ports packages/durable/test/harness-conversations.test.ts.

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

type noteState struct {
	Text string `json:"text"`
}

// uuidV7Pattern is harness-conversations.test.ts UUID_V7.
var uuidV7Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// providerSessionId reads the persisted pi.provider identity of a conversation; empty when absent.
func providerSessionId(t *testing.T, harness Harness, id durable.ConversationId) string {
	t.Helper()
	state, err := durable.Snapshot[ProviderState](testContext, harness, ProviderDoc, id)
	if err != nil {
		t.Fatal(err)
	}
	if state == nil {
		return ""
	}
	return state.SessionId
}

// expectProviderSessionId asserts a conversation's pi.provider is exactly {sessionId: <UUIDv7>} and returns the identity.
func expectProviderSessionId(t *testing.T, harness Harness, id durable.ConversationId) string {
	t.Helper()
	value := snapshotJSON(t, harness, ProviderDoc, id)
	sessionId, _ := value["sessionId"].(string)
	if len(value) != 1 || !uuidV7Pattern.MatchString(sessionId) {
		t.Fatalf("pi.provider of %d = %v, want {sessionId: UUIDv7}", id, value)
	}
	return sessionId
}

// retireProviderDoc retires a conversation's pi.provider, leaving it as a legacy conversation created before Pi 1.0.2.
func retireProviderDoc(t *testing.T, conversation Conversation) {
	t.Helper()
	if _, err := durable.Commit(testContext, conversation, func(tx durable.Tx) (struct{}, error) {
		return struct{}{}, durable.TxRetireDoc(tx, ProviderDoc, conversation.Id())
	}); err != nil {
		t.Fatal(err)
	}
}

var noteDoc = durable.DefineDoc(durable.DocDefinition[noteState]{
	CommonDocDefinition: durable.CommonDocDefinition[noteState]{Kind: "test.note", Version: 1},
	DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryRewindable, Fork: durable.ForkAsOf},
	Initial:             func() noteState { return noteState{Text: ""} },
})

var messageEntry = durable.DefineEntry[durable.Never]("message")

var ownerless = durable.ConversationOwnership{Kind: durable.ConversationOwnerless}

func appendText(t *testing.T, conversation Conversation, text string) durable.EntryRecord {
	t.Helper()
	entry, err := durable.Commit(testContext, conversation, func(tx durable.Tx) (durable.EntryRecord, error) {
		return tx.AppendEntry(conversation.Id(), durable.EntryDraft{Kind: "message", Model: []ai.Message{user(text)}})
	})
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

// allTexts pages through the history two entries at a time, newest first.
func allTexts(t *testing.T, conversation Conversation) []string {
	t.Helper()
	texts := []string{}
	var cursor durable.Cursor
	for {
		page, err := conversation.Entries(testContext, durable.EntryQuery{}, 2, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range page.Items {
			texts = append(texts, string(entry.Model[0].(ai.UserMessage).Content.(ai.UserText)))
		}
		if page.Next == nil {
			return texts
		}
		cursor = *page.Next
	}
}

func setNote(tx durable.Tx, id durable.ConversationId, text string) error {
	note, err := durable.TxDoc[noteState](tx, noteDoc, id)
	if err != nil {
		return err
	}
	return note.Set("text", text)
}

func noteText(tx durable.Tx, id durable.ConversationId) (string, error) {
	note, err := durable.TxDoc[noteState](tx, noteDoc, id)
	if err != nil {
		return "", err
	}
	text, _ := note.Get("text").(string)
	return text, nil
}

func agentDraft(tx durable.Tx, id durable.ConversationId) (*AgentState, error) {
	draft, err := durable.TxDoc[AgentState](tx, AgentDoc, id)
	if err != nil {
		return nil, err
	}
	return durable.DecodeDoc[AgentState](draft.Snapshot())
}

func snapshotJSON(t *testing.T, harness Harness, token durable.AnyDocToken, id durable.ConversationId) durable.JsonObject {
	t.Helper()
	value, err := harness.SnapshotErased(testContext, token, id)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func expectJSON(t *testing.T, got durable.JsonObject, want string) {
	t.Helper()
	if want == "undefined" {
		if got != nil {
			t.Fatalf("got %v, want undefined", got)
		}
		return
	}
	var expected durable.JsonObject
	if err := json.Unmarshal([]byte(want), &expected); err != nil {
		t.Fatal(err)
	}
	if got == nil || !reflect.DeepEqual(got, expected) {
		t.Fatalf("got %v, want %s", got, want)
	}
}

func writeTypes(writes []durable.StorageWrite) []string {
	types := []string{}
	for _, write := range writes {
		types = append(types, durable.StorageWriteType(write))
	}
	return types
}

func sqlitePath(t *testing.T) string {
	return filepath.Join(t.TempDir(), "session.sqlite")
}

func mustRoot(t *testing.T, harness Harness, options *RootOptions) Conversation {
	t.Helper()
	root, err := harness.Root(testContext, options)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func mustClose(t *testing.T, harness Harness) {
	t.Helper()
	if err := harness.Close(testContext); err != nil {
		t.Fatal(err)
	}
}

func mustAgent(t *testing.T, conversation Conversation) durable.Agent {
	t.Helper()
	agent, err := conversation.Agent(testContext)
	if err != nil {
		t.Fatal(err)
	}
	return agent
}

func mustConfigure(t *testing.T, conversation Conversation, change AgentChange) {
	t.Helper()
	if err := conversation.Configure(testContext, change); err != nil {
		t.Fatal(err)
	}
}

func mustFork(t *testing.T, conversation Conversation, at durable.EntryId, options ConversationCreateOptions) Conversation {
	t.Helper()
	fork, err := conversation.Fork(testContext, at, options)
	if err != nil {
		t.Fatal(err)
	}
	return fork
}

func expectErrorContains(t *testing.T, err error, message string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), message) {
		t.Fatalf("error %v, want %q", err, message)
	}
}

func TestHarnessRootAndConversations(t *testing.T) {
	t.Run("creates the root lazily with its agent change and init in one commit", func(t *testing.T) {
		// upstream: packages/durable/test/harness-conversations.test.ts:72
		store := newControlledStorage()
		harness, _ := openHarness(t, store, []string{"read", "bash"})
		if store.commitCount() != 0 {
			t.Fatal("open committed")
		}
		root := mustRoot(t, harness, &RootOptions{
			Agent: &AgentChange{ThinkingLevel: SetTo[ai.ModelThinkingLevel]("high")},
			Init: func(tx durable.Tx, id durable.ConversationId) error {
				// The agent change applied before init.
				agent, err := agentDraft(tx, id)
				if err != nil {
					return err
				}
				if agent.ThinkingLevel != "high" {
					return fmt.Errorf("thinking level %q before init", agent.ThinkingLevel)
				}
				return setNote(tx, id, "root note")
			},
		})
		if root.Id() != durable.ROOT_CONVERSATION_ID {
			t.Fatalf("root id %d", root.Id())
		}
		if store.commitCount() != 1 {
			t.Fatalf("commits %d", store.commitCount())
		}
		// Conversation, five built-in documents, and the init note.
		expectStrings(t, writeTypes(store.commitAt(0)), []string{"conversation", "document.create", "document.create", "document.create", "document.create", "document.create", "document.create"})
		expectJSON(t, snapshotJSON(t, harness, LiveDoc, root.Id()), `{}`)
		expectProviderSessionId(t, harness, root.Id())
		expectJSON(t, snapshotJSON(t, harness, AgentDoc, root.Id()), `{"thinkingLevel":"high"}`)
		agent := mustAgent(t, root)
		if agent.ThinkingLevel != "high" {
			t.Fatalf("thinking %q", agent.ThinkingLevel)
		}
		expectStrings(t, toolNamesOf(agent.Tools), []string{"read", "bash"})
		expectJSON(t, snapshotJSON(t, harness, noteDoc, root.Id()), `{"text":"root note"}`)

		again := mustRoot(t, harness, &RootOptions{
			Agent: &AgentChange{ThinkingLevel: SetTo[ai.ModelThinkingLevel]("low")},
			Init:  func(durable.Tx, durable.ConversationId) error { return errUnreachable },
		})
		if again.Id() != root.Id() || store.commitCount() != 1 {
			t.Fatal("root was created again")
		}
		mustClose(t, harness)
	})

	t.Run("keeps root and conversation identity and state across reopen", func(t *testing.T) {
		// upstream: packages/durable/test/harness-conversations.test.ts:112
		path := sqlitePath(t)
		openSqlite := func() durable.Storage {
			store, err := sqlitenode.OpenNodeSqliteStorage(path, sqlitenode.NodeSqliteStorageOptions{})
			if err != nil {
				t.Fatal(err)
			}
			return store
		}
		harness, _ := openHarness(t, openSqlite(), []string{"read"})
		root := mustRoot(t, harness, nil)
		mustConfigure(t, root, AgentChange{Model: SetTo(durable.ModelRef{Provider: "anthropic", ModelId: "claude"}), Cwd: SetTo("/repo")})
		entry := appendText(t, root, "hello")
		child, err := harness.CreateConversation(testContext, ConversationCreateOptions{Ownership: ownerless})
		if err != nil {
			t.Fatal(err)
		}
		fork := mustFork(t, root, entry.Id, ConversationCreateOptions{Ownership: ownerless})
		providerSessionIds := []string{}
		for _, id := range []durable.ConversationId{root.Id(), child.Id(), fork.Id()} {
			providerSessionIds = append(providerSessionIds, providerSessionId(t, harness, id))
		}
		// Regression coverage for #10424: a fork must not inherit its parent's provider identity.
		for _, sessionId := range providerSessionIds {
			if !uuidV7Pattern.MatchString(sessionId) {
				t.Fatalf("provider session ID %q is not a UUIDv7", sessionId)
			}
		}
		if distinct := map[string]bool{providerSessionIds[0]: true, providerSessionIds[1]: true, providerSessionIds[2]: true}; len(distinct) != 3 {
			t.Fatalf("provider session IDs %v are not distinct", providerSessionIds)
		}
		mustClose(t, harness)
		if _, err := root.Agent(testContext); err == nil {
			t.Fatal("agent read after close succeeded")
		}

		// The new process installs nothing: the stored choices survive, the tools do not resolve.
		harness, _ = openHarness(t, openSqlite(), nil)
		reopened := mustRoot(t, harness, &RootOptions{Init: func(durable.Tx, durable.ConversationId) error { return errUnreachable }})
		if reopened.Id() != durable.ROOT_CONVERSATION_ID {
			t.Fatalf("root id %d", reopened.Id())
		}
		agent := mustAgent(t, reopened)
		if agent.Model == nil || *agent.Model != (durable.ModelRef{Provider: "anthropic", ModelId: "claude"}) || agent.Cwd == nil || *agent.Cwd != "/repo" || len(agent.Tools) != 0 {
			t.Fatalf("agent %+v", agent)
		}
		expectStrings(t, allTexts(t, reopened), []string{"hello"})
		if found, err := harness.Conversation(testContext, child.Id()); err != nil || found == nil || found.Id() != child.Id() {
			t.Fatalf("child %v %v", found, err)
		}
		reopenedIds := []string{}
		for _, id := range []durable.ConversationId{root.Id(), child.Id(), fork.Id()} {
			reopenedIds = append(reopenedIds, providerSessionId(t, harness, id))
		}
		expectStrings(t, reopenedIds, providerSessionIds)
		reopenedFork, err := harness.Conversation(testContext, fork.Id())
		if err != nil || reopenedFork == nil {
			t.Fatalf("fork %v", err)
		}
		expectStrings(t, allTexts(t, reopenedFork), []string{"hello"})
		if model := mustAgent(t, reopenedFork).Model; model == nil || *model != (durable.ModelRef{Provider: "anthropic", ModelId: "claude"}) {
			t.Fatalf("fork model %v", model)
		}
		if missing, err := harness.Conversation(testContext, 999); err != nil || missing != nil {
			t.Fatalf("missing %v %v", missing, err)
		}
		mustClose(t, harness)
	})

	t.Run("creates independent conversations atomically with init and rolls back failures", func(t *testing.T) {
		// upstream: packages/durable/test/harness-conversations.test.ts:156
		store := newControlledStorage()
		harness, registry := openHarness(t, store, []string{"read"})
		created, err := harness.CreateConversation(testContext, ConversationCreateOptions{
			Ownership: ownerless,
			Init: func(tx durable.Tx, id durable.ConversationId) error {
				_, err := tx.AppendEntry(id, durable.EntryDraft{Kind: "message", Model: []ai.Message{user("seed")}})
				return err
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if store.commitCount() != 1 {
			t.Fatalf("commits %d", store.commitCount())
		}
		expectStrings(t, allTexts(t, created), []string{"seed"})

		before := store.commitCount()
		_, err = harness.CreateConversation(testContext, ConversationCreateOptions{
			Ownership: ownerless,
			Agent:     &AgentChange{ThinkingLevel: SetTo[ai.ModelThinkingLevel]("high")},
			Init:      func(durable.Tx, durable.ConversationId) error { return errors.New("init failed") },
		})
		expectErrorContains(t, err, "init failed")
		if store.commitCount() != before {
			t.Fatal("a failed creation committed")
		}

		// A conversation on the default selection follows installs live.
		addTool(t, registry, supportTool("bash"))
		expectStrings(t, toolNamesOf(mustAgent(t, created).Tools), []string{"read", "bash"})
		mustClose(t, harness)
	})

	t.Run("forks at a concrete entry with the as-of agent and applies agent and init overrides", func(t *testing.T) {
		// upstream: packages/durable/test/harness-conversations.test.ts:194
		harness, registry := openHarness(t, storage.NewMemoryStorage(), []string{"read"})
		legacy := supportTool("legacy")
		addTool(t, registry, legacy)
		root := mustRoot(t, harness, nil)
		mustConfigure(t, root, AgentChange{ThinkingLevel: SetTo[ai.ModelThinkingLevel]("low")})
		at := appendText(t, root, "one")
		mustConfigure(t, root, AgentChange{ThinkingLevel: SetTo[ai.ModelThinkingLevel]("high"), Tools: SetTo(ToolChange{Exact: true, List: []*durable.ToolRegistration{supportTool("read")}})})
		appendText(t, root, "two")

		child := mustFork(t, root, at.Id, ConversationCreateOptions{Ownership: ownerless})
		expectJSON(t, snapshotJSON(t, harness, AgentDoc, child.Id()), `{"thinkingLevel":"low"}`)
		expectStrings(t, allTexts(t, child), []string{"one"})

		overridden := mustFork(t, root, at.Id, ConversationCreateOptions{
			Ownership: ownerless,
			Agent:     &AgentChange{ThinkingLevel: SetTo[ai.ModelThinkingLevel]("minimal"), Tools: SetTo(ToolChange{Exact: true, List: []*durable.ToolRegistration{legacy}})},
			Init: func(tx durable.Tx, id durable.ConversationId) error {
				agent, err := agentDraft(tx, id)
				if err != nil {
					return err
				}
				if agent.ThinkingLevel != "minimal" {
					return fmt.Errorf("thinking level %q in init", agent.ThinkingLevel)
				}
				return nil
			},
		})
		expectJSON(t, snapshotJSON(t, harness, AgentDoc, overridden.Id()), `{"thinkingLevel":"minimal","tools":["legacy"]}`)
		expectJSON(t, snapshotJSON(t, harness, AgentDoc, root.Id()), `{"thinkingLevel":"high","tools":["read"]}`)

		unrelated, err := harness.CreateConversation(testContext, ConversationCreateOptions{Ownership: ownerless})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := root.Fork(testContext, at.Id, ConversationCreateOptions{Ownership: ownerless}); err != nil {
			t.Fatal(err)
		}
		_, err = unrelated.Fork(testContext, at.Id, ConversationCreateOptions{Ownership: ownerless})
		expectErrorContains(t, err, "is not visible")
		mustClose(t, harness)
	})

	t.Run("paginates fork-aware history through deep ancestor caps and same-commit prefixes", func(t *testing.T) {
		// upstream: packages/durable/test/harness-conversations.test.ts:233
		harness, _ := openHarness(t, storage.NewMemoryStorage(), nil)
		root := mustRoot(t, harness, nil)
		appendText(t, root, "r1")
		r2, err := durable.Commit(testContext, root, func(tx durable.Tx) (durable.EntryRecord, error) {
			r2, err := tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "message", Model: []ai.Message{user("r2")}})
			if err != nil {
				return r2, err
			}
			_, err = tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "message", Model: []ai.Message{user("r3")}})
			return r2, err
		})
		if err != nil {
			t.Fatal(err)
		}
		child := mustFork(t, root, r2.Id, ConversationCreateOptions{Ownership: ownerless})
		c1 := appendText(t, child, "c1")
		appendText(t, child, "c2")
		grandchild := mustFork(t, child, c1.Id, ConversationCreateOptions{Ownership: ownerless})
		appendText(t, grandchild, "g1")

		expectStrings(t, allTexts(t, root), []string{"r3", "r2", "r1"})
		expectStrings(t, allTexts(t, child), []string{"c2", "c1", "r2", "r1"})
		expectStrings(t, allTexts(t, grandchild), []string{"g1", "c1", "r2", "r1"})
		// The query's conversation is ignored: the handle's conversation bounds the scan.
		bounded, err := grandchild.Entries(testContext, durable.EntryQuery{MinEntryId: new(r2.Id), MaxEntryId: new(c1.Id), ConversationId: root.Id()}, 10, nil)
		if err != nil {
			t.Fatal(err)
		}
		expectIds(t, entryIds(bounded.Items), []durable.EntryId{c1.Id, r2.Id})
		if !messageEntry.Is(&bounded.Items[0]) || messageEntry.Is(nil) {
			t.Fatal("entry kind guard")
		}
		mustClose(t, harness)
	})

	t.Run("binds commits and task creation to the conversation", func(t *testing.T) {
		// upstream: packages/durable/test/harness-conversations.test.ts:265
		harness, _ := openHarness(t, storage.NewMemoryStorage(), nil)
		conversation, err := harness.CreateConversation(testContext, ConversationCreateOptions{Ownership: ownerless})
		if err != nil {
			t.Fatal(err)
		}
		type workInput struct {
			N int `json:"n"`
		}
		work := durable.DefineTask(durable.TaskDefinition[workInput, stepState, durable.JsonValue, any]{
			Name:    "test.work",
			Version: 1,
			Initial: func(workInput) stepState { return stepState{Phase: "run"} },
			Phases: map[string]durable.PhaseHandler[workInput, stepState, durable.JsonValue, any]{
				"run": func(context.Context, durable.RunningTask[workInput, stepState, durable.JsonValue], durable.TaskRuntime[workInput, stepState, durable.JsonValue, any]) error {
					return nil
				},
			},
			Abort: func(context.Context, durable.RunningTask[workInput, stepState, durable.JsonValue], durable.TaskRuntime[workInput, stepState, durable.JsonValue, any]) error {
				return nil
			},
		})
		conversationOwned := durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}}
		taskId, err := durable.Commit(testContext, conversation, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, work, workInput{N: 1}, conversationOwned)
		})
		if err != nil {
			t.Fatal(err)
		}
		record, err := durable.Commit(testContext, harness, func(tx durable.Tx) (*durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], error) {
			return tx.Task(taskId)
		})
		if err != nil || record == nil || record.ConversationId != conversation.Id() {
			t.Fatalf("record %+v %v", record, err)
		}
		_, err = durable.Commit(testContext, harness, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, work, workInput{N: 2}, conversationOwned)
		})
		expectErrorContains(t, err, "requires options.conversationId")
		mustClose(t, harness)
	})

	t.Run("runs conversationCreated in every creating commit, after the built-ins and before agent and init", func(t *testing.T) {
		// upstream: packages/durable/test/harness-conversations.test.ts:287
		var seen []string
		harness, err := OpenHarness(testContext, storage.NewMemoryStorage(), HarnessOptions{
			Models:   ai.CreateModels(),
			Registry: CreateRegistry(),
			ConversationCreated: func(tx durable.Tx, conversation durable.ConversationRecord) error {
				agent, err := agentDraft(tx, conversation.Id)
				if err != nil {
					return err
				}
				kind := "fork"
				if conversation.Parent == nil {
					kind = "new"
				}
				cwd := "undefined"
				if agent.Cwd != nil {
					cwd = *agent.Cwd
				}
				seen = append(seen, fmt.Sprintf("%d:%s:%s", conversation.Id, kind, cwd))
				text, err := noteText(tx, conversation.Id)
				if err != nil {
					return err
				}
				if text == "" {
					if err := setNote(tx, conversation.Id, "created"); err != nil {
						return err
					}
				}
				if cwd == "/fail" {
					return errors.New("no")
				}
				return nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		root := mustRoot(t, harness, &RootOptions{
			Agent: &AgentChange{Cwd: SetTo("/root")},
			Init: func(tx durable.Tx, id durable.ConversationId) error {
				text, err := noteText(tx, id)
				seen = append(seen, "init:"+text)
				return err
			},
		})
		// A raw creation in a commit, as in a tool, and a fork, which already has the asOf copies.
		raw, err := durable.Commit(testContext, harness, func(tx durable.Tx) (durable.ConversationId, error) {
			record, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: ownerless})
			return record.Id, err
		})
		if err != nil {
			t.Fatal(err)
		}
		entry := appendText(t, root, "hello")
		fork := mustFork(t, root, entry.Id, ConversationCreateOptions{Ownership: ownerless})
		expectStrings(t, seen, []string{
			fmt.Sprintf("%d:new:undefined", root.Id()),
			"init:created",
			fmt.Sprintf("%d:new:undefined", raw),
			fmt.Sprintf("%d:fork:/root", fork.Id()),
		})
		expectJSON(t, snapshotJSON(t, harness, noteDoc, raw), `{"text":"created"}`)
		// A failure fails the creating commit.
		mustConfigure(t, root, AgentChange{Cwd: SetTo("/fail")})
		if _, err := root.Fork(testContext, entry.Id, ConversationCreateOptions{Ownership: ownerless}); err != nil {
			t.Fatal(err)
		}
		failing := appendText(t, root, "after")
		_, err = root.Fork(testContext, failing.Id, ConversationCreateOptions{Ownership: ownerless})
		expectErrorContains(t, err, "no")
		mustClose(t, harness)
	})
}

func TestHarnessAgent(t *testing.T) {
	t.Run("replaces whole fields, clears them with null, and leaves undefined fields alone", func(t *testing.T) {
		// upstream: packages/durable/test/harness-conversations.test.ts:331
		harness, registry := openHarness(t, storage.NewMemoryStorage(), nil)
		read, bash, edit := supportTool("read"), supportTool("bash"), supportTool("edit")
		mustInstall(t, registry, new(durable.Extension{Name: "coding", Tools: []*durable.ToolRegistration{read, bash, edit}}))
		root := mustRoot(t, harness, nil)
		stored := func() durable.JsonObject { return snapshotJSON(t, harness, AgentDoc, root.Id()) }
		offered := func() []string { return toolNamesOf(mustAgent(t, root).Tools) }
		agent := mustAgent(t, root)
		if agent.ThinkingLevel != "off" || len(agent.Tools) != 3 || agent.Tools[0] != read || agent.Tools[1] != bash || agent.Tools[2] != edit || agent.Model != nil {
			t.Fatalf("agent %+v", agent)
		}

		mustConfigure(t, root, AgentChange{Model: SetTo(durable.ModelRef{Provider: "openai", ModelId: "gpt"}), ThinkingLevel: SetTo[ai.ModelThinkingLevel]("medium")})
		mustConfigure(t, root, AgentChange{Instructions: SetTo("Be terse.")})
		expectJSON(t, stored(), `{"model":{"provider":"openai","modelId":"gpt"},"thinkingLevel":"medium","instructions":"Be terse."}`)
		mustConfigure(t, root, AgentChange{Model: Cleared[durable.ModelRef](), Instructions: Cleared[string]()})
		expectJSON(t, stored(), `{"thinkingLevel":"medium"}`)

		mustConfigure(t, root, AgentChange{Tools: SetTo(ToolChange{Remove: []*durable.ToolRegistration{edit}})})
		expectStrings(t, offered(), []string{"read", "bash"})
		// A new filter replaces the old one: edit is offered again.
		mustConfigure(t, root, AgentChange{Tools: SetTo(ToolChange{Remove: []*durable.ToolRegistration{bash}})})
		expectStrings(t, offered(), []string{"read", "edit"})
		mustConfigure(t, root, AgentChange{Tools: SetTo(ToolChange{Exact: true, List: []*durable.ToolRegistration{edit, read}})})
		expectStrings(t, offered(), []string{"edit", "read"})
		mustConfigure(t, root, AgentChange{Tools: Cleared[ToolChange]()})
		expectStrings(t, offered(), []string{"read", "bash", "edit"})
		// Names are stored without checking the registry.
		mustConfigure(t, root, AgentChange{Tools: SetTo(ToolChange{Exact: true, List: []*durable.ToolRegistration{supportTool("missing"), read}})})
		if tools := stored()["tools"]; !reflect.DeepEqual(tools, []any{"missing", "read"}) {
			t.Fatalf("stored tools %v", tools)
		}
		expectStrings(t, offered(), []string{"read"})
		mustClose(t, harness)
	})

	t.Run("gives conversations created through Tx their documents: empty, an owner copy, or the fork's as-of copy", func(t *testing.T) {
		// upstream: packages/durable/test/harness-conversations.test.ts:369
		harness, _ := openHarness(t, storage.NewMemoryStorage(), []string{"read"})
		root := mustRoot(t, harness, &RootOptions{Agent: &AgentChange{Model: SetTo(durable.ModelRef{Provider: "faux", ModelId: "m"}), Instructions: SetTo("Main role."), Cwd: SetTo("/repo")}})
		owner := durable.DefineTask(durable.TaskDefinition[durable.JsonObject, stepState, durable.JsonValue, any]{
			Name:    "test.owner",
			Version: 1,
			Initial: func(durable.JsonObject) stepState { return stepState{Phase: "never"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonObject, stepState, durable.JsonValue, any]{
				"never": func(context.Context, durable.RunningTask[durable.JsonObject, stepState, durable.JsonValue], durable.TaskRuntime[durable.JsonObject, stepState, durable.JsonValue, any]) error {
					return nil
				},
			},
			Abort: func(context.Context, durable.RunningTask[durable.JsonObject, stepState, durable.JsonValue], durable.TaskRuntime[durable.JsonObject, stepState, durable.JsonValue, any]) error {
				return nil
			},
		})
		type created struct {
			taskId durable.TaskId
			plain  durable.ConversationId
			owned  durable.ConversationId
		}
		ids, err := durable.Commit(testContext, root, func(tx durable.Tx) (created, error) {
			taskId, err := durable.CreateTask(tx, owner, durable.JsonObject{}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
			if err != nil {
				return created{}, err
			}
			plain, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: ownerless})
			if err != nil {
				return created{}, err
			}
			owned, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: taskId}})
			if err != nil {
				return created{}, err
			}
			// The copy exists when CreateConversation returns, so a Configure in the same callback overrides it.
			agent, err := agentDraft(tx, owned.Id)
			if err != nil {
				return created{}, err
			}
			if agent.Cwd == nil || *agent.Cwd != "/repo" {
				return created{}, fmt.Errorf("owned cwd %v", agent.Cwd)
			}
			if err := Configure(tx, owned.Id, AgentChange{Cwd: SetTo("/worktree")}); err != nil {
				return created{}, err
			}
			return created{taskId: taskId, plain: plain.Id, owned: owned.Id}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		expectJSON(t, snapshotJSON(t, harness, AgentDoc, ids.plain), `{}`)
		expectJSON(t, snapshotJSON(t, harness, LiveDoc, ids.plain), `{}`)
		expectProviderSessionId(t, harness, ids.plain)
		if providerSessionId(t, harness, ids.owned) == providerSessionId(t, harness, root.Id()) {
			t.Fatal("a task-owned conversation copied its owner's provider session ID")
		}
		expectJSON(t, snapshotJSON(t, harness, AgentDoc, ids.owned), `{"model":{"provider":"faux","modelId":"m"},"instructions":"Main role.","cwd":"/worktree"}`)

		// A later owner change does not reach the child.
		mustConfigure(t, root, AgentChange{ThinkingLevel: SetTo[ai.ModelThinkingLevel]("high")})
		if _, set := snapshotJSON(t, harness, AgentDoc, ids.owned)["thinkingLevel"]; set {
			t.Fatal("owner change reached the child")
		}

		// A task-owned fork keeps its as-of copy of its fork parent, not its owner's agent.
		at, err := durable.Commit(testContext, root, func(tx durable.Tx) (durable.EntryId, error) {
			entry, err := tx.AppendEntry(ids.plain, durable.EntryDraft{Kind: "note"})
			return entry.Id, err
		})
		if err != nil {
			t.Fatal(err)
		}
		fork, err := durable.Commit(testContext, root, func(tx durable.Tx) (durable.ConversationId, error) {
			record, err := tx.ForkConversation(ids.plain, at, durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: ids.taskId}})
			return record.Id, err
		})
		if err != nil {
			t.Fatal(err)
		}
		expectJSON(t, snapshotJSON(t, harness, AgentDoc, fork), `{}`)
		expectJSON(t, snapshotJSON(t, harness, LiveDoc, fork), `{}`)
		if providerSessionId(t, harness, fork) == providerSessionId(t, harness, ids.plain) {
			t.Fatal("a fork copied its parent's provider session ID")
		}
		mustClose(t, harness)
	})

	t.Run("reads an absent agent for conversations a plain Session created without writing", func(t *testing.T) {
		// upstream: packages/durable/test/harness-conversations.test.ts:423
		store := newControlledStorage()
		id, err := durable.Commit(testContext, session.CreateSession(store), func(tx durable.Tx) (durable.ConversationId, error) {
			record, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: ownerless})
			return record.Id, err
		})
		if err != nil {
			t.Fatal(err)
		}
		harness, _ := openHarness(t, store, []string{"read"})
		raw, err := harness.Conversation(testContext, id)
		if err != nil || raw == nil {
			t.Fatalf("raw %v", err)
		}
		expectJSON(t, snapshotJSON(t, harness, LiveDoc, id), "undefined")
		commits := store.commitCount()
		agent := mustAgent(t, raw)
		if agent.ThinkingLevel != "off" || len(agent.Tools) != 1 || agent.Tools[0].Name != "read" {
			t.Fatalf("agent %+v", agent)
		}
		if store.commitCount() != commits {
			t.Fatal("reading the agent committed")
		}
		mustConfigure(t, raw, AgentChange{ThinkingLevel: SetTo[ai.ModelThinkingLevel]("low")})
		expectJSON(t, snapshotJSON(t, harness, AgentDoc, id), `{"thinkingLevel":"low"}`)
		mustClose(t, harness)
	})
}

func TestHarnessLifecycleHandles(t *testing.T) {
	t.Run("returns stateless handles and rejects operations after close", func(t *testing.T) {
		// upstream: packages/durable/test/harness-conversations.test.ts:442
		harness, _ := openHarness(t, storage.NewMemoryStorage(), nil)
		root := mustRoot(t, harness, nil)
		again := mustRoot(t, harness, nil)
		if again == root || again.Id() != root.Id() {
			t.Fatal("handles are not stateless")
		}
		mustClose(t, harness)
		_, err := harness.Root(testContext, nil)
		expectErrorContains(t, err, "Harness is closed")
		_, err = harness.CreateConversation(testContext, ConversationCreateOptions{Ownership: ownerless})
		expectErrorContains(t, err, "Harness is closed")
		_, err = harness.Conversation(testContext, root.Id())
		expectErrorContains(t, err, "Harness is closed")
	})

	t.Run("forwards generic Session document APIs", func(t *testing.T) {
		// upstream: packages/durable/test/harness-conversations.test.ts:456
		harness, _ := openHarness(t, storage.NewMemoryStorage(), nil)
		root := mustRoot(t, harness, nil)
		entry, err := durable.Commit(testContext, root, func(tx durable.Tx) (durable.EntryRecord, error) {
			if err := setNote(tx, root.Id(), "first"); err != nil {
				return durable.EntryRecord{}, err
			}
			return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "message", Model: []ai.Message{user("m")}})
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := harness.Commit(testContext, func(tx durable.Tx) (any, error) { return nil, setNote(tx, root.Id(), "second") }); err != nil {
			t.Fatal(err)
		}
		expectJSON(t, snapshotJSON(t, harness, noteDoc, root.Id()), `{"text":"second"}`)
		asOf, err := harness.SnapshotAsOfErased(testContext, noteDoc, entry.Id, root.Id())
		if err != nil {
			t.Fatal(err)
		}
		expectJSON(t, asOf, `{"text":"first"}`)
		state, err := harness.DocumentStateErased(testContext, noteDoc, root.Id())
		if err != nil || state == nil {
			t.Fatalf("state %v", err)
		}
		if !reflect.DeepEqual(state.Value(), durable.JsonObject{"text": "second"}) {
			t.Fatalf("state value %v", state.Value())
		}
		state.Dispose()
		mustClose(t, harness)
	})
}
