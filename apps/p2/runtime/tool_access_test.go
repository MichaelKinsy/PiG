package runtime

import (
	"context"
	"regexp"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

// Ports the guardTool cases of apps/p2/test/p2/tool-access.test.ts (tool requirements). Its four resolveTools cases belong
// to the subagents plugin (`contract.worker.ts`), not yet ported: rows/p2-A.md (c).

func accessTool(name string, access ToolAccess) P2Tool {
	return P2Tool{
		ToolRegistration: &durable.ToolRegistration{
			ToolSchema: ai.ToolSchema{Name: name, Description: name},
			Execute: func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: name + " ran"}}}, nil
			},
		},
		ToolAccess: access,
	}
}

// accessAPI is a call's API in conversation conversation, whose run placed one input with data; lock: the session's
// lock. Only what the guard reads is there.
type accessAPI struct {
	durable.ToolExecutionApi
	conversation durable.ConversationId
	data         any
	lock         *SessionLock
}

func (api accessAPI) ConversationId() durable.ConversationId { return api.conversation }

func (api accessAPI) SnapshotErased(_ context.Context, token durable.AnyDocToken, _ ...any) (durable.JsonObject, error) {
	switch token.AnyDefinition().Kind {
	case "pi.live":
		return delta.JsonObjectOf("run", delta.JsonObjectOf("inputs", []any{float64(1)})), nil
	case "p2.session-lock":
		if api.lock != nil {
			capabilities := make([]any, len(api.lock.Capabilities))
			for i, each := range api.lock.Capabilities {
				capabilities[i] = string(each)
			}
			return delta.JsonObjectOf("lock", delta.JsonObjectOf("mcp", []any{}, "projectMcp", false, "instructions", false, "capabilities", capabilities)), nil
		}
	}
	return nil, nil
}

func (api accessAPI) Commit(_ context.Context, change func(tx durable.Tx) (any, error), _ ...durable.CommitOptions) (any, error) {
	return change(accessTx{data: api.data})
}

type accessTx struct {
	durable.Tx
	data any
}

func (accessTx) Submission(durable.SubmissionId) (*durable.SubmissionRecord, error) {
	entry := durable.EntryId(1)
	return &durable.SubmissionRecord{Type: durable.SubmissionTypeInput, Entry: &entry}, nil
}

func (tx accessTx) Entry(durable.EntryId) (*durable.EntryRecord, error) {
	return &durable.EntryRecord{Id: 1, Kind: "p2.input", Data: tx.data}, nil
}

func author(kind, id, name string) any {
	return delta.JsonObjectOf("author", delta.JsonObjectOf("kind", kind, "id", id, "name", name), "text", "x", "attachments", []any{})
}

var (
	ownerInput = author("owner", "owner", "Owner")
	guestInput = author("guest", "g", "Guest")
)

func ran(t *testing.T, tool *durable.ToolRegistration, api durable.ToolExecutionApi) string {
	t.Helper()
	result, err := tool.Execute(context.Background(), map[string]any{}, api)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Output) == 0 {
		return ""
	}
	text, _ := result.Output[0].(ai.TextContent)
	return text.Text
}

func expectMatch(t *testing.T, got, pattern string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(got) {
		t.Errorf("%q does not match %s", got, pattern)
	}
}

func TestToolsWithoutRequirementsRunUnchanged(t *testing.T) {
	plain := accessTool("read", ToolAccess{})
	if guardTool(plain) != plain.ToolRegistration {
		t.Error("a tool without requirements was wrapped")
	}
	subagentOnly := accessTool("team", ToolAccess{Scope: "subagent"})
	if guardTool(subagentOnly) != subagentOnly.ToolRegistration {
		t.Error("a subagent-only tool was wrapped")
	}
}

func TestAnOwnerOnlyToolRefusesATurnTheOwnerDidNotStartAndAMainOnlyToolOutsideTheMainConversation(t *testing.T) {
	owned := guardTool(accessTool("ports", ToolAccess{Origin: "owner", Scope: "main"}))
	if got := ran(t, owned, accessAPI{conversation: 1, data: ownerInput}); got != "ports ran" {
		t.Errorf("the owner's turn: %q", got)
	}
	expectMatch(t, ran(t, owned, accessAPI{conversation: 1, data: guestInput}), `needs a turn the owner started`)
	// Conversation 2 is no top-level conversation (no `/tree` branches): a subagent's.
	expectMatch(t, ran(t, owned, accessAPI{conversation: 2, data: ownerInput}), `works only in the session's main`)
	anywhere := guardTool(accessTool("location_envs", ToolAccess{Origin: "owner"}))
	if got := ran(t, anywhere, accessAPI{conversation: 2, data: ownerInput}); got != "location_envs ran" {
		t.Errorf("an owner-only tool anywhere: %q", got)
	}
}

func TestInALockedSessionTheLockDecidesAnOwnerOnlyToolForEveryoneTheOwnerToo(t *testing.T) {
	lock := &SessionLock{MCP: []string{}, Capabilities: []LockCapability{"ports"}}
	ports := guardTool(accessTool("ports", ToolAccess{Origin: "owner", Locked: "ports"}))
	conversations := guardTool(accessTool("conversations", ToolAccess{Origin: "owner", Locked: "conversations"}))
	reload := guardTool(accessTool("reload_plugins", ToolAccess{Origin: "owner", Locked: "never"}))
	location := guardTool(accessTool("location_envs", ToolAccess{Origin: "owner"}))
	if got := ran(t, ports, accessAPI{conversation: 1, data: guestInput, lock: lock}); got != "ports ran" {
		t.Errorf("picked: guests too: %q", got)
	}
	expectMatch(t, ran(t, conversations, accessAPI{conversation: 1, data: ownerInput, lock: lock}), `not among the resources`)
	expectMatch(t, ran(t, reload, accessAPI{conversation: 1, data: ownerInput, lock: lock}), `not available in a locked`)
	if got := ran(t, location, accessAPI{conversation: 1, data: guestInput, lock: lock}); got != "location_envs ran" {
		t.Errorf("pass: %q", got)
	}
}
