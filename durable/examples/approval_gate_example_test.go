// Ports packages/durable/test/examples/44-approval-gate.ts: gates before a tool runs: an approvals extension parks a risky
// `bash` call until a person approves it, keyed by the arguments it saw, with a deadline fixed at its first pass. A
// second gate (quiet hours) is asked again on every wake, since the whole `call` phase runs again. Denial and expiry end
// the call as blocks. The script's printing is the transcript this test reads.
//
// Go mapping: the script's pauses of a few milliseconds become waits for the parked state and settledWake; the Harness
// clock is read from several goroutines, so it is atomic.

package examples_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/schema"
)

type approvalDecision struct {
	Allow bool `json:"allow"`
}

// Pi: packages/durable/src/harness/tool.ts (gates: beforeTool sleep).
func TestExample44ApprovalGate(t *testing.T) {
	const day = 24 * 60 * 60 * 1000
	var clock atomic.Int64
	start := time.Date(2026, time.November, 9, 9, 0, 0, 0, time.UTC).UnixMilli()
	clock.Store(start)

	bash := &durable.ToolRegistration{
		Name:        "bash",
		Description: "Run a shell command",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"command": map[string]any{"type": "string"}},
			"required":   []any{"command"},
		},
		Execute: func(_ context.Context, args any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return textResult("ran: " + args.(map[string]any)["command"].(string)), nil
		},
	}

	// ─── The approvals extension ──────────────────────────────────────────────────

	approvalSignal := durable.DefineSignal(durable.SignalDefinition[approvalDecision]{
		Name: "approvals.decision", Version: 1, Schema: schema.Obj(schema.Properties{schema.Prop("allow", schema.Bool())}),
	})
	isRisky := func(call harness.ToolHookCall) bool {
		command, _ := call.Arguments["command"].(string)
		return strings.HasPrefix(command, "rm ")
	}
	// A digest of the arguments; other arguments, another question. (Sorting keys is left out: there is one argument.)
	canonicalDigest := func(value any) string { return base64.RawURLEncoding.EncodeToString([]byte(jsonText(value))) }
	// What shows the question keeps its own record, by task ID and key.
	type question struct {
		taskId  durable.TaskId
		command string
	}
	var questionsMu sync.Mutex
	questions := map[string]question{}

	approvals := &durable.Extension{
		Name: "approvals",
		Hooks: []durable.HookRegistration{harness.Hook(harness.ToolTask, &harness.ToolHooks{
			BeforeTool: func(ctx context.Context, call harness.ToolHookCall, api harness.HookApi) (*harness.BeforeToolResult, error) {
				if call.Name != "bash" || !isRisky(call) {
					return nil, nil
				}
				key := canonicalDigest(call.Arguments)
				state, err := durable.ReadSignal(ctx, api, approvalSignal, key)
				if err != nil {
					return nil, err
				}
				// One deadline per question, fixed at the first pass: later passes (wakes, registry changes) keep it.
				fixed, err := api.MemoCandidate(ctx, "approvals.until."+key, api.Now()+day)
				if err != nil {
					return nil, err
				}
				until := fixed.(float64)
				if state == nil {
					questionsMu.Lock()
					questions[key] = question{taskId: api.TaskId(), command: call.Arguments["command"].(string)}
					questionsMu.Unlock()
					return &harness.BeforeToolResult{Sleep: &harness.GateSleep{Signals: []durable.SignalKey{approvalSignal.Key(key)}, Until: &until}}, nil
				}
				questionsMu.Lock()
				delete(questions, key)
				questionsMu.Unlock()
				if state.Expired {
					return &harness.BeforeToolResult{Block: new("No approval within a day")}, nil
				}
				if state.Value.Allow {
					return nil, nil
				}
				return &harness.BeforeToolResult{Block: new("Not approved")}, nil
			},
		})},
	}

	// A second gate, installed first: asked again on every wake, it reads the clock each time.
	var quietChecks atomic.Int32
	quietHours := &durable.Extension{
		Name: "quiet-hours",
		Hooks: []durable.HookRegistration{harness.Hook(harness.ToolTask, &harness.ToolHooks{
			BeforeTool: func(_ context.Context, _ harness.ToolHookCall, api harness.HookApi) (*harness.BeforeToolResult, error) {
				quietChecks.Add(1)
				hour := time.UnixMilli(int64(api.Now())).UTC().Hour()
				if hour >= 22 || hour < 6 {
					return &harness.BeforeToolResult{Block: new("Quiet hours: no commands at night")}, nil
				}
				return nil, nil
			},
		})},
	}

	// ─── Host ─────────────────────────────────────────────────────────────────────

	command := func(text string) ai.FauxResponseStep {
		return fauxToolTurn("bash", map[string]any{"command": text}, "")
	}
	models := fauxModels(
		command("rm -rf build"),
		fauxAnswer("Cleaned."),
		command("rm -rf dist"),
		fauxAnswer("Not cleaned."),
		command("rm -rf cache"),
		fauxAnswer("Nobody answered."),
	)
	registry := harness.CreateRegistry()
	for _, extension := range []*durable.Extension{{Name: "tools", Tools: []*durable.ToolRegistration{bash}}, quietHours, approvals} {
		installed(t, registry, extension)
	}
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{
		Models: models, Registry: registry,
		Now:    func() float64 { return float64(clock.Load()) },
		OnWake: func(harness.Wake) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	root := must(opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(fauxModel)}}))

	var script printed
	type asked struct {
		key string
		question
	}
	waitAsked := func() asked {
		var found asked
		eventually(t, func() bool {
			questionsMu.Lock()
			defer questionsMu.Unlock()
			for key, question := range questions {
				found = asked{key: key, question: question}
				return true
			}
			return false
		})
		// Parked at its gates: the call's task sleeps, and nothing runs.
		waitSleeping(t, opened, found.taskId)
		return found
	}
	run := func(input string, answer func(key string, taskId durable.TaskId)) {
		t.Helper()
		quietChecks.Store(0)
		submission := must(root.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(input)}))
		question := waitAsked()
		wake := settledWake(opened)
		slot := liveOf(t, opened, root).Tools[0]
		script.add(fmt.Sprintf("asked to approve %q; the call is %s; wake: %s", question.command, slot.Status, wakeText(wake)))
		answer(question.key, question.taskId)
		waitSubmission(t, submission)
		script.add(fmt.Sprintf("  result: %s (quiet-hours gate asked %d times)", strings.ReplaceAll(newestToolResult(t, root, 20), "\n", " "), quietChecks.Load()))
	}

	wakeAtDeadline := fmt.Sprintf(`{"active":false,"at":%d}`, start+day)
	// 1. Approved: the whole call phase runs again, both gates pass, and the command runs.
	run("Clean build", func(key string, taskId durable.TaskId) {
		script.add("  approve: " + string(must(harness.Signal(background, opened, taskId, approvalSignal, key, approvalDecision{Allow: true}))))
	})
	// 2. Denied: a block.
	run("Clean dist", func(key string, taskId durable.TaskId) {
		script.add("  deny: " + string(must(harness.Signal(background, opened, taskId, approvalSignal, key, approvalDecision{Allow: false}))))
	})
	// 3. Nobody answers within a day: the deadline wakes it, and the gate blocks.
	run("Clean cache", func(string, durable.TaskId) {
		clock.Add(day)
		opened.Resume()
	})

	expectEqual(t, "script", script.all(), []string{
		`asked to approve "rm -rf build"; the call is sleeping; wake: ` + wakeAtDeadline,
		"  approve: delivered",
		"  result: ran: rm -rf build (quiet-hours gate asked 2 times)",
		`asked to approve "rm -rf dist"; the call is sleeping; wake: ` + wakeAtDeadline,
		"  deny: delivered",
		"  result: Tool call blocked: Not approved (quiet-hours gate asked 2 times)",
		`asked to approve "rm -rf cache"; the call is sleeping; wake: ` + wakeAtDeadline,
		"  result: Tool call blocked: No approval within a day (quiet-hours gate asked 2 times)",
	})
	if err := opened.Close(background); err != nil {
		t.Fatal(err)
	}
}
