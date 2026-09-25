package pico3

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// Source: packages/agent/test/harness/pico3/view.test.ts, all five cases.
// Bounded observation polling replaces source sleeps; the asserted states and
// eventual transcript/background contracts remain the same.
func viewEventually(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatal("view state did not arrive")
		}
		time.Sleep(time.Millisecond)
	}
}
func viewTranscript(v JsonObject) []string {
	out := []string{}
	for _, e := range arr(v, "entries") {
		out = append(out, strings.TrimPrefix(str(asObject(e), "kind"), "pi."))
	}
	return out
}
func viewStreaming(v JsonObject) string {
	content := arr(asObject(asObject(v["turn"])["message"]), "content")
	if len(content) == 0 {
		return ""
	}
	return str(asObject(content[0]), "text")
}
func viewTools(v JsonObject) []any { return arr(asObject(v["turn"]), "tools") }
func viewBackground(v JsonObject) JsonObject {
	out := JsonObject{}
	for id, raw := range asObject(v["tasks"]) {
		if asObject(raw)["background"] == true {
			out[id] = raw
		}
	}
	return out
}
func assertViewTurnEnded(t *testing.T, v JsonObject) {
	t.Helper()
	equal(t, len(viewTools(v)), 0, "no running tools")
	equal(t, viewStreaming(v), "", "no streaming message")
	_, working := v["turn"]
	equal(t, working, false, "turn ended")
}
func TestViewStreamingToToolsAtomicAndCleared(t *testing.T) {
	gate, toolGate := &testGate{}, &testGate{}
	tool := &ToolDeclaration{Name: "x", Parameters: JsonObject{"type": "object"}, Execute: func(ctx context.Context, _ JsonValue, api *ToolApi) (ToolResult, error) {
		if err := api.Stream([]byte("partial ")); err != nil {
			return ToolResult{}, err
		}
		if err := toolGate.Wait(ctx); err != nil {
			return ToolResult{}, err
		}
		return ToolResult{}, api.Stream([]byte("done"))
	}}
	env := openEnv(t, openOptions{tools: []*ToolDeclaration{tool}, models: newFake(fakeOptions{respond: echoScript, gate: gate, tokenDelayMs: 2})})
	collector := collectWatch(t, env.root)
	input := env.send(env.root, "tool:x")
	gate.Arrivals(t, 1)
	gate.Open()
	toolGate.Arrivals(t, 1)
	viewEventually(t, func() bool {
		v := foldCollected(t, collector)
		tools := viewTools(v)
		return len(tools) == 1 && str(asObject(tools[0]), "output") == "partial "
	})
	mid := foldCollected(t, collector)
	equal(t, viewTranscript(mid), []string{"user", "system", "assistant"}, "assistant durable at tool start")
	slot := asObject(viewTools(mid)[0])
	equal(t, str(slot, "name"), "x", "running tool")
	equal(t, slot["continuedBy"], nil, "no background link")
	equal(t, viewStreaming(mid), "", "stream cleared")
	_, working := mid["turn"]
	equal(t, working, true, "working")
	// Inspect every envelope boundary, not just the eventual tool snapshot.
	folded := collector.view
	for _, e := range collector.Envelopes() {
		folded = must(ApplyEnvelope(folded, e))
		if len(viewTranscript(folded)) == 3 {
			equal(t, len(viewTools(folded)), 1, "assistant and tools published atomically")
		}
	}
	toolGate.Open()
	env.wait(input)
	env.idle()
	end := foldCollected(t, collector)
	equal(t, viewTranscript(end), []string{"user", "system", "assistant", "tool_result", "assistant"}, "settled transcript")
	assertViewTurnEnded(t, end)
}
func TestViewStreamingTextPrecedesEntryAndFoldEqualsSnapshot(t *testing.T) {
	gate := &testGate{}
	words := []string{}
	for i := range 40 {
		words = append(words, fmt.Sprintf("w%d", i))
	}
	answer := strings.Join(words, " ")
	env := openEnv(t, openOptions{models: newFake(fakeOptions{respond: func([]JsonObject, int) fakeResponse { return fakeResponse{text: &answer} }, gate: gate, tokenDelayMs: 10})})
	collector := collectWatch(t, env.root)
	input := env.send(env.root, "hi")
	gate.Arrivals(t, 1)
	gate.Open()
	var mid JsonObject
	viewEventually(t, func() bool {
		mid = foldCollected(t, collector)
		text := viewStreaming(mid)
		return len(text) > 0 && len(text) < len(answer)
	})
	equal(t, viewTranscript(mid), []string{"user", "system"}, "partial precedes durable assistant")
	env.wait(input)
	env.idle()
	end := foldCollected(t, collector)
	equal(t, viewStreaming(end), "", "final stream cleared")
	equal(t, viewTranscript(end), []string{"user", "system", "assistant"}, "final transcript")
	fresh := must(env.root.Watch(bg))
	defer fresh.Stop()
	equal(t, end, fresh.View, "fold equals fresh snapshot")
}
func viewJobTool(name string, budget time.Duration) *ToolDeclaration {
	return &ToolDeclaration{Name: name, Parameters: JsonObject{"type": "object"}, Execute: func(ctx context.Context, _ JsonValue, api *ToolApi) (ToolResult, error) {
		job, err := api.Task(ctx, Kinds.Job, JsonObject{"command": "sleep", "args": []any{}, "cwd": "/", "notify": false, "rerun": false}, TaskOptions{Background: true})
		if err != nil {
			return ToolResult{}, err
		}
		if err := api.Progress(ctx, func(p *ToolProgress) {
			p.ContinuedBy = &job.Id
			if budget == 0 {
				p.Progress = new("backgrounded")
			}
		}); err != nil {
			return ToolResult{}, err
		}
		result := ToolResult{Details: JsonObject{"jobId": job.Id}}
		if budget == 0 {
			result.Content = []ai.ToolResultMessageContent{ai.TextContent{Text: fmt.Sprintf("continues in background as task %d", job.Id)}}
			return result, nil
		}
		wait, cancel := context.WithTimeout(ctx, budget)
		defer cancel()
		done, err := api.WaitForTask(wait, job)
		if err == nil {
			if done.Outcome == nil || done.Outcome.Status != "completed" {
				return ToolResult{IsError: true}, nil
			}
			out := asObject(done.Outcome.Result)
			result.Content = []ai.ToolResultMessageContent{ai.TextContent{Text: str(out, "stdout")}}
			result.IsError = numberOr(out["exitCode"], 0) != 0
			result.Details = JsonObject{"jobId": job.Id, "finished": true}
			return result, nil
		}
		if ctx.Err() != nil {
			return ToolResult{}, err
		}
		slot, err := api.Slot(ctx, job)
		if err != nil {
			return ToolResult{}, err
		}
		result.Content = []ai.ToolResultMessageContent{ai.TextContent{Text: fmt.Sprintf("%s\n\n[still running in the background as task %d]", str(slot, "stdout"), job.Id)}}
		result.Details = JsonObject{"jobId": job.Id, "finished": false}
		return result, nil
	}}
}
func viewRunningProcess(t *testing.T, host *fakeProcessHost, output string) string {
	t.Helper()
	key := ""
	viewEventually(t, func() bool {
		host.mu.Lock()
		defer host.mu.Unlock()
		for k := range host.procs {
			key = k
			host.procs[k] = ProcessStatus{Status: "running", Stdout: output}
			return true
		}
		return false
	})
	return key
}
func viewToolResult(t *testing.T, env *testEnv) Entry {
	t.Helper()
	for _, entry := range env.entries() {
		if entry.Kind == "pi.tool_result" {
			return entry
		}
	}
	t.Fatal("missing tool result")
	return Entry{}
}
func TestViewBackgroundJobOutlivesTurnAndStreams(t *testing.T) {
	host := newFakeHost()
	env := openEnv(t, openOptions{tools: []*ToolDeclaration{viewJobTool("bg", 0)}, processHost: host, models: newFake(fakeOptions{respond: echoScript})})
	collector := collectWatch(t, env.root)
	env.wait(env.send(env.root, "tool:bg"))
	env.idle()
	key := viewRunningProcess(t, host, "job says hi")
	mid := foldCollected(t, collector)
	assertViewTurnEnded(t, mid)
	background := viewBackground(mid)
	equal(t, len(background), 1, "background job remains")
	entry := viewToolResult(t, env)
	jobId, _ := asID(asObject(asObject(entry.Data)["details"])["jobId"])
	taskKey := fmt.Sprint(jobId)
	equal(t, str(asObject(background[taskKey]), "kind"), "pi.job", "durable result links background job")
	viewEventually(t, func() bool {
		return str(asObject(asObject(viewBackground(foldCollected(t, collector))[taskKey])["status"]), "stdout") == "job says hi"
	})
	host.exit(key, 0)
	env.untilTerminal(jobId)
	viewEventually(t, func() bool { return len(asObject(foldCollected(t, collector)["tasks"])) == 0 })
}
func TestViewJobWithinBudgetReturnsOutput(t *testing.T) {
	host := newFakeHost()
	env := openEnv(t, openOptions{tools: []*ToolDeclaration{viewJobTool("run", 2*time.Second)}, processHost: host, models: newFake(fakeOptions{respond: echoScript})})
	collector := collectWatch(t, env.root)
	input := env.send(env.root, "tool:run")
	key := viewRunningProcess(t, host, "halfway")
	var mid JsonObject
	var jobId Id
	viewEventually(t, func() bool {
		mid = foldCollected(t, collector)
		for id, raw := range viewBackground(mid) {
			if str(asObject(asObject(raw)["status"]), "stdout") == "halfway" {
				_, err := fmt.Sscan(id, &jobId)
				check(t, err)
				return len(viewTools(mid)) > 0
			}
		}
		return false
	})
	equal(t, numberOr(asObject(viewTools(mid)[0])["continuedBy"], 0), float64(jobId), "tool links live job")
	host.exit(key, 0)
	env.wait(input)
	env.idle()
	entry := viewToolResult(t, env)
	equal(t, contentOf(&entry), fmt.Sprintf("[{\"text\":\"out %s\",\"type\":\"text\"}]", key), "job output result")
	equal(t, asObject(asObject(entry.Data)["details"])["finished"], true, "finished result")
	equal(t, len(viewBackground(foldCollected(t, collector))), 0, "job no longer live")
}
func TestViewJobBudgetExpiryPreservesBackgroundTask(t *testing.T) {
	host := newFakeHost()
	env := openEnv(t, openOptions{tools: []*ToolDeclaration{viewJobTool("run", 300*time.Millisecond)}, processHost: host, models: newFake(fakeOptions{respond: echoScript})})
	collector := collectWatch(t, env.root)
	input := env.send(env.root, "tool:run")
	key := viewRunningProcess(t, host, "first 20 lines")
	env.wait(input)
	env.idle()
	entry := viewToolResult(t, env)
	details := asObject(asObject(entry.Data)["details"])
	equal(t, details["finished"], false, "budget expired")
	content := contentOf(&entry)
	if !strings.Contains(content, "first 20 lines") || !strings.Contains(content, "still running") {
		t.Fatalf("partial output and continuation: %s", content)
	}
	mid := foldCollected(t, collector)
	assertViewTurnEnded(t, mid)
	equal(t, len(viewBackground(mid)), 1, "background persists")
	jobId, _ := asID(details["jobId"])
	task := must(env.h.GetTask(bg, jobId))
	equal(t, task.Status, TaskRunning, "timeout did not abort job")
	host.exit(key, 0)
	env.untilTerminal(jobId)
}
