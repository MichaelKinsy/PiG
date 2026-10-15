// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

//go:build pigfault

package driver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

func conversationTool(name string) *durable.ToolRegistration {
	return &durable.ToolRegistration{
		ToolSchema: ai.ToolSchema{Name: name, Description: name + " tool", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
		Execute: func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}}, nil
		},
	}
}

// openConversations is harness-support.ts openHarness: a Harness over the real core with the named tools installed.
func openConversations(t *testing.T, path string, store *controlledStore, toolNames ...string) (harness.Harness, harness.Registry) {
	t.Helper()
	registry := harness.CreateRegistry()
	if len(toolNames) > 0 {
		tools := make([]*durable.ToolRegistration, 0, len(toolNames))
		for _, name := range toolNames {
			tools = append(tools, conversationTool(name))
		}
		if err := registry.Install(&durable.Extension{Name: "tools", Tools: tools}); err != nil {
			t.Fatal(err)
		}
	}
	return openConversationsWith(t, path, store, harness.HarnessOptions{Models: ai.CreateModels(), Registry: registry}), registry
}

func openConversationsWith(t *testing.T, path string, store *controlledStore, options harness.HarnessOptions) harness.Harness {
	t.Helper()
	h, err := driver.OpenHarnessFault(t.Context(), path, options, store.onCommit)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	return h
}

var conversationNote = durable.DefineDoc(durable.DocDefinition[obj]{
	CommonDocDefinition: durable.CommonDocDefinition[obj]{Kind: "test.note", Version: 1, Initial: func() obj { return delta.JsonObjectOf("text", "") }},
	DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryRewindable, Fork: durable.ForkAsOf},
})

var uuidV7 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func conversationAgentDraft(tx durable.Tx, id durable.ConversationId) (*harness.AgentState, error) {
	draft, err := durable.TxDoc[harness.AgentState](tx, harness.AgentDoc, id)
	if err != nil {
		return nil, err
	}
	return durable.DecodeDoc[harness.AgentState](draft.Snapshot())
}

// sameJson is Vitest's toEqual for JSON values: members compare by key, not by insertion order.
func sameJson(got, want any) bool {
	plain := func(value any) any {
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		var out any
		if err := json.Unmarshal(encoded, &out); err != nil {
			return err
		}
		return out
	}
	return reflect.DeepEqual(plain(got), plain(want))
}

func conversationSnapshot(t *testing.T, h harness.Harness, token durable.AnyDocToken, id durable.ConversationId) durable.JsonObject {
	t.Helper()
	value, err := h.SnapshotErased(t.Context(), token, id)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// providerIdentity asserts a conversation's pi.provider is exactly {sessionId: <UUIDv7>} and returns the identity.
func providerIdentity(t *testing.T, h harness.Harness, id durable.ConversationId) string {
	t.Helper()
	value := conversationSnapshot(t, h, harness.ProviderDoc, id)
	sessionId, _ := value.Value("sessionId").(string)
	if value.Len() != 1 || !uuidV7.MatchString(sessionId) {
		t.Fatalf("pi.provider of %d = %v, want {sessionId: UUIDv7}", id, value)
	}
	return sessionId
}

func toolNames(tools []*durable.ToolRegistration) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

// harness-conversations.test.ts:73 "creates the root lazily with its agent change and init in one commit".
func TestCoreConversationsCreateTheRootLazilyWithItsAgentChangeAndInitInOneCommit(t *testing.T) {
	store := &controlledStore{}
	h, _ := openConversations(t, t.TempDir()+"/s.db", store, "read", "bash")
	// The native store creates its schema in the first commit; Pi's adapter does that outside Storage.commit, so the baseline is one.
	base := store.count()
	root, err := h.Root(t.Context(), &harness.RootOptions{
		Agent: &harness.AgentChange{ThinkingLevel: harness.SetTo[ai.ModelThinkingLevel]("high")},
		Init: func(tx durable.Tx, id durable.ConversationId) error {
			// The agent change applied before init.
			agent, err := conversationAgentDraft(tx, id)
			if err != nil {
				return err
			}
			if agent.ThinkingLevel != "high" {
				return fmt.Errorf("thinking level %q before init", agent.ThinkingLevel)
			}
			note, err := tx.Doc(conversationNote, id)
			if err != nil {
				return err
			}
			return note.Set("text", "root note")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if root.Id() != durable.ROOT_CONVERSATION_ID {
		t.Fatalf("root id %d", root.Id())
	}
	if store.count() != base+1 {
		t.Fatalf("commits %d, want %d", store.count(), base+1)
	}
	// Conversation, five built-in documents, and the init note: Pi's seven writes are one conversation insert and six documents.
	var conversations, documents int
	for _, stmt := range store.statementsOf(base) {
		switch {
		case strings.HasPrefix(stmt, "INSERT INTO conversations"):
			conversations++
		case strings.HasPrefix(stmt, "INSERT INTO documents"):
			documents++
		}
	}
	if conversations != 1 || documents != 6 {
		t.Fatalf("the creating commit wrote %d conversations and %d documents, want 1 and 6", conversations, documents)
	}
	if got := conversationSnapshot(t, h, harness.LiveDoc, root.Id()); !sameJson(got, delta.JsonObjectOf()) {
		t.Fatalf("live doc = %v", got)
	}
	providerIdentity(t, h, root.Id())
	if got := conversationSnapshot(t, h, harness.AgentDoc, root.Id()); !sameJson(got, delta.JsonObjectOf("thinkingLevel", "high")) {
		t.Fatalf("agent doc = %v", got)
	}
	agent, err := root.Agent(t.Context())
	if err != nil || agent.ThinkingLevel != "high" || !reflect.DeepEqual(toolNames(agent.Tools), []string{"read", "bash"}) {
		t.Fatalf("agent = %+v, %v", agent, err)
	}
	if got := conversationSnapshot(t, h, conversationNote, root.Id()); !sameJson(got, delta.JsonObjectOf("text", "root note")) {
		t.Fatalf("note = %v", got)
	}
	again, err := h.Root(t.Context(), &harness.RootOptions{
		Agent: &harness.AgentChange{ThinkingLevel: harness.SetTo[ai.ModelThinkingLevel]("low")},
		Init:  func(durable.Tx, durable.ConversationId) error { t.Error("init ran for an existing root"); return nil },
	})
	if err != nil || again.Id() != root.Id() || store.count() != base+1 {
		t.Fatalf("second root %v %v, commits %d", again, err, store.count())
	}
}
