// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

//go:build pigfault

package driver_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// harness-conversations.test.ts:426 "reads an absent agent for conversations a plain Session created without writing" has no native
// equivalent: it creates a conversation through a plain Session over the same Storage, bypassing the Harness creation hook. The native
// Harness owns its database file, and every conversation it can create runs the built-in hook (the in-process port in
// durable/harness/harness_conversations_test.go covers it).
var conversationOwnerless = durable.ConversationOwnership{Kind: durable.ConversationOwnerless}

func conversationUser(text string) ai.Message {
	return ai.UserMessage{Content: ai.UserText(text), Timestamp: 1}
}

// conversationAppend is harness-conversations.test.ts append.
func conversationAppend(t *testing.T, conversation harness.Conversation, text string) durable.EntryRecord {
	t.Helper()
	entry, err := durable.Commit(t.Context(), conversation, func(tx durable.Tx) (durable.EntryRecord, error) {
		return tx.AppendEntry(conversation.Id(), durable.EntryDraft{Kind: "message", Model: []ai.Message{conversationUser(text)}})
	})
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

// conversationTexts is harness-conversations.test.ts allEntries: pages of two, newest first unless ordered.
func conversationTexts(t *testing.T, conversation harness.Conversation, order ...durable.ScanOrder) []string {
	t.Helper()
	texts := []string{}
	query := durable.EntryQuery{}
	if len(order) > 0 {
		query.Order = &order[0]
	}
	var cursor durable.Cursor
	for {
		page, err := conversation.Entries(t.Context(), query, 2, cursor)
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

func conversationFork(t *testing.T, conversation harness.Conversation, at durable.EntryId, options harness.ConversationCreateOptions) harness.Conversation {
	t.Helper()
	fork, err := conversation.Fork(t.Context(), at, options)
	if err != nil {
		t.Fatal(err)
	}
	return fork
}

func conversationConfigure(t *testing.T, conversation harness.Conversation, change harness.AgentChange) {
	t.Helper()
	if err := conversation.Configure(t.Context(), change); err != nil {
		t.Fatal(err)
	}
}

func conversationAgentOf(t *testing.T, conversation harness.Conversation) durable.Agent {
	t.Helper()
	agent, err := conversation.Agent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return agent
}

func expectConversationSnapshot(t *testing.T, h harness.Harness, id durable.ConversationId, want durable.JsonObject) {
	t.Helper()
	if got := conversationSnapshot(t, h, harness.AgentDoc, id); !sameJson(got, want) {
		t.Fatalf("pi.agent of %d = %v, want %v", id, got, want)
	}
}

func expectTexts(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("entries %v, want %v", got, want)
	}
}

var claude = durable.ModelRef{Provider: "anthropic", ModelId: "claude"}

// harness-conversations.test.ts:113 "keeps root and conversation identity and state across reopen": the first Harness closes,
// a second one opens the same database file with no installs.
func TestCoreConversationsKeepRootAndConversationIdentityAndStateAcrossReopen(t *testing.T) {
	path := t.TempDir() + "/s.db"
	first, _ := openConversations(t, path, &controlledStore{}, "read")
	root, err := first.Root(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	conversationConfigure(t, root, harness.AgentChange{Model: harness.SetTo(claude), Cwd: harness.SetTo("/repo")})
	entry := conversationAppend(t, root, "hello")
	child, err := first.CreateConversation(t.Context(), harness.ConversationCreateOptions{Ownership: conversationOwnerless})
	if err != nil {
		t.Fatal(err)
	}
	fork := conversationFork(t, root, entry.Id, harness.ConversationCreateOptions{Ownership: conversationOwnerless})
	ids := []durable.ConversationId{root.Id(), child.Id(), fork.Id()}
	var sessionIds []string
	for _, id := range ids {
		sessionIds = append(sessionIds, providerIdentity(t, first, id))
	}
	// Regression coverage for #10424: a fork must not inherit its parent's provider identity.
	if distinct := map[string]bool{sessionIds[0]: true, sessionIds[1]: true, sessionIds[2]: true}; len(distinct) != 3 {
		t.Fatalf("provider session ids %v are not distinct", sessionIds)
	}
	if err := first.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Agent(t.Context()); err == nil {
		t.Fatal("a closed Harness answered agent()")
	}

	// The new process installs nothing: the stored choices survive, the tools do not resolve.
	second, _ := openConversations(t, path, &controlledStore{})
	reopened, err := second.Root(t.Context(), &harness.RootOptions{Init: func(durable.Tx, durable.ConversationId) error {
		return errors.New("init ran for an existing root")
	}})
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Id() != durable.ROOT_CONVERSATION_ID {
		t.Fatalf("root id %d", reopened.Id())
	}
	agent := conversationAgentOf(t, reopened)
	if agent.Model == nil || *agent.Model != claude || agent.Cwd == nil || *agent.Cwd != "/repo" || len(agent.Tools) != 0 {
		t.Fatalf("agent %+v", agent)
	}
	expectTexts(t, conversationTexts(t, reopened), []string{"hello"})
	if found, err := second.Conversation(t.Context(), child.Id()); err != nil || found == nil || found.Id() != child.Id() {
		t.Fatalf("child %v %v", found, err)
	}
	var reopenedIds []string
	for _, id := range ids {
		reopenedIds = append(reopenedIds, providerIdentity(t, second, id))
	}
	expectTexts(t, reopenedIds, sessionIds)
	reopenedFork, err := second.Conversation(t.Context(), fork.Id())
	if err != nil || reopenedFork == nil {
		t.Fatalf("fork %v %v", reopenedFork, err)
	}
	expectTexts(t, conversationTexts(t, reopenedFork), []string{"hello"})
	if model := conversationAgentOf(t, reopenedFork).Model; model == nil || *model != claude {
		t.Fatalf("fork model %v", model)
	}
	if missing, err := second.Conversation(t.Context(), 999); err != nil || missing != nil {
		t.Fatalf("conversation 999 = %v, %v", missing, err)
	}
}

// harness-conversations.test.ts:157 "creates independent conversations atomically with init and rolls back failures".
func TestCoreConversationsCreateAtomicallyWithInitAndRollBackFailures(t *testing.T) {
	store := &controlledStore{}
	h, registry := openConversations(t, t.TempDir()+"/s.db", store, "read")
	base := store.count()
	created, err := h.CreateConversation(t.Context(), harness.ConversationCreateOptions{
		Ownership: conversationOwnerless,
		Init: func(tx durable.Tx, id durable.ConversationId) error {
			_, err := tx.AppendEntry(id, durable.EntryDraft{Kind: "message", Model: []ai.Message{conversationUser("seed")}})
			return err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.count() != base+1 {
		t.Fatalf("commits %d, want %d", store.count(), base+1)
	}
	expectTexts(t, conversationTexts(t, created), []string{"seed"})

	before := store.count()
	_, err = h.CreateConversation(t.Context(), harness.ConversationCreateOptions{
		Ownership: conversationOwnerless,
		Agent:     &harness.AgentChange{ThinkingLevel: harness.SetTo[ai.ModelThinkingLevel]("high")},
		Init:      func(durable.Tx, durable.ConversationId) error { return errors.New("init failed") },
	})
	if err == nil || !strings.Contains(err.Error(), "init failed") {
		t.Fatalf("creation error %v, want init failed", err)
	}
	if store.count() != before {
		t.Fatalf("a failed creation committed: %d commits, want %d", store.count(), before)
	}

	// A conversation on the default selection follows installs live.
	if err := registry.Install(&durable.Extension{Name: "tool:bash", Tools: []*durable.ToolRegistration{conversationTool("bash")}}); err != nil {
		t.Fatal(err)
	}
	if got := toolNames(conversationAgentOf(t, created).Tools); !reflect.DeepEqual(got, []string{"read", "bash"}) {
		t.Fatalf("tools %v, want [read bash]", got)
	}
}

// harness-conversations.test.ts:195 "forks at a concrete entry with the as-of agent and applies agent and init overrides".
func TestCoreConversationsForkAtAConcreteEntryWithTheAsOfAgentAndOverrides(t *testing.T) {
	h, registry := openConversations(t, t.TempDir()+"/s.db", &controlledStore{}, "read")
	legacy := conversationTool("legacy")
	if err := registry.Install(&durable.Extension{Name: "tool:legacy", Tools: []*durable.ToolRegistration{legacy}}); err != nil {
		t.Fatal(err)
	}
	root, err := h.Root(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	conversationConfigure(t, root, harness.AgentChange{ThinkingLevel: harness.SetTo[ai.ModelThinkingLevel]("low")})
	at := conversationAppend(t, root, "one")
	conversationConfigure(t, root, harness.AgentChange{
		ThinkingLevel: harness.SetTo[ai.ModelThinkingLevel]("high"),
		Tools:         harness.SetTo(harness.ToolChange{List: []*durable.ToolRegistration{conversationTool("read")}}),
	})
	conversationAppend(t, root, "two")

	child := conversationFork(t, root, at.Id, harness.ConversationCreateOptions{Ownership: conversationOwnerless})
	expectConversationSnapshot(t, h, child.Id(), delta.JsonObjectOf("thinkingLevel", "low"))
	expectTexts(t, conversationTexts(t, child), []string{"one"})

	overridden := conversationFork(t, root, at.Id, harness.ConversationCreateOptions{
		Ownership: conversationOwnerless,
		Agent: &harness.AgentChange{
			ThinkingLevel: harness.SetTo[ai.ModelThinkingLevel]("minimal"),
			Tools:         harness.SetTo(harness.ToolChange{List: []*durable.ToolRegistration{legacy}}),
		},
		Init: func(tx durable.Tx, id durable.ConversationId) error {
			agent, err := conversationAgentDraft(tx, id)
			if err != nil {
				return err
			}
			if agent.ThinkingLevel != "minimal" {
				return errors.New("thinking level " + string(agent.ThinkingLevel) + " in init")
			}
			return nil
		},
	})
	expectConversationSnapshot(t, h, overridden.Id(), delta.JsonObjectOf("thinkingLevel", "minimal", "tools", []any{"legacy"}))
	expectConversationSnapshot(t, h, root.Id(), delta.JsonObjectOf("thinkingLevel", "high", "tools", []any{"read"}))

	unrelated, err := h.CreateConversation(t.Context(), harness.ConversationCreateOptions{Ownership: conversationOwnerless})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := root.Fork(t.Context(), at.Id, harness.ConversationCreateOptions{Ownership: conversationOwnerless}); err != nil {
		t.Fatal(err)
	}
	if _, err := unrelated.Fork(t.Context(), at.Id, harness.ConversationCreateOptions{Ownership: conversationOwnerless}); err == nil || !strings.Contains(err.Error(), "is not visible") {
		t.Fatalf("fork of an entry outside the conversation: %v, want is not visible", err)
	}
}

// harness-conversations.test.ts:234 "paginates fork-aware history through deep ancestor caps and same-commit prefixes".
func TestCoreConversationsPaginateForkAwareHistoryThroughAncestorCapsAndSameCommitPrefixes(t *testing.T) {
	h, _ := openConversations(t, t.TempDir()+"/s.db", &controlledStore{})
	root, err := h.Root(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	conversationAppend(t, root, "r1")
	r2, err := durable.Commit(t.Context(), root, func(tx durable.Tx) (durable.EntryRecord, error) {
		r2, err := tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "message", Model: []ai.Message{conversationUser("r2")}})
		if err != nil {
			return r2, err
		}
		_, err = tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "message", Model: []ai.Message{conversationUser("r3")}})
		return r2, err
	})
	if err != nil {
		t.Fatal(err)
	}
	child := conversationFork(t, root, r2.Id, harness.ConversationCreateOptions{Ownership: conversationOwnerless})
	c1 := conversationAppend(t, child, "c1")
	conversationAppend(t, child, "c2")
	grandchild := conversationFork(t, child, c1.Id, harness.ConversationCreateOptions{Ownership: conversationOwnerless})
	conversationAppend(t, grandchild, "g1")

	expectTexts(t, conversationTexts(t, root), []string{"r3", "r2", "r1"})
	expectTexts(t, conversationTexts(t, child), []string{"c2", "c1", "r2", "r1"})
	expectTexts(t, conversationTexts(t, grandchild), []string{"g1", "c1", "r2", "r1"})
	// #10546: oldest first, root segment forward, through pages that carry the order in their cursor.
	expectTexts(t, conversationTexts(t, grandchild, durable.ScanAscending), []string{"r1", "r2", "c1", "g1"})
	// The query's conversation is ignored: the handle's conversation bounds the scan.
	bounded, err := grandchild.Entries(t.Context(), durable.EntryQuery{MinEntryId: &r2.Id, MaxEntryId: &c1.Id, ConversationId: root.Id()}, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	var ids []durable.EntryId
	for _, entry := range bounded.Items {
		ids = append(ids, entry.Id)
	}
	if !reflect.DeepEqual(ids, []durable.EntryId{c1.Id, r2.Id}) {
		t.Fatalf("bounded entries %v, want [%d %d]", ids, c1.Id, r2.Id)
	}
}

// harness-conversations.test.ts:268 "binds commits and task creation to the conversation".
func TestCoreConversationsBindCommitsAndTaskCreationToTheConversation(t *testing.T) {
	h, _ := openConversations(t, t.TempDir()+"/s.db", &controlledStore{})
	conversation, err := h.CreateConversation(t.Context(), harness.ConversationCreateOptions{Ownership: conversationOwnerless})
	if err != nil {
		t.Fatal(err)
	}
	work := oneStepTask("test.work", func(context.Context, stepRuntime) error { return nil })
	owned := durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}}
	taskId, err := durable.Commit(t.Context(), conversation, func(tx durable.Tx) (durable.TaskId, error) {
		return durable.CreateTask(tx, work, durable.JsonValue(delta.JsonObjectOf("n", float64(1))), owned)
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := durable.Commit(t.Context(), h, func(tx durable.Tx) (*durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], error) {
		return tx.Task(taskId)
	})
	if err != nil || record == nil || record.ConversationId != conversation.Id() {
		t.Fatalf("task record %+v, %v: want conversation %d", record, err, conversation.Id())
	}
	_, err = durable.Commit(t.Context(), h, func(tx durable.Tx) (durable.TaskId, error) {
		return durable.CreateTask(tx, work, durable.JsonValue(delta.JsonObjectOf("n", float64(2))), owned)
	})
	if err == nil || !strings.Contains(err.Error(), "requires options.conversationId") {
		t.Fatalf("a Harness-level conversation-owned task: %v, want requires options.conversationId", err)
	}
}

// harness-conversations.test.ts:326 "runs conversationCreated in every creating commit, after the built-ins and before agent and init".
func TestCoreConversationsRunConversationCreatedInEveryCreatingCommit(t *testing.T) {
	var seen []string
	h := openConversationsWith(t, t.TempDir()+"/s.db", &controlledStore{}, harness.HarnessOptions{
		Models:   ai.CreateModels(),
		Registry: harness.CreateRegistry(),
		ConversationCreated: func(tx durable.Tx, conversation durable.ConversationRecord) error {
			agent, err := conversationAgentDraft(tx, conversation.Id)
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
			note, err := tx.Doc(conversationNote, conversation.Id)
			if err != nil {
				return err
			}
			if text, _ := note.Get("text").(string); text == "" {
				if err := note.Set("text", "created"); err != nil {
					return err
				}
			}
			if cwd == "/fail" {
				return errors.New("no")
			}
			return nil
		},
	})
	root, err := h.Root(t.Context(), &harness.RootOptions{
		Agent: &harness.AgentChange{Cwd: harness.SetTo("/root")},
		Init: func(tx durable.Tx, id durable.ConversationId) error {
			note, err := tx.Doc(conversationNote, id)
			if err != nil {
				return err
			}
			text, _ := note.Get("text").(string)
			seen = append(seen, "init:"+text)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// A raw creation in a commit, as in a tool, and a fork, which already has the asOf copies.
	raw, err := durable.Commit(t.Context(), h, func(tx durable.Tx) (durable.ConversationId, error) {
		record, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: conversationOwnerless})
		return record.Id, err
	})
	if err != nil {
		t.Fatal(err)
	}
	entry := conversationAppend(t, root, "hello")
	fork := conversationFork(t, root, entry.Id, harness.ConversationCreateOptions{Ownership: conversationOwnerless})
	expectTexts(t, seen, []string{
		fmt.Sprintf("%d:new:undefined", root.Id()),
		"init:created",
		fmt.Sprintf("%d:new:undefined", raw),
		fmt.Sprintf("%d:fork:/root", fork.Id()),
	})
	if got := conversationSnapshot(t, h, conversationNote, raw); !sameJson(got, delta.JsonObjectOf("text", "created")) {
		t.Fatalf("note of the raw conversation = %v", got)
	}
	// A throw fails the creating commit.
	conversationConfigure(t, root, harness.AgentChange{Cwd: harness.SetTo("/fail")})
	if _, err := root.Fork(t.Context(), entry.Id, harness.ConversationCreateOptions{Ownership: conversationOwnerless}); err != nil {
		t.Fatal(err)
	}
	failing := conversationAppend(t, root, "after")
	if _, err := root.Fork(t.Context(), failing.Id, harness.ConversationCreateOptions{Ownership: conversationOwnerless}); err == nil || !strings.Contains(err.Error(), "no") {
		t.Fatalf("fork whose hook throws: %v, want no", err)
	}
}

// harness-conversations.test.ts:334 "replaces whole fields, clears them with null, and leaves undefined fields alone".
func TestCoreAgentReplacesWholeFieldsClearsThemAndLeavesUnsetFieldsAlone(t *testing.T) {
	h, registry := openConversations(t, t.TempDir()+"/s.db", &controlledStore{})
	read, bash, edit := conversationTool("read"), conversationTool("bash"), conversationTool("edit")
	if err := registry.Install(&durable.Extension{Name: "coding", Tools: []*durable.ToolRegistration{read, bash, edit}}); err != nil {
		t.Fatal(err)
	}
	root, err := h.Root(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	stored := func() durable.JsonObject { return conversationSnapshot(t, h, harness.AgentDoc, root.Id()) }
	offered := func() []string { return toolNames(conversationAgentOf(t, root).Tools) }
	agent := conversationAgentOf(t, root)
	if agent.ThinkingLevel != "off" || !reflect.DeepEqual(toolNames(agent.Tools), []string{"read", "bash", "edit"}) || agent.Model != nil {
		t.Fatalf("agent %+v", agent)
	}

	conversationConfigure(t, root, harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "openai", ModelId: "gpt"}), ThinkingLevel: harness.SetTo[ai.ModelThinkingLevel]("medium")})
	conversationConfigure(t, root, harness.AgentChange{Instructions: harness.SetTo("Be terse.")})
	want := delta.JsonObjectOf("model", delta.JsonObjectOf("provider", "openai", "modelId", "gpt"), "thinkingLevel", "medium", "instructions", "Be terse.")
	if got := stored(); !sameJson(got, want) {
		t.Fatalf("stored %v, want %v", got, want)
	}
	conversationConfigure(t, root, harness.AgentChange{Model: harness.Cleared[durable.ModelRef](), Instructions: harness.Cleared[string]()})
	if got := stored(); !sameJson(got, delta.JsonObjectOf("thinkingLevel", "medium")) {
		t.Fatalf("stored after clearing %v", got)
	}

	for _, step := range []struct {
		change harness.ToolChange
		want   []string
	}{
		{harness.ToolChange{Edit: &harness.ToolEdit{Remove: []*durable.ToolRegistration{edit}}}, []string{"read", "bash"}},
		// A new filter replaces the old one: edit is offered again.
		{harness.ToolChange{Edit: &harness.ToolEdit{Remove: []*durable.ToolRegistration{bash}}}, []string{"read", "edit"}},
		{harness.ToolChange{List: []*durable.ToolRegistration{edit, read}}, []string{"edit", "read"}},
	} {
		conversationConfigure(t, root, harness.AgentChange{Tools: harness.SetTo(step.change)})
		expectTexts(t, offered(), step.want)
	}
	conversationConfigure(t, root, harness.AgentChange{Tools: harness.Cleared[harness.ToolChange]()})
	expectTexts(t, offered(), []string{"read", "bash", "edit"})
	// Names are stored without checking the registry.
	conversationConfigure(t, root, harness.AgentChange{Tools: harness.SetTo(harness.ToolChange{List: []*durable.ToolRegistration{conversationTool("missing"), read}})})
	if tools := stored().Value("tools"); !reflect.DeepEqual(tools, []any{"missing", "read"}) {
		t.Fatalf("stored tools %v", tools)
	}
	expectTexts(t, offered(), []string{"read"})
}

// harness-conversations.test.ts:372 "gives conversations created through Tx their documents: empty, an owner copy, or the fork's as-of copy".
func TestCoreConversationsCreatedThroughTxGetTheirDocuments(t *testing.T) {
	h, _ := openConversations(t, t.TempDir()+"/s.db", &controlledStore{}, "read")
	root, err := h.Root(t.Context(), &harness.RootOptions{Agent: &harness.AgentChange{
		Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "m"}), Instructions: harness.SetTo("Main role."), Cwd: harness.SetTo("/repo"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	owner := oneStepTask("test.owner", func(context.Context, stepRuntime) error { return nil })
	type created struct {
		taskId       durable.TaskId
		plain, owned durable.ConversationId
	}
	ids, err := durable.Commit(t.Context(), root, func(tx durable.Tx) (created, error) {
		taskId, err := durable.CreateTask(tx, owner, durable.JsonValue(delta.JsonObjectOf()), durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
		if err != nil {
			return created{}, err
		}
		plain, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: conversationOwnerless})
		if err != nil {
			return created{}, err
		}
		owned, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: taskId}})
		if err != nil {
			return created{}, err
		}
		// The copy exists when CreateConversation returns, so a Configure in the same callback overrides it.
		agent, err := conversationAgentDraft(tx, owned.Id)
		if err != nil {
			return created{}, err
		}
		if agent.Cwd == nil || *agent.Cwd != "/repo" {
			return created{}, fmt.Errorf("owned cwd %v", agent.Cwd)
		}
		if err := harness.Configure(tx, owned.Id, harness.AgentChange{Cwd: harness.SetTo("/worktree")}); err != nil {
			return created{}, err
		}
		return created{taskId: taskId, plain: plain.Id, owned: owned.Id}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := conversationSnapshot(t, h, harness.AgentDoc, ids.plain); !sameJson(got, delta.JsonObjectOf()) {
		t.Fatalf("plain agent %v", got)
	}
	if got := conversationSnapshot(t, h, harness.LiveDoc, ids.plain); !sameJson(got, delta.JsonObjectOf()) {
		t.Fatalf("plain live %v", got)
	}
	providerIdentity(t, h, ids.plain)
	if providerIdentity(t, h, ids.owned) == providerIdentity(t, h, root.Id()) {
		t.Fatal("a task-owned conversation copied its owner's provider session id")
	}
	want := delta.JsonObjectOf("model", delta.JsonObjectOf("provider", "faux", "modelId", "m"), "instructions", "Main role.", "cwd", "/worktree")
	if got := conversationSnapshot(t, h, harness.AgentDoc, ids.owned); !sameJson(got, want) {
		t.Fatalf("owned agent %v, want %v", got, want)
	}

	// A later owner change does not reach the child.
	conversationConfigure(t, root, harness.AgentChange{ThinkingLevel: harness.SetTo[ai.ModelThinkingLevel]("high")})
	if set := conversationSnapshot(t, h, harness.AgentDoc, ids.owned).Has("thinkingLevel"); set {
		t.Fatal("an owner change reached the child")
	}

	// A task-owned fork keeps its as-of copy of its fork parent, not its owner's agent.
	at, err := durable.Commit(t.Context(), root, func(tx durable.Tx) (durable.EntryId, error) {
		entry, err := tx.AppendEntry(ids.plain, durable.EntryDraft{Kind: "note"})
		return entry.Id, err
	})
	if err != nil {
		t.Fatal(err)
	}
	fork, err := durable.Commit(t.Context(), root, func(tx durable.Tx) (durable.ConversationId, error) {
		record, err := tx.ForkConversation(ids.plain, at, durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: ids.taskId}})
		return record.Id, err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []durable.AnyDocToken{harness.AgentDoc, harness.LiveDoc} {
		if got := conversationSnapshot(t, h, token, fork); !sameJson(got, delta.JsonObjectOf()) {
			t.Fatalf("fork document %v", got)
		}
	}
	if providerIdentity(t, h, fork) == providerIdentity(t, h, ids.plain) {
		t.Fatal("a fork copied its parent's provider session id")
	}
}

// harness-conversations.test.ts:445 "returns stateless handles and rejects operations after close".
func TestCoreConversationsReturnStatelessHandlesAndRejectOperationsAfterClose(t *testing.T) {
	h, _ := openConversations(t, t.TempDir()+"/s.db", &controlledStore{})
	root, err := h.Root(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	again, err := h.Root(t.Context(), nil)
	if err != nil || again == root || again.Id() != root.Id() {
		t.Fatalf("second root %v %v: handles are not stateless", again, err)
	}
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, err = h.Root(t.Context(), nil)
	expectClosed(t, "root", err)
	_, err = h.CreateConversation(t.Context(), harness.ConversationCreateOptions{Ownership: conversationOwnerless})
	expectClosed(t, "createConversation", err)
	_, err = h.Conversation(t.Context(), root.Id())
	expectClosed(t, "conversation", err)
}

func expectClosed(t *testing.T, call string, err error) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "Harness is closed") {
		t.Fatalf("%s after close: %v, want Harness is closed", call, err)
	}
}

// harness-conversations.test.ts:459 "forwards generic Session document APIs".
func TestCoreConversationsForwardGenericSessionDocumentAPIs(t *testing.T) {
	h, _ := openConversations(t, t.TempDir()+"/s.db", &controlledStore{})
	root, err := h.Root(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	setNote := func(tx durable.Tx, text string) error {
		note, err := tx.Doc(conversationNote, root.Id())
		if err != nil {
			return err
		}
		return note.Set("text", text)
	}
	entry, err := durable.Commit(t.Context(), root, func(tx durable.Tx) (durable.EntryRecord, error) {
		if err := setNote(tx, "first"); err != nil {
			return durable.EntryRecord{}, err
		}
		return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "message", Model: []ai.Message{conversationUser("m")}})
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Commit(t.Context(), func(tx durable.Tx) (any, error) { return nil, setNote(tx, "second") }); err != nil {
		t.Fatal(err)
	}
	if got := conversationSnapshot(t, h, conversationNote, root.Id()); !sameJson(got, delta.JsonObjectOf("text", "second")) {
		t.Fatalf("snapshot %v", got)
	}
	asOf, err := h.SnapshotAsOfErased(t.Context(), conversationNote, entry.Id, root.Id())
	if err != nil || !sameJson(asOf, delta.JsonObjectOf("text", "first")) {
		t.Fatalf("snapshotAsOf %v, %v", asOf, err)
	}
	watch, err := h.WatchDocErased(t.Context(), conversationNote, root.Id())
	if err != nil || watch == nil {
		t.Fatalf("watchDoc %v, %v", watch, err)
	}
	if !sameJson(watch.Value(), delta.JsonObjectOf("text", "second")) {
		t.Fatalf("watchDoc value %v", watch.Value())
	}
	_, _ = watch.Stop()
}
