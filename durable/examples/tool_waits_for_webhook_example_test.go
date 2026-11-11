// Ports packages/durable/test/examples/40-tool-waits-for-webhook.ts: a tool that starts a CI build and parks until the
// build's webhook arrives, holding no process meanwhile. A crash before the park reruns the tool, which finds its build
// again through its own idempotency key; Stop while it is parked runs the tool's `cancel`, which cancels the build. The
// script's printing is the transcript this test reads.
//
// Go mapping: the first process "dies" inside the tool, which waits for the invocation context to end (upstream waits
// for the abort signal) and is released when the first Harness closes at the end; the pauses of a few milliseconds
// become settledWake.

package examples_test

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
	"github.com/MichaelKinsy/PiG/schema"
)

type ciResult struct {
	Status string `json:"status"`
	LogUrl string `json:"logUrl"`
}

type ciCheckpoint struct {
	BuildId string `json:"buildId"`
}

// ciBuild is a build of the CI service.
type ciBuild struct {
	Id        string `json:"id"`
	Ref       string `json:"ref"`
	Cancelled bool   `json:"cancelled"`
}

// Pi: packages/durable/src/harness/parks.ts, packages/durable/src/harness/tool.ts (parks, cancel).
func TestExample40ToolWaitsForWebhook(t *testing.T) {
	const hour = 60 * 60 * 1000

	// ─── The outside world: a CI service and the host's webhook registry ──────────

	// Builds by idempotency key: triggering twice with one key starts one build.
	var ciMu sync.Mutex
	var builds []*ciBuild
	buildsByKey := map[string]*ciBuild{}
	buildIds := func() []string {
		ciMu.Lock()
		defer ciMu.Unlock()
		ids := []string{}
		for _, build := range builds {
			ids = append(ids, build.Id)
		}
		return ids
	}
	trigger := func(ref, idempotencyKey string) string {
		ciMu.Lock()
		defer ciMu.Unlock()
		build := buildsByKey[idempotencyKey]
		if build == nil {
			build = &ciBuild{Id: fmt.Sprintf("build-%d", len(builds)+1), Ref: ref}
			builds = append(builds, build)
			buildsByKey[idempotencyKey] = build
		}
		return build.Id
	}
	cancelBuild := func(buildId string) {
		ciMu.Lock()
		defer ciMu.Unlock()
		for _, build := range builds {
			if build.Id == buildId {
				build.Cancelled = true
			}
		}
	}
	cancelByKey := func(idempotencyKey string) {
		ciMu.Lock()
		defer ciMu.Unlock()
		if build := buildsByKey[idempotencyKey]; build != nil {
			build.Cancelled = true
		}
	}

	ciResultSignal := durable.DefineSignal(durable.SignalDefinition[ciResult]{
		Name: "example.ci-result", Version: 1,
		Schema: schema.Obj(schema.Properties{schema.Prop("status", schema.OneOf([]any{"passed", "failed"})), schema.Prop("logUrl", schema.Str())}),
	})

	// "When the webhook for `match` arrives, signal this key to this task": durable and idempotent in a real host.
	type expectation struct {
		taskId durable.TaskId
		key    string
	}
	var webhooksMu sync.Mutex
	expected := map[string]expectation{}
	var expectedOrder []string
	expect := func(match string, taskId durable.TaskId, key string) {
		webhooksMu.Lock()
		defer webhooksMu.Unlock()
		if _, found := expected[match]; !found {
			expectedOrder = append(expectedOrder, match)
		}
		expected[match] = expectation{taskId: taskId, key: key}
	}
	// deliver is the CI service calling back; the host turns it into a signal.
	deliver := func(opened harness.Harness, match string, payload ciResult) durable.SignalResult {
		webhooksMu.Lock()
		target := expected[match]
		webhooksMu.Unlock()
		return must(harness.Signal(background, opened, target.taskId, ciResultSignal, target.key, payload))
	}

	// ─── The tool ─────────────────────────────────────────────────────────────────

	var script printed
	// Set by the host below to make the first attempt "crash" after it triggered the build.
	var crashAfterTrigger atomic.Bool
	var crashedOnce sync.Once
	crashed := make(chan struct{})
	runCi := &durable.ToolRegistration{
		Name:        "run_ci",
		Description: "Run CI on a git ref and report the result",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"ref": map[string]any{"type": "string"}},
			"required":   []any{"ref"},
		},
		// A rerun after a crash is safe: it finds its build again by its idempotency key.
		Replay: durable.ReplaySafe,
		Execute: func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			ref := args.(map[string]any)["ref"].(string)
			// The result, the deadline's expiry, or nothing yet (a fresh call, or a rerun after a crash before the park).
			state, err := durable.ReadSignal(ctx, api, ciResultSignal, "done")
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if state != nil && !state.Expired {
				isError := state.Value.Status != "passed"
				return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: fmt.Sprintf("CI %s: %s", state.Value.Status, state.Value.LogUrl)}}, IsError: &isError}, nil
			}
			if state != nil {
				checkpoint, err := checkpointOf[ciCheckpoint](api)
				if err != nil {
					return durable.ToolExecutionResult{}, err
				}
				isError := true
				return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: fmt.Sprintf("CI build %s never reported", checkpoint.BuildId)}}, IsError: &isError}, nil
			}
			// The tool's own idempotency key, stable across reruns: memos are first-writer-wins.
			runId, err := api.MemoCandidate(ctx, "ci.run", uuid.NewString())
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			// Register before triggering, so a fast webhook already finds this task.
			expect(runId.(string), api.TaskId(), "done")
			buildId := trigger(ref, runId.(string))
			if err := api.Details(ctx, delta.JsonObjectOf("buildId", buildId)); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if crashAfterTrigger.Load() {
				crashedOnce.Do(func() { close(crashed) })
				// The process dies here; this attempt never parks.
				<-ctx.Done()
				return durable.ToolExecutionResult{}, context.Cause(ctx)
			}
			until := api.Now() + 2*hour
			return durable.ToolExecutionResult{Park: &durable.ToolPark{Sleep: &durable.ToolParkSleep{
				Checkpoint: delta.JsonObjectOf("buildId", buildId), HasCheckpoint: true,
				Signals: []durable.SignalKey{ciResultSignal.Key("done")}, Until: &until,
			}}}, nil
		},
		Cancel: func(ctx context.Context, _ any, api durable.ToolExecutionApi) error {
			// Parked: the checkpoint names the build. Cut off before a park was stored: the idempotency key finds it.
			if _, parked := api.Checkpoint(); parked {
				checkpoint, err := checkpointOf[ciCheckpoint](api)
				if err != nil {
					return err
				}
				script.add("  [cancel] cancelling " + checkpoint.BuildId)
				cancelBuild(checkpoint.BuildId)
				return nil
			}
			script.add("  [cancel] cancelling the build of this call's key")
			runId, _, err := api.Memo(ctx, "ci.run")
			if err != nil {
				return err
			}
			cancelByKey(runId.(string))
			return nil
		},
	}

	// ─── Host ─────────────────────────────────────────────────────────────────────

	faux := ai.NewFauxProvider(ai.FauxConfig{})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	faux.SetResponses([]ai.FauxResponseStep{
		fauxToolTurn("run_ci", map[string]any{"ref": "main"}, "ci-1"),
		fauxAnswer("CI is green."),
		fauxToolTurn("run_ci", map[string]any{"ref": "feature"}, "ci-2"),
	})
	registry := harness.CreateRegistry()
	installed(t, registry, &durable.Extension{Name: "ci", Tools: []*durable.ToolRegistration{runCi}})
	databasePath := filepath.Join(t.TempDir(), "session.sqlite")
	open := func() harness.Harness {
		t.Helper()
		store, err := sqlitenode.OpenNodeSqliteStorage(databasePath, sqlitenode.NodeSqliteStorageOptions{})
		if err != nil {
			t.Fatal(err)
		}
		opened, err := harness.OpenHarness(background, store, harness.HarnessOptions{Models: models, Registry: registry, OnWake: func(harness.Wake) {}})
		if err != nil {
			t.Fatal(err)
		}
		return opened
	}
	slot := func(opened harness.Harness, root harness.Conversation) *harness.ToolSlot {
		tools := liveOf(t, opened, root).Tools
		if len(tools) == 0 {
			return nil
		}
		return &tools[0]
	}
	parked := func(opened harness.Harness, root harness.Conversation) durable.TaskId {
		t.Helper()
		var taskId durable.TaskId
		eventually(t, func() bool {
			found := slot(opened, root)
			if found == nil || found.Status != harness.ToolSlotSleeping {
				return false
			}
			taskId = *found.TaskId
			return true
		})
		return taskId
	}

	// 1. The first process starts the build and dies before the park is stored.
	crashAfterTrigger.Store(true)
	first := open()
	firstRoot := must(first.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(fauxModel)}}))
	first.Resume()
	submission := must(firstRoot.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("Run CI on main")}))
	submissionId := submission.Id()
	<-crashed
	script.add(fmt.Sprintf("builds after the first attempt: %v", buildIds()))

	// 2. Another process opens the same database. The replay-safe call runs again and finds its build by its key.
	crashAfterTrigger.Store(false)
	opened := open()
	root := must(opened.Root(background, nil))
	opened.Resume()
	parked(opened, root)
	script.add(fmt.Sprintf("builds after the rerun: %v", buildIds()))
	parkedSlot := slot(opened, root)
	// Parked work holds no invocation: only its deadline needs a wakeup.
	wake := settledWake(opened)
	if wake.At == nil {
		t.Fatalf("wake while parked = %s, want a deadline", wakeText(wake))
	}
	deadlineHours := math.Round((*wake.At - float64(time.Now().UnixMilli())) / hour)
	script.add(fmt.Sprintf("wake while parked: active %t, deadline in %v h", wake.Active, deadlineHours))

	// 3. The webhook arrives; the host delivers it, and the call runs again with the result. Retries are duplicates.
	webhooksMu.Lock()
	match := expectedOrder[0]
	webhooksMu.Unlock()
	script.add("webhook: " + string(deliver(opened, match, ciResult{Status: "passed", LogUrl: "https://ci/1"})))
	script.add("retry: " + string(deliver(opened, match, ciResult{Status: "passed", LogUrl: "https://ci/1"})))
	settled := must(opened.Submission(background, submissionId))
	if settled == nil {
		t.Fatal("the first process's submission is gone")
	}
	waitSubmission(t, settled)
	script.add("result: " + newestToolResult(t, root, 10))

	// 4. A second build, stopped while parked: `cancel` cancels it, and the call ends aborted.
	must(root.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("Run CI on feature")}))
	parked(opened, root)
	if err := root.Abort(background, nil); err != nil {
		t.Fatal(err)
	}
	ciMu.Lock()
	script.add("builds: " + jsonText(builds))
	ciMu.Unlock()
	script.add("result: " + newestToolResult(t, root, 10))

	if parkedSlot == nil || parkedSlot.TaskId == nil || parkedSlot.Name != "run_ci" || parkedSlot.Status != harness.ToolSlotSleeping || parkedSlot.Output != nil || parkedSlot.Details == nil {
		t.Fatalf("parked slot = %+v, want the sleeping run_ci call without output, with its details", parkedSlot)
	}
	expectEqual(t, "parked slot details", jsonText(*parkedSlot.Details), `{"buildId":"build-1"}`)
	expectEqual(t, "script", script.all(), []string{
		"builds after the first attempt: [build-1]",
		"builds after the rerun: [build-1]",
		"wake while parked: active false, deadline in 2 h",
		"webhook: delivered",
		"retry: duplicate",
		"result: CI passed: https://ci/1",
		"  [cancel] cancelling build-2",
		`builds: [{"id":"build-1","ref":"main","cancelled":false},{"id":"build-2","ref":"feature","cancelled":true}]`,
		"result: Tool run_ci was aborted",
	})
	if err := opened.Close(background); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(background); err != nil {
		t.Fatal(err)
	}
}
