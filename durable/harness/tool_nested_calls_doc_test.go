package harness

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// Pi 4ac0bd8c harness/tool.ts: one document per nested call (NestedCallDoc, `pi.tool.nested-call`), a task-scoped family of the
// caller keyed by the call's key. Admission creates the member; the nested call's terminal commit adds its result.

// createdDocs records the kind and key of every document a commit creates.
type createdDocs struct {
	durable.Storage
	mu   sync.Mutex
	docs []string
}

func (recorder *createdDocs) Commit(ctx context.Context, writes []durable.StorageWrite) (durable.Seq, error) {
	for _, write := range writes {
		if create, ok := write.(durable.DocumentCreateWrite); ok {
			key := "<none>"
			if create.Record.Key != nil {
				key = *create.Record.Key
			}
			recorder.mu.Lock()
			recorder.docs = append(recorder.docs, create.Record.Kind+" "+key)
			recorder.mu.Unlock()
		}
	}
	return recorder.Storage.Commit(ctx, writes)
}

// harness-nested-tools.test.ts:99 it("stores one document per nested call, keyed by its key, written only by that call")
func TestOneDocumentPerNestedCallKeyedByItsKey(t *testing.T) {
	setup := chatSetup(t)
	addTool(t, setup.Registry, echoRegistration())
	addTool(t, setup.Registry, tlTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		// Promise.all of both calls.
		errs := make([]error, 2)
		promiseAll(
			func() { _, errs[0] = api.ExecuteTool(ctx, "echo", map[string]any{"text": "a"}) },
			func() {
				_, errs[1] = api.ExecuteTool(ctx, "echo", map[string]any{"text": "b"}, durable.NestedCallOptions{Key: new("named")})
			},
		)
		return durable.ToolExecutionResult{}, errors.Join(errs...)
	}))
	setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"batch", map[string]any{}, "c1"}), tlDone()})
	recorder := &createdDocs{Storage: storage.NewMemoryStorage()}
	harness, root := openChat(t, recorder, setup)
	if _, err := ownSubmit(t, root, ownInput("go")).Wait(testContext); err != nil {
		t.Fatal(err)
	}
	recorder.mu.Lock()
	var nested []string
	for _, doc := range recorder.docs {
		if strings.HasPrefix(doc, "pi.tool.") {
			nested = append(nested, doc)
		}
	}
	recorder.mu.Unlock()
	if want := []string{"pi.tool.nested-call 1", "pi.tool.nested-call named"}; !slices.Equal(nested, want) {
		t.Fatalf("documents created for nested calls %v, want %v", nested, want)
	}
	mustClose(t, harness)
}

// harness-nested-tools.test.ts:134 it("rejects a nested call of an aborted caller and leaves no document for it"): a nested
// call's admission is a commit of the calling task, so once the caller has its abort mark executeTool rejects, and no
// nested-call document exists for the call.
func TestANestedCallOfAnAbortedCallerRejectsAndLeavesNoDocumentForIt(t *testing.T) {
	setup := chatSetup(t)
	addTool(t, setup.Registry, echoRegistration())
	started, marked := make(chan struct{}), make(chan struct{})
	var caller durable.TaskId
	var rejected error
	addTool(t, setup.Registry, tlTool("late", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		caller = api.TaskId()
		close(started)
		<-marked
		// Its context is aborted too, so pass one that is not: the admission itself must refuse.
		_, rejected = api.ExecuteTool(testContext, "echo", map[string]any{"text": "a"})
		return durable.ToolExecutionResult{}, nil
	}))
	setup.Faux.SetResponses([]ai.FauxResponseStep{tlCallsStep(tlCall{"late", map[string]any{}, "c1"}), tlDone()})
	harness, root := openChat(t, storage.NewMemoryStorage(), setup)
	submission := ownSubmit(t, root, ownInput("go"))
	<-started
	if _, err := harness.Commit(testContext, func(tx durable.Tx) (any, error) {
		record, err := tx.Task(caller)
		if err != nil {
			return nil, err
		}
		mark := *record
		mark.AbortRequested = true
		return nil, tx.(*session.Transaction).SetTask(mark)
	}); err != nil {
		t.Fatal(err)
	}
	close(marked)
	if _, err := submission.Wait(testContext); err != nil {
		t.Fatal(err)
	}
	if rejected == nil || !regexp.MustCompile(`abort mark|aborted|has settled`).MatchString(rejected.Error()) {
		t.Fatalf("the nested call returned %v, want a rejection for the abort mark", rejected)
	}
	if state := must(durable.Snapshot[NestedCallState](testContext, harness, NestedCallDoc, caller, "1")); state != nil {
		t.Fatalf("nested-call document %+v, want none", state)
	}
	mustClose(t, harness)
}
