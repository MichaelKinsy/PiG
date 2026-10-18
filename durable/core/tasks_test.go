package core

import (
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/inbox"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

func registryOf(t *testing.T, text string) *jv.Object {
	t.Helper()
	v, err := jv.ParseString(text)
	if err != nil {
		t.Fatal(err)
	}
	return v.(*jv.Object)
}

const taskRegistry = `{"extensions":[{"name":"x","tasks":[{"name":"pay","version":1,"phases":["a"],"abort":true,"handler":3}]}]}`

// The scheduler detects a registry reload by the identity of a task definition between phases (harness handover), so a
// definition the host sends unchanged keeps its TaskImpl across registry events and a changed one gets a new one.
func TestLoadRegistryKeepsTheIdentityOfAnUnchangedTaskDefinition(t *testing.T) {
	h := &handler{runtimes: map[session.ID]*session.Runtime{}}
	h.loadRegistry(registryOf(t, taskRegistry))
	first := h.snap.Task("pay")
	if first == nil || first.Def.Version != 1 || first.Abort == nil {
		t.Fatalf("the custom task is in the snapshot: %+v", first)
	}
	h.loadRegistry(registryOf(t, taskRegistry))
	if h.snap.Task("pay") != first {
		t.Fatal("an unchanged definition must keep its identity")
	}
	h.loadRegistry(registryOf(t, `{"extensions":[{"name":"x","tasks":[{"name":"pay","version":2,"phases":["a"],"abort":true,"handler":3}]}]}`))
	if h.snap.Task("pay") == first || h.snap.Task("pay").Def.Version != 2 {
		t.Fatal("a changed definition is a new TaskImpl")
	}
	h.loadRegistry(registryOf(t, `{"extensions":[]}`))
	if h.snap.Task("pay") != nil {
		t.Fatal("a removed task leaves the snapshot")
	}
}

// Pi's error names cross the boundary: a busy conversation is ConversationBusy, with Pi's message.
func TestErrorNameOfAnInboxBusyError(t *testing.T) {
	err := error(&inbox.BusyError{ConversationID: 1})
	if errorName(err) != "ConversationBusy" || errorMessage(err) != "Conversation 1 is busy" {
		t.Fatalf("%s: %s", errorName(err), errorMessage(err))
	}
	// The wire body carries the id in the structured conversationId slot (the TS host's reviveError reads body.conversationId first).
	if got, want := string(errorJSON(err)), `{"name":"ConversationBusy","message":"Conversation 1 is busy","conversationId":1}`; got != want {
		t.Fatalf("errorJSON = %s, want %s", got, want)
	}
	if got, want := string(errorJSON(errors.New("x"))), `{"name":"Error","message":"x"}`; got != want {
		t.Fatalf("errorJSON = %s, want %s", got, want)
	}
}

// Tx.createTask requires options.ownership (transaction.ts createTask reads ownership.kind): no ownership is a
// TypeError, and a task owner that does not exist rejects.
func TestCreateTaskRequiresOwnership(t *testing.T) {
	mem := sessiontest.New()
	s := session.New(mem, mem.NextID(), func() float64 { return 1 })
	h := &handler{}
	_, err := s.Commit(func(tx *session.Tx) error {
		root, err := tx.CreateRootConversation()
		if err != nil {
			return err
		}
		cid, _ := root.Get("id")
		bound := func(options string) (any, error) {
			op := parsed(t, `{"task":"t.x","version":1,"state":{},"input":null,"options":`+options+`}`)
			return h.createTask(tx, op)
		}
		if _, err := bound(`{"conversationId":` + jv.Stringify(cid) + `}`); err == nil || errorName(err) != "TypeError" {
			t.Errorf("no ownership: %v", err)
		}
		if _, err := bound(`{"ownership":{"kind":"conversation"},"conversationId":` + jv.Stringify(cid) + `}`); err != nil {
			t.Errorf("conversation ownership: %v", err)
		}
		if _, err := bound(`{"ownership":{"kind":"task","taskId":999999}}`); err == nil || !strings.Contains(err.Error(), "does not exist") {
			t.Errorf("missing owner: %v", err)
		}
		return nil
	}, session.TxScope{})
	if err != nil {
		t.Fatal(err)
	}
}

// A built-in task's record carries the version of the definition the caller passed (a run task stored by a newer
// generation definition stays task_too_old), not the core's own.
func TestCreateBuiltinTaskKeepsTheCallersVersion(t *testing.T) {
	mem := sessiontest.New()
	s := session.New(mem, mem.NextID(), func() float64 { return 1 })
	h := &handler{builtins: []*session.TaskImpl{{Def: session.TaskDef{Name: "pi.generation", Version: 1, Initial: func(any) any { return jv.NewObject() }}}}}
	var id any
	if _, err := s.Commit(func(tx *session.Tx) error {
		root, err := tx.CreateRootConversation()
		if err != nil {
			return err
		}
		cid, _ := root.Get("id")
		id, err = h.createTask(tx, parsed(t, `{"task":"pi.generation","version":2,"input":{},"options":{"ownership":{"kind":"conversation"},"conversationId":`+jv.Stringify(cid)+`}}`))
		return err
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	rec, _ := mem.Task(session.ID(id.(float64)))
	if v, _ := rec.Get("version"); v != 2.0 {
		t.Fatalf("version %v, want 2", v)
	}
}

// A throw from host code reads as the error's message (harness/tool.ts errorText), keeps its name for classification,
// and is not mistaken for a protocol rejection.
func TestThrownErrorReadsAsItsMessage(t *testing.T) {
	err := thrown(parsed(t, `{"name":"RangeError","message":"too big"}`))
	if err.Error() != "too big" || errorName(err) != "RangeError" || errorMessage(err) != "too big" {
		t.Fatalf("%q %q %q", err.Error(), errorName(err), errorMessage(err))
	}
	if anon := thrown(parsed(t, `{"message":"x"}`)); errorName(anon) != "Error" {
		t.Fatalf("an unnamed throw is an Error: %q", errorName(anon))
	}
}

// The core runs the hooks of the conversation's selected extensions only, in extension order (agent.ts agentHooks), as
// the resolved agent's hook tables name them.
func TestHookHandlersFollowTheAgentsSelection(t *testing.T) {
	agent := agentOf(parsed(t, `{"thinkingLevel":"off","hooks":[{"task":"pi.tool","handlers":{"beforeTool":4}},{"task":"pi.tool","handlers":{"beforeTool":9,"afterTool":10}}]}`))
	h := &handler{agents: map[session.ID]*turn.Agent{1: agent}}
	if got := h.hookHandlers("beforeTool", 1); len(got) != 2 || got[0] != 4 || got[1] != 9 {
		t.Fatalf("beforeTool handlers %v", got)
	}
	if got := h.hookHandlers("afterTool", 1); len(got) != 1 || got[0] != 10 {
		t.Fatalf("afterTool handlers %v", got)
	}
	if got := h.hookHandlers("beforeTool", 2); len(got) != 0 {
		t.Fatalf("a conversation without a resolved agent selects no hooks: %v", got)
	}
}

// The scheduler's report carries the ReportInfo of types.ts on the wire, after the error's name and message.
func TestReportCarriesTheInfo(t *testing.T) {
	b := abi.NewBuilder()
	h := &handler{b: b}
	h.report(errors.New("Task 3 keeps running under its old k definition"), jobj("type", "task_outdated", "task", jobj("id", float64(3), "kind", "k", "conversationId", float64(1)), "reason", "missing_task"))
	step := b.Step()
	if len(step.Notices) != 1 {
		t.Fatalf("notices %d", len(step.Notices))
	}
	got := string(step.Notices[0].Payload)
	if got != `{"name":"Error","message":"Task 3 keeps running under its old k definition","info":{"type":"task_outdated","task":{"id":3,"kind":"k","conversationId":1},"reason":"missing_task"}}` {
		t.Fatalf("%s", got)
	}
}

// A SessionFailed names itself, with the fixed message, even though the Storage error that failed the Session, which it
// wraps as its cause, is a thrown error with a name of its own (session.ts SessionFailed, harness util closedError).
func TestASessionFailedErrorKeepsItsNameOverItsCause(t *testing.T) {
	err := &session.FailedError{Cause: &ThrownError{Name: "Error", Message: "disk gone"}}
	want := `{"name":"SessionFailed","message":"Session failed after a storage error; close and reopen it"}`
	if got := string(errorJSON(err)); got != want {
		t.Fatalf("errorJSON = %s, want %s", got, want)
	}
}
