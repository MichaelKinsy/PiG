// Ports packages/durable/test/harness-live-deltas.test.ts.

package harness

// pi: packages/durable/src/harness/live.ts

import (
	"context"
	"encoding/json"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// liveOps collects the exact Chord operations of every pi.live commit with operations.
type liveOps struct {
	mu      sync.Mutex
	commits [][]durable.Op
}

func observeLiveOps(harness Harness) *liveOps {
	collected := &liveOps{}
	harness.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
		for _, change := range documentChanges(publication) {
			if change.Record.Kind == "pi.live" && len(change.Ops) > 0 {
				collected.mu.Lock()
				collected.commits = append(collected.commits, slices.Clone(change.Ops))
				collected.mu.Unlock()
			}
		}
	})
	return collected
}

func (collected *liveOps) all() [][]durable.Op {
	collected.mu.Lock()
	defer collected.mu.Unlock()
	return slices.Clone(collected.commits)
}

func (collected *liveOps) count() int {
	collected.mu.Lock()
	defer collected.mu.Unlock()
	return len(collected.commits)
}

func toolCallStep(calls ...ai.FauxContentBlock) ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{Content: calls, StopReason: "toolUse"})
}

func noopTool(name string) *durable.ToolRegistration {
	return DefineTool(durable.ToolRegistration{
		ToolSchema: ai.ToolSchema{Name: name, Description: name, Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
		Execute: func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}}, nil
		},
	})
}

// driver drives one tool call step by step and captures the exact Chord operations of every pi.live commit.
type driver struct {
	t          *testing.T
	harness    Harness
	ops        *liveOps
	actions    chan func(api durable.ToolExecutionApi) bool
	submission durable.Submission
}

func drive(t *testing.T, limits *durable.ToolOutputLimits) *driver {
	t.Helper()
	setup := chatSetup(t)
	actions := make(chan func(api durable.ToolExecutionApi) bool, 16)
	addTool(t, setup.Registry, DefineTool(durable.ToolRegistration{
		ToolSchema:   ai.ToolSchema{Name: "drive", Description: "Driven by the test", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
		OutputLimits: limits,
		Execute: func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			for {
				select {
				case action := <-actions:
					if !action(api) {
						return durable.ToolExecutionResult{}, nil
					}
				case <-ctx.Done():
					return durable.ToolExecutionResult{}, context.Cause(ctx)
				}
			}
		},
	}))
	setup.Faux.SetResponses([]ai.FauxResponseStep{toolCallStep(ai.FauxToolCall("drive", map[string]any{}, &ai.FauxToolCallOptions{ID: "c1"})), fauxAnswer("done")})
	harness, root := openChat(t, storage.NewMemoryStorage(), setup)
	ops := observeLiveOps(harness)
	submission := submitInput(t, root, "go")
	waitFor(t, func() bool {
		state := liveOf(t, harness, durable.ROOT_CONVERSATION_ID)
		return len(state.Tools) > 0 && state.Tools[0].Status == ToolSlotRunning
	})
	return &driver{t: t, harness: harness, ops: ops, actions: actions, submission: submission}
}

// step runs one action inside the tool and returns the operations of the commit it caused.
func (run *driver) step(action func(api durable.ToolExecutionApi)) []durable.Op {
	run.t.Helper()
	before := run.ops.count()
	run.actions <- func(api durable.ToolExecutionApi) bool {
		action(api)
		return true
	}
	waitFor(run.t, func() bool { return run.ops.count() > before })
	commits := run.ops.all()
	return commits[len(commits)-1]
}

// finish lets the tool return and the run finish; it returns the commits made meanwhile.
func (run *driver) finish() [][]durable.Op {
	run.t.Helper()
	before := run.ops.count()
	run.actions <- func(durable.ToolExecutionApi) bool { return false }
	must(run.submission.Wait(testContext))
	return run.ops.all()[before:]
}

const outputPath = `["tools",0,"output"]`

func isOutput(t *testing.T, op durable.Op) bool {
	return jsonText(t, op[1]) == outputPath
}

func TestLiveDeltas(t *testing.T) {
	t.Run("hands a generation over to its tool round and starts a tool with one field write each", func(t *testing.T) {
		setup := chatSetup(t)
		addTool(t, setup.Registry, noopTool("noop"))
		setup.Faux.SetResponses([]ai.FauxResponseStep{toolCallStep(ai.FauxToolCall("noop", map[string]any{}, &ai.FauxToolCallOptions{ID: "c1"})), fauxAnswer("done")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		ops := observeLiveOps(harness)
		must(submitInput(t, root, "go").Wait(testContext))
		tasks := conversationTasks(t, harness, root)
		ids := func(kind string) []durable.TaskId {
			found := []durable.TaskId{}
			for _, task := range tasks {
				if task.Kind == kind {
					found = append(found, task.Id)
				}
			}
			slices.Sort(found)
			return found
		}
		generations, tools := ids("pi.generation"), ids("pi.tool")
		var result durable.EntryId
		for _, entry := range allEntries(t, root) {
			if entry.Kind == "pi.tool-result" {
				result = entry.Id
			}
		}
		commits := ops.all()
		if len(commits) != 8 {
			t.Fatalf("commits = %s", jsonText(t, commits))
		}
		expectLike(t, commits[0], jsonText(t, []any{[]any{"s", []any{"run"}, map[string]any{"taskId": generations[0], "inputs": []any{"$number"}}}}))
		expectLike(t, commits[1], `[["s",["generation"],{"attempt":1}]]`)
		expectContainsLike(t, commits[2], jsonText(t, []any{[]any{"d", []any{"generation"}}, []any{"s", []any{"tools"}, []any{map[string]any{"callId": "c1", "name": "noop", "taskId": tools[0], "status": "pending"}}}}))
		expectLike(t, commits[3], `[["s",["tools",0,"status"],"running"]]`)
		expectContainsLike(t, commits[4], jsonText(t, []any{[]any{"s", []any{"tools", 0, "status"}, "done"}, []any{"s", []any{"tools", 0, "entry"}, result}}))
		expectContainsLike(t, commits[5], jsonText(t, []any{[]any{"d", []any{"tools"}}, []any{"s", []any{"run", "taskId"}, generations[1]}}))
		expectLike(t, commits[6], `[["s",["generation"],{"attempt":1}]]`)
		expectContainsLike(t, commits[7], `[["d",["run"]],["d",["generation"]]]`)
		for _, index := range []int{2, 4, 5} {
			if len(commits[index]) != 2 {
				t.Fatalf("commit %d = %s, want two operations", index, jsonText(t, commits[index]))
			}
		}
		closeHarness(t, harness)
	})

	t.Run("appends head output and then only updates the dropped counts once the window is full", func(t *testing.T) {
		run := drive(t, &durable.ToolOutputLimits{MaxLines: new(2)})
		expectLike(t, run.step(func(api durable.ToolExecutionApi) { api.Output("one\n") }), `[["s",`+outputPath+`,"one\n"]]`)
		expectLike(t, run.step(func(api durable.ToolExecutionApi) { api.Output("two\n") }), `[["a",`+outputPath+`,"two\n"]]`)
		// The window is full: the retained text stays; only the counts change.
		expectLike(t, run.step(func(api durable.ToolExecutionApi) { api.Output("three\n") }), `[["s",["tools",0,"droppedBytes"],6],["s",["tools",0,"droppedLines"],1]]`)
		expectLike(t, run.step(func(api durable.ToolExecutionApi) { api.Output("four\n") }), `[["s",["tools",0,"droppedBytes"],11],["s",["tools",0,"droppedLines"],2]]`)
		run.finish()
		closeHarness(t, run.harness)
	})

	t.Run("slides a tail window as a front trim plus an append", func(t *testing.T) {
		run := drive(t, &durable.ToolOutputLimits{MaxLines: new(3), Retain: durable.RetainTail})
		expectLike(t, run.step(func(api durable.ToolExecutionApi) { api.Output("line 1\nline 2\nline 3\n") }), `[["s",`+outputPath+`,"line 1\nline 2\nline 3\n"]]`)
		expectLike(t, run.step(func(api durable.ToolExecutionApi) { api.Output("line 4\n") }), `[["t",`+outputPath+`,7],["a",`+outputPath+`,"line 4\n"],["s",["tools",0,"droppedBytes"],7],["s",["tools",0,"droppedLines"],1]]`)
		// The buffer keeps only the window, and later slides stay minimal and exact.
		expectLike(t, run.step(func(api durable.ToolExecutionApi) { api.Output("line 5\nline 6\n") }), `[["t",`+outputPath+`,14],["a",`+outputPath+`,"line 5\nline 6\n"],["s",["tools",0,"droppedBytes"],21],["s",["tools",0,"droppedLines"],3]]`)
		if output := liveOf(t, run.harness, durable.ROOT_CONVERSATION_ID).Tools[0].Output; output == nil || *output != "line 4\nline 5\nline 6\n" {
			t.Fatalf("output = %v", output)
		}
		run.finish()
		closeHarness(t, run.harness)
	})

	t.Run("writes the whole window when Chord's overlap search cannot find the shared part", func(t *testing.T) {
		// A retained window beyond the 64 KiB overlap scan.
		wide := drive(t, &durable.ToolOutputLimits{MaxBytes: new(100 * 1024), MaxLines: new(1_000_000), Retain: durable.RetainTail})
		line := func(index int) string {
			return strings.Repeat("0", 10-len(itoa(index))) + itoa(index) + " " + strings.Repeat("x", 989) + "\n"
		}
		var text strings.Builder
		for index := range 100 {
			text.WriteString(line(index))
		}
		wide.step(func(api durable.ToolExecutionApi) { api.Output(text.String()) })
		slid := wide.step(func(api durable.ToolExecutionApi) { api.Output(line(100) + line(101) + line(102) + line(103)) })
		verbs := []any{}
		for _, op := range slid {
			if isOutput(t, op) {
				verbs = append(verbs, op[0])
			}
		}
		expectLike(t, verbs, `["s"]`)
		wide.finish()
		closeHarness(t, wide.harness)

		// Repetitive output still finds an overlap here; Chord's bounded candidate search can give up on other inputs
		// and then writes one window.
		repetitive := drive(t, &durable.ToolOutputLimits{MaxLines: new(50), Retain: durable.RetainTail})
		repetitive.step(func(api durable.ToolExecutionApi) { api.Output(strings.Repeat("y\n", 50)) })
		repeated := repetitive.step(func(api durable.ToolExecutionApi) { api.Output("z\n") })
		outputs := []durable.Op{}
		for _, op := range repeated {
			if isOutput(t, op) {
				outputs = append(outputs, op)
			}
		}
		expectLike(t, outputs, `[["t",`+outputPath+`,2],["a",`+outputPath+`,"z\n"]]`)
		repetitive.finish()
		closeHarness(t, repetitive.harness)
	})

	t.Run("diffs details leaf by leaf and appends diagnostics", func(t *testing.T) {
		run := drive(t, nil)
		details := `["tools",0,"details"`
		detailsStep := func(value durable.JsonValue) []durable.Op {
			return run.step(func(api durable.ToolExecutionApi) {
				if err := api.Details(testContext, value); err != nil {
					t.Error(err)
				}
			})
		}
		expectLike(t, detailsStep(map[string]any{"step": 1.0, "log": "a"}), `[["s",`+details+`],{"step":1,"log":"a"}]]`)
		expectContainsLike(t, detailsStep(map[string]any{"step": 2.0, "log": "ab"}), `[["s",`+details+`,"step"],2],["a",`+details+`,"log"],"b"]]`)
		expectLike(t, detailsStep(map[string]any{"step": 2.0}), `[["d",`+details+`,"log"]]]`)
		diagnostics := `["tools",0,"diagnostics"]`
		first := durable.ToolDiagnostic{Severity: "info", Message: "first"}
		second := durable.ToolDiagnostic{Severity: durable.SeverityWarn, Message: "second"}
		expectLike(t, run.step(func(api durable.ToolExecutionApi) { api.Diagnostic(first) }), `[["s",`+diagnostics+`,[{"severity":"info","message":"first"}]]]`)
		expectLike(t, run.step(func(api durable.ToolExecutionApi) { api.Diagnostic(second) }), `[["p",`+diagnostics+`,1,0,[{"severity":"warn","message":"second"}]]]`)
		// Settlement moves everything into the result entry and keeps the slot small.
		settled := run.finish()[0]
		expectContainsLike(t, settled, `[["s",["tools",0,"status"],"done"],["d",`+details+`]],["d",`+diagnostics+`]]`)
		closeHarness(t, run.harness)
	})

	t.Run("streams partial text as appends", func(t *testing.T) {
		setup := chatSetup(t, ai.FauxConfig{TokensPerSecond: 400, TokenSize: &ai.FauxTokenSize{Min: new(4), Max: new(4)}})
		setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAnswer(strings.Repeat("word ", 250))})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		ops := observeLiveOps(harness)
		must(submitInput(t, root, "go").Wait(testContext))
		commits := ops.all()
		partials := commits[2 : len(commits)-1]
		if len(partials) <= 2 {
			t.Fatalf("partials = %d, want more than 2", len(partials))
		}
		expectLike(t, partials[0], `[["s",["generation","message"],"$object"]]`)
		for _, partial := range partials[1:] {
			expectLike(t, partial, `[["a",["generation","message","content",0,"text"],"$string"]]`)
		}
		closeHarness(t, harness)
	})

	t.Run("stores a complete base exactly in the commits where nothing runs", func(t *testing.T) {
		// Storage that remembers whether each commit wrote pi.live as a base or a delta.
		store := &recordingStorage{MemoryStorage: storage.NewMemoryStorage(), written: map[durable.Seq]string{}}
		setup := chatSetup(t)
		for _, name := range []string{"first", "second"} {
			addTool(t, setup.Registry, DefineTool(durable.ToolRegistration{
				ToolSchema: ai.ToolSchema{Name: name, Description: name, Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
				Execute: func(_ context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
					api.Output(name + " output\n")
					time.Sleep(150 * time.Millisecond)
					api.Output(name + " more\n")
					return durable.ToolExecutionResult{}, nil
				},
			}))
		}
		setup.Faux.SetResponses([]ai.FauxResponseStep{toolCallStep(ai.FauxToolCall("first", map[string]any{}, &ai.FauxToolCallOptions{ID: "a"}), ai.FauxToolCall("second", map[string]any{}, &ai.FauxToolCallOptions{ID: "b"})), fauxAnswer("done")})
		setup.SetSettings(func(settings *HarnessSettings) { settings.ToolExecution = durable.ToolExecutionSequential })
		harness, root := openChat(t, store, setup)
		var mu sync.Mutex
		values := map[durable.Seq]LiveState{}
		harness.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
			for _, change := range documentChanges(publication) {
				if change.Record.Kind != "pi.live" {
					continue
				}
				store.setLive(change.Record.Id)
				if change.Value != nil {
					value := must(durable.FromJsonValue[LiveState](change.Value))
					mu.Lock()
					values[publication.Seq] = value
					mu.Unlock()
				}
			}
		})
		must(submitInput(t, root, "go").Wait(testContext))
		nothingRuns := func(value LiveState) bool {
			return value.Generation == nil && !slices.ContainsFunc(value.Tools, func(slot ToolSlot) bool { return slot.Status == ToolSlotRunning })
		}
		store.mu.Lock()
		written := maps.Clone(store.written)
		store.mu.Unlock()
		mu.Lock()
		observed := maps.Clone(values)
		mu.Unlock()
		bases, deltas := 0, 0
		for seq, kind := range written {
			if (kind == "base") != nothingRuns(observed[seq]) {
				t.Fatalf("commit %d wrote a %s for %s", seq, kind, jsonText(t, observed[seq]))
			}
			if kind == "base" {
				bases++
			} else {
				deltas++
			}
		}
		// Bases at the handover, after each sequential tool, after the round, and when the run ends.
		if bases < 5 || deltas == 0 {
			t.Fatalf("bases = %d, deltas = %d", bases, deltas)
		}
		closeHarness(t, harness)
	})

	t.Run("starts calls the request did not offer as done and marks a faulted tool's slot done without an entry", func(t *testing.T) {
		setup := chatSetup(t)
		addTool(t, setup.Registry, DefineTool(durable.ToolRegistration{
			ToolSchema: ai.ToolSchema{Name: "bad", Description: "bad", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
			// Not strict JSON: the result commit fails and the scheduler faults the task. A Go value cannot hold a
			// function, so a non-finite number stands for upstream's function.
			Execute: func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}, Details: map[string]any{"fn": math.NaN()}, HasDetails: true}, nil
			},
		}))
		setup.Faux.SetResponses([]ai.FauxResponseStep{toolCallStep(ai.FauxToolCall("ghost", map[string]any{}, &ai.FauxToolCallOptions{ID: "g"}), ai.FauxToolCall("bad", map[string]any{}, &ai.FauxToolCallOptions{ID: "b"})), fauxAnswer("done")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		ops := observeLiveOps(harness)
		must(submitInput(t, root, "go").Wait(testContext))
		var ghostResult durable.EntryId
		for _, entry := range allEntries(t, root) {
			if entry.Kind == "pi.tool-result" {
				ghostResult = entry.Id
				break
			}
		}
		commits := ops.all()
		var handover []durable.Op
		for _, commit := range commits {
			if slices.ContainsFunc(commit, func(op durable.Op) bool { return op[0] == "s" && jsonText(t, op[1]) == `["tools"]` }) {
				handover = commit
				break
			}
		}
		expectContainsLike(t, handover, jsonText(t, []any{[]any{"s", []any{"tools"}, []any{
			map[string]any{"callId": "g", "name": "ghost", "status": "done", "entry": ghostResult},
			map[string]any{"callId": "b", "name": "bad", "taskId": "$number", "status": "pending"},
		}}}))
		// The fault cleanup writes only the status.
		expectContainsLike(t, commits, `[[["s",["tools",1,"status"],"done"]]]`)
		closeHarness(t, harness)
	})

	t.Run("commits the tool-calling answer, its tool tasks, the generation's wait, and the tool round in one commit", func(t *testing.T) {
		setup := chatSetup(t)
		addTool(t, setup.Registry, noopTool("noop"))
		setup.Faux.SetResponses([]ai.FauxResponseStep{toolCallStep(ai.FauxToolCall("noop", map[string]any{}, &ai.FauxToolCallOptions{ID: "a"}), ai.FauxToolCall("noop", map[string]any{}, &ai.FauxToolCallOptions{ID: "b"})), fauxAnswer("done")})
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		var mu sync.Mutex
		var handover *durable.CommitPublication
		harness.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) {
			for _, change := range documentChanges(publication) {
				tools, _ := change.Value.Value("tools").([]any)
				mu.Lock()
				if change.Record.Kind == "pi.live" && len(tools) == 2 && handover == nil {
					handover = &publication
				}
				mu.Unlock()
			}
		})
		must(submitInput(t, root, "go").Wait(testContext))
		mu.Lock()
		published := handover
		mu.Unlock()
		kinds := []string{}
		for _, change := range published.Changes {
			switch typed := change.(type) {
			case durable.EntryWrite:
				kinds = append(kinds, typed.Value.Kind)
			case durable.TaskWrite:
				kinds = append(kinds, typed.Value.Kind)
			}
		}
		slices.Sort(kinds)
		expectLike(t, kinds, `["pi.assistant","pi.generation","pi.tool","pi.tool"]`)
		closeHarness(t, harness)
	})

	t.Run("writes an aborted tool's slot with field-level ops", func(t *testing.T) {
		run := drive(t, nil)
		run.step(func(api durable.ToolExecutionApi) { api.Output("partial\n") })
		run.step(func(api durable.ToolExecutionApi) {
			if err := api.Details(testContext, map[string]any{"n": 1.0}); err != nil {
				t.Error(err)
			}
		})
		taskId := *liveOf(t, run.harness, durable.ROOT_CONVERSATION_ID).Tools[0].TaskId
		before := run.ops.count()
		must(run.harness.AbortTask(testContext, taskId))
		isAbortCommit := func(ops []durable.Op) bool {
			return slices.ContainsFunc(ops, func(op durable.Op) bool { return op[0] == "s" && jsonText(t, op[1]) == `["tools",0,"status"]` })
		}
		var abortCommit []durable.Op
		waitFor(t, func() bool {
			for _, ops := range run.ops.all()[before:] {
				if isAbortCommit(ops) {
					abortCommit = ops
					return true
				}
			}
			return false
		})
		expectContainsLike(t, abortCommit, `[["s",["tools",0,"status"],"done"],["s",["tools",0,"entry"],"$number"],["d",`+outputPath+`],["d",["tools",0,"details"]]]`)
		if len(abortCommit) != 4 {
			t.Fatalf("abort commit = %s, want four operations", jsonText(t, abortCommit))
		}
		closeHarness(t, run.harness)
	})

	t.Run("keeps a complete base exactly while nothing runs", func(t *testing.T) {
		base := func(value string) bool {
			object := must(delta.DecodeJson([]byte(value))).(*delta.JsonObject)
			return must(LiveDoc.AnyDefinition().CheckpointWhen(object, nil, durable.CheckpointInfo{DeltasSinceBase: 1000}))
		}
		slot := func(status string) string { return `{"callId":"c","name":"n","status":"` + status + `"}` }
		run := `"run":{"taskId":1,"inputs":[]}`
		if !base(`{}`) || base(`{`+run+`,"generation":{"attempt":1}}`) || !base(`{`+run+`,"tools":[`+slot("pending")+`,`+slot("done")+`]}`) ||
			base(`{`+run+`,"tools":[`+slot("done")+`,`+slot("running")+`]}`) || !base(`{`+run+`}`) {
			t.Fatal("pi.live checkpoint predicate disagrees with whether something runs")
		}
	})
}

// recordingStorage remembers whether each commit wrote pi.live as a base or a delta.
type recordingStorage struct {
	*storage.MemoryStorage
	mu      sync.Mutex
	liveId  *durable.DocumentId
	written map[durable.Seq]string
}

func (store *recordingStorage) setLive(id durable.DocumentId) {
	store.mu.Lock()
	store.liveId = &id
	store.mu.Unlock()
}

func (store *recordingStorage) Commit(ctx context.Context, writes []durable.StorageWrite) (durable.Seq, error) {
	store.mu.Lock()
	liveId := store.liveId
	store.mu.Unlock()
	kind := ""
	for _, write := range writes {
		if change, ok := write.(durable.DocumentChangeWrite); ok && liveId != nil && change.Id == *liveId {
			kind = string(change.Content.Kind)
		}
	}
	seq, err := store.MemoryStorage.Commit(ctx, writes)
	if err == nil && kind != "" {
		store.mu.Lock()
		store.written[seq] = kind
		store.mu.Unlock()
	}
	return seq, err
}

func itoa(value int) string { return strconv.Itoa(value) }

func jsonValue(text string) (durable.JsonValue, error) {
	var value any
	err := json.Unmarshal([]byte(text), &value)
	return value, err
}
