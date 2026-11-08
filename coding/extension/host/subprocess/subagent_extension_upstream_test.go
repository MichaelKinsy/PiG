//go:build !pig_strip_node_extensions

package subprocess

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Ports packages/coding-agent/test/subagent-extension.test.ts at Pi c23b9ec1 (Pi aba42ae6b #8250, 5c808e17b): the subagent example extension
// run in a Node extension process, with Pi's fixture child (testdata/subagent-child.mjs) standing in for the pi CLI.
//
// The example re-executes process.argv[1] as Pi. In a PiG Node extension process argv[1] is runtime-node/harness/cli.mjs, which runs the binary
// the Host names (harnessBinary); here that binary is a shell script that starts the fixture, as Pi's test points process.argv[1] at it.
//
// Differences from Pi's test, none of them a change of input or expectation:
//   - Pi's `vi.useFakeTimers()` calls only guard a test against timers it never waits for; a Go test runs on the real clock.
//   - Pi's "does not pass a Windows Bun virtual entry path to the child" sets process.argv[1] to "B:/~BUN/root/pi.exe" in the test's own process.
//     A PiG extension process has the harness entry as argv[1] (process-identity.mjs) and never a Bun virtual path, so that case has no PiG
//     input; TestSubagentExampleStartsTheChildThroughTheHarnessEntry covers what it asserts, that the child gets the fixture and not the entry.
//   - Pi's two "runtime integration" cases read the tool result an AgentSession records; these tests read the result the tool returns,
//     whose IsError is what the session records.

// subagentExampleSHA256 is the sha256 of the example's index.ts at Pi c23b9ec1.
const subagentExampleSHA256 = "3b738fd10c1aaadfb5f96d0c683481faad916cd88544677efd7280eb38f425fe"

const (
	subagentMaxBytes = 50 * 1024 // DEFAULT_MAX_BYTES
	subagentMaxLines = 2000      // DEFAULT_MAX_LINES
)

type subagentChild struct {
	Status            string          `json:"status"`
	Messages          []subagentMsg   `json:"messages"`
	Stderr            string          `json:"stderr"`
	ErrorMessage      string          `json:"errorMessage"`
	TerminationSignal string          `json:"terminationSignal"`
	Model             string          `json:"model"`
	Extra             json.RawMessage `json:"-"`
}

type subagentMsg struct {
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

type subagentConfirmUI struct {
	*mockUIContext
	mu    sync.Mutex
	calls [][2]string
}

func (ui *subagentConfirmUI) Confirm(_ context.Context, title, message string, _ extension.ExtensionUIDialogOptions) (bool, error) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	ui.calls = append(ui.calls, [2]string{title, message})
	return true, nil
}

func (ui *subagentConfirmUI) confirmations() [][2]string {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	return append([][2]string(nil), ui.calls...)
}

type subagentRig struct {
	t      *testing.T
	dir    string
	host   *Host
	tool   extension.ToolDefinition
	loaded *extension.Extension
	ui     *subagentConfirmUI
	cancel context.CancelFunc
	ctx    context.Context
}

// newSubagentRig loads the example in a Host whose session cwd is cwd (the test's temp dir when empty). interactive gives the tool context a UI whose
// confirm answers true; trusted is what ctx.isProjectTrusted() reports.
func newSubagentRig(t *testing.T, cwd string, interactive, trusted bool) *subagentRig {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required: %v", err)
	}
	shortSockDir(t)
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	dir := t.TempDir()
	if cwd == "" {
		cwd = dir
	}
	source, err := filepath.Abs("runtime-node/shims/pi-dist/pi-coding-agent/examples/extensions/subagent/index.ts")
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(body); hex.EncodeToString(sum[:]) != subagentExampleSHA256 {
		t.Fatalf("the vendored subagent example is %x, not Pi c23b9ec1's %s", sum, subagentExampleSHA256)
	}
	fixture, err := filepath.Abs("testdata/subagent-child.mjs")
	if err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(dir, "pi-fixture.sh")
	// The pi the example starts is the fixture. The harness entry (argv[1] of the extension) sits between them, and killing it with SIGKILL leaves its
	// child running, which Pi's direct child would not survive; for the one scenario that waits for that kill the launcher therefore ends its fixture
	// when it is itself killed, and forwards SIGTERM as the harness entry does. Every other scenario execs the fixture, so its exit status reaches the entry.
	write(t, launcher, "#!/bin/sh\ncase \"$*\" in\n*'Task: ignore-sigterm:'*) ;;\n*) exec "+shellQuote(node)+" "+shellQuote(fixture)+" \"$@\" ;;\nesac\n"+
		shellQuote(node)+" "+shellQuote(fixture)+" \"$@\" &\nchild=$!\ntrap 'kill -TERM $child 2>/dev/null' TERM INT HUP\n"+
		"( while kill -0 $$ 2>/dev/null; do sleep 0.05; done; kill -KILL $child 2>/dev/null ) &\n"+
		"while kill -0 $child 2>/dev/null; do wait $child; done\n")
	if err := os.Chmod(launcher, 0o755); err != nil {
		t.Fatal(err)
	}
	host := NewHost(cwd)
	host.harnessBinary = launcher
	t.Cleanup(func() { host.Shutdown("test done") })
	rig := &subagentRig{t: t, dir: dir, host: host}
	if interactive {
		rig.ui = &subagentConfirmUI{mockUIContext: &mockUIContext{}}
		bridge := NewUIBridge(func() {})
		bridge.SetUIContext(rig.ui)
		bridge.SetHostAction("isProjectTrusted", func() bool { return trusted })
		host.SetUIBridge(bridge)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	t.Cleanup(cancel)
	loaded, err := host.Load(ctx, ExtConfig{Name: "subagent", Source: source, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	loadedTool, ok := loaded.Tools["subagent"]
	if !ok {
		t.Fatal("the subagent extension did not register its tool")
	}
	rig.tool = loadedTool.Definition
	rig.loaded = loaded
	rig.ctx, rig.cancel = ctx, cancel
	rig.createProjectAgent(cwd, "worker", "")
	return rig
}

func (r *subagentRig) createProjectAgent(cwd, name, model string) {
	r.t.Helper()
	front := "---\nname: " + name + "\ndescription: " + name + " test agent\n"
	if model != "" {
		front += "model: " + model + "\n"
	}
	write(r.t, filepath.Join(cwd, ".pig", "agents", name+".md"), front+"---\n")
}

func (r *subagentRig) execute(ctx context.Context, params map[string]any, onUpdate agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	r.t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		r.t.Fatal(err)
	}
	return r.tool.Execute(ctx, "test", raw, onUpdate)
}

func (r *subagentRig) mustExecute(params map[string]any) agent.AgentToolResult {
	r.t.Helper()
	result, err := r.execute(r.ctx, params, nil)
	if err != nil {
		r.t.Fatal(err)
	}
	return result
}

func singleTask(task string, overrides map[string]any) map[string]any {
	params := map[string]any{"agent": "worker", "task": task, "agentScope": "project", "confirmProjectAgents": false}
	for k, v := range overrides {
		params[k] = v
	}
	return params
}

func subagentResults(t *testing.T, result agent.AgentToolResult) []subagentChild {
	t.Helper()
	raw, err := json.Marshal(result.Details)
	if err != nil {
		t.Fatal(err)
	}
	var details struct {
		Results []subagentChild `json:"results"`
	}
	if err := json.Unmarshal(raw, &details); err != nil {
		t.Fatalf("details %s: %v", raw, err)
	}
	return details.Results
}

func subagentText(result agent.AgentToolResult) string {
	var parts []string
	for _, block := range result.Content {
		if text, ok := block.(ai.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func finalAssistantText(child subagentChild) string {
	for i := len(child.Messages) - 1; i >= 0; i-- {
		if child.Messages[i].Role != "assistant" {
			continue
		}
		var parts []string
		for _, part := range child.Messages[i].Content {
			if part.Type == "text" {
				parts = append(parts, part.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func (r *subagentRig) renderText(result agent.AgentToolResult, expanded bool, want ...string) string {
	r.t.Helper()
	if r.tool.RenderResult == nil {
		r.t.Fatal("the subagent tool has no result renderer")
	}
	card := fmt.Sprintf("subagent-%d", time.Now().UnixNano())
	proxy := r.tool.RenderResult(result, extension.ToolRenderResultOptions{Expanded: expanded}, nil, extension.ToolRenderContext{
		Args: json.RawMessage(`{}`), ToolCallID: "test", Card: card, Cwd: "/tmp", ExecutionStarted: true, ArgsComplete: true,
		Expanded: expanded, Invalidate: func() {},
	}).(*toolRenderProxy)
	deadline := time.Now().Add(10 * time.Second)
	var text string
	for {
		text = strings.Join(proxy.Render(160), "\n")
		missing := false
		for _, w := range want {
			if !strings.Contains(text, w) {
				missing = true
			}
		}
		if !missing || time.Now().After(deadline) {
			return text
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitUntil(t *testing.T, what string, within time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s", within, what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func readLines(path string) []string {
	body, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(body)) == "" {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(body)), "\n")
}

func processAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func selectedModel(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var argv []string
	if err := json.Unmarshal(body, &argv); err != nil {
		t.Fatal(err)
	}
	for i, arg := range argv {
		if arg == "--model" && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

func TestSubagentExampleChildLifecycle(t *testing.T) {
	t.Run("keeps a child running across a tool-calling message until process close", func(t *testing.T) {
		rig := newSubagentRig(t, "", false, false)
		var mu sync.Mutex
		var updates []agent.AgentToolResult
		first := make(chan struct{})
		var once sync.Once
		done := make(chan agent.AgentToolResult, 1)
		go func() {
			result, err := rig.execute(rig.ctx, singleTask("tool-then-final", nil), func(update agent.AgentToolResult) {
				mu.Lock()
				updates = append(updates, update)
				mu.Unlock()
				once.Do(func() { close(first) })
			})
			if err != nil {
				t.Error(err)
			}
			done <- result
		}()
		select {
		case <-first:
		case <-time.After(30 * time.Second):
			t.Fatal("no update arrived")
		}
		mu.Lock()
		streaming := updates[0]
		mu.Unlock()
		if got := subagentResults(t, streaming)[0].Status; got != "running" {
			t.Fatalf("streaming status = %q, want running", got)
		}
		if render := rig.renderText(streaming, false, "running"); !strings.Contains(render, "running") {
			t.Fatalf("streaming render = %q", render)
		}
		result := <-done
		if got := subagentResults(t, result)[0].Status; got != "completed" {
			t.Fatalf("final status = %q, want completed", got)
		}
	})

	t.Run("distinguishes pending tasks from four active workers", func(t *testing.T) {
		rig := newSubagentRig(t, "", false, false)
		release := filepath.Join(rig.dir, "release-workers")
		tasks := make([]map[string]any, 5)
		for i := range tasks {
			tasks[i] = map[string]any{"agent": "worker", "task": "tool-wait:" + release}
		}
		var mu sync.Mutex
		var updates []agent.AgentToolResult
		done := make(chan error, 1)
		go func() {
			_, err := rig.execute(rig.ctx, map[string]any{"tasks": tasks, "agentScope": "project", "confirmProjectAgents": false}, func(update agent.AgentToolResult) {
				mu.Lock()
				updates = append(updates, update)
				mu.Unlock()
			})
			done <- err
		}()
		waitUntil(t, "four updates", 30*time.Second, func() bool { mu.Lock(); defer mu.Unlock(); return len(updates) >= 4 })
		mu.Lock()
		last := updates[len(updates)-1]
		mu.Unlock()
		running, pending := 0, 0
		for _, child := range subagentResults(t, last) {
			switch child.Status {
			case "running":
				running++
			case "pending":
				pending++
			}
		}
		write(t, release, "release")
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if running != 4 || pending != 1 {
			t.Fatalf("running = %d, pending = %d, want 4 and 1", running, pending)
		}
	})

	t.Run("treats signal termination as a failed child with a diagnostic", func(t *testing.T) {
		rig := newSubagentRig(t, "", false, false)
		result := rig.mustExecute(singleTask("signal", nil))
		child := subagentResults(t, result)[0]
		if child.Status != "failed" || child.TerminationSignal != "SIGTERM" || !strings.Contains(subagentText(result), "SIGTERM") {
			t.Fatalf("child = %+v, text = %q", child, subagentText(result))
		}
	})

	t.Run("preserves spawn errors for a nonexistent working directory", func(t *testing.T) {
		rig := newSubagentRig(t, "", false, false)
		result := rig.mustExecute(singleTask("spawn-error", map[string]any{"cwd": filepath.Join(rig.dir, "missing-working-directory")}))
		text := strings.ToLower(subagentText(result))
		if got := subagentResults(t, result)[0].Status; got != "failed" || !(strings.Contains(text, "enoent") || strings.Contains(text, "no such file or directory")) {
			t.Fatalf("status = %q, text = %q", got, text)
		}
	})

	t.Run("escalates SIGTERM to SIGKILL based on close state and preserves partial details", func(t *testing.T) {
		rig := newSubagentRig(t, "", false, false)
		pidFile := filepath.Join(rig.dir, "ignores-sigterm.pid")
		ctx, abort := context.WithCancel(rig.ctx)
		type settled struct {
			result agent.AgentToolResult
			err    error
		}
		done := make(chan settled, 1)
		go func() {
			result, err := rig.execute(ctx, singleTask("ignore-sigterm:"+pidFile, nil), nil)
			done <- settled{result, err}
		}()
		waitUntil(t, "the child's pid file", 30*time.Second, func() bool { return len(readLines(pidFile)) == 1 })
		pid, err := strconv.Atoi(readLines(pidFile)[0])
		if err != nil {
			t.Fatal(err)
		}
		abort()
		exited := false
		defer func() {
			if !exited {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}()
		var got settled
		select {
		case got = <-done:
		case <-time.After(60 * time.Second):
			t.Fatal("the aborted call never settled")
		}
		waitUntil(t, "the child to exit after the escalation", 10*time.Second, func() bool { return !processAlive(pid) })
		exited = true
		if got.err != nil {
			t.Fatalf("the aborted call rejected: %v", got.err)
		}
		child := subagentResults(t, got.result)[0]
		if child.Status != "aborted" || len(child.Messages) != 1 {
			t.Fatalf("child = %+v, want aborted with its one partial message", child)
		}
	})

	// Pi's case passes a signal that is already aborted and expects five aborted results. A PiG call whose context has ended is never sent to the
	// extension: the host returns the context's error before the example runs, so no child can start either. This asserts both.
	t.Run("starts no work when the signal is already aborted", func(t *testing.T) {
		rig := newSubagentRig(t, "", false, false)
		ctx, abort := context.WithCancel(rig.ctx)
		abort()
		starts := filepath.Join(rig.dir, "never-started.txt")
		tasks := make([]map[string]any, 5)
		for i := range tasks {
			tasks[i] = map[string]any{"agent": "worker", "task": "hold:" + starts}
		}
		_, err := rig.execute(ctx, map[string]any{"tasks": tasks, "agentScope": "project", "confirmProjectAgents": false}, nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want the context's cancellation", err)
		}
		time.Sleep(500 * time.Millisecond)
		if started := readLines(starts); len(started) != 0 {
			t.Fatalf("%d children started for an aborted call", len(started))
		}
	})

	t.Run("does not start queued work after aborting active workers", func(t *testing.T) {
		rig := newSubagentRig(t, "", false, false)
		starts := filepath.Join(rig.dir, "started-children.txt")
		ctx, abort := context.WithCancel(rig.ctx)
		tasks := make([]map[string]any, 5)
		for i := range tasks {
			tasks[i] = map[string]any{"agent": "worker", "task": "hold:" + starts}
		}
		type settled struct {
			result agent.AgentToolResult
			err    error
		}
		done := make(chan settled, 1)
		go func() {
			result, err := rig.execute(ctx, map[string]any{"tasks": tasks, "agentScope": "project", "confirmProjectAgents": false}, nil)
			done <- settled{result, err}
		}()
		waitUntil(t, "four started children", 30*time.Second, func() bool { return len(readLines(starts)) == 4 })
		abort()
		got := <-done
		if got.err != nil {
			t.Fatal(got.err)
		}
		if started := readLines(starts); len(started) != 4 {
			t.Fatalf("%d children started, want 4", len(started))
		}
		children := subagentResults(t, got.result)
		if len(children) != 5 {
			t.Fatalf("%d results, want 5", len(children))
		}
		for _, child := range children {
			if child.Status != "aborted" {
				t.Fatalf("child = %+v, want aborted", child)
			}
		}
	})
}

func TestSubagentExampleRuntimeIntegration(t *testing.T) {
	t.Run("marks a failed subagent invocation as an error tool result while preserving details", func(t *testing.T) {
		rig := newSubagentRig(t, "", false, false)
		result := rig.mustExecute(singleTask("error", nil))
		if !rig.toolResultMarksError(result) {
			t.Fatalf("the tool_result hook did not mark the result an error: %+v", result)
		}
		if got := subagentResults(t, result)[0].ErrorMessage; got != "provider evidence" {
			t.Fatalf("errorMessage = %q, want provider evidence", got)
		}
	})

	t.Run("marks invalid mode combinations as error tool results", func(t *testing.T) {
		rig := newSubagentRig(t, "", false, false)
		result, err := rig.execute(rig.ctx, map[string]any{
			"agent": "worker", "task": "single", "tasks": []map[string]any{{"agent": "worker", "task": "parallel"}},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !rig.toolResultMarksError(result) {
			t.Fatalf("an invalid mode combination was not marked an error: %+v", result)
		}
	})
}

// toolResultMarksError is what the session records as the tool result's isError: the example reports failure through its tool_result hook
// (`return { isError: true }` for details.isError), which the runner emits after the call.
func (r *subagentRig) toolResultMarksError(result agent.AgentToolResult) bool {
	r.t.Helper()
	runner := inproc.NewRunner([]extension.Extension{*r.loaded}, r.dir)
	patched, err := runner.EmitToolResult(r.ctx, extension.CustomToolResultEvent{
		ToolResultEventBase: extension.ToolResultEventBase{Type: "tool_result", ToolCallID: "test", Input: map[string]any{}, Content: nil},
		ToolName:            "subagent",
		Details:             result.Details,
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return result.IsError || (patched != nil && patched.IsError != nil && *patched.IsError)
}

// Covers what Pi's `does not pass a Windows Bun virtual entry path to the child` asserts: the child the example starts is the fixture, not the
// entry that reported itself as argv[1].
func TestSubagentExampleStartsTheChildThroughTheHarnessEntry(t *testing.T) {
	rig := newSubagentRig(t, "", false, false)
	argvFile := filepath.Join(rig.dir, "argv.json")
	rig.mustExecute(singleTask("record-argv:"+argvFile, nil))
	body, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatal(err)
	}
	var argv []string
	if err := json.Unmarshal(body, &argv); err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("testdata/subagent-child.mjs")
	if len(argv) < 2 || argv[1] != fixture {
		t.Fatalf("child argv = %q, want the fixture at [1]", argv)
	}
	for _, arg := range argv[2:] {
		if strings.Contains(arg, "harness/cli.mjs") {
			t.Fatalf("the harness entry reached the child: %q", argv)
		}
	}
}

func TestSubagentExampleTaskCwdAgentDiscovery(t *testing.T) {
	t.Run("resolves a relative task cwd against the session cwd for discovery and execution", func(t *testing.T) {
		root := t.TempDir()
		sessionRoot := filepath.Join(root, "session-project")
		startupChild := filepath.Join(root, "startup-project", "child")
		sessionChild := filepath.Join(sessionRoot, "child")
		for _, dir := range []string{startupChild, sessionChild} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		rig := newSubagentRig(t, sessionRoot, false, false)
		rig.createProjectAgent(startupChild, "worker", "startup-model")
		rig.createProjectAgent(sessionChild, "worker", "session-model")
		invocation := filepath.Join(rig.dir, "relative-cwd-invocation.json")
		rig.mustExecute(singleTask("record-invocation:"+invocation, map[string]any{"cwd": "child"}))
		body, err := os.ReadFile(invocation)
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Argv []string `json:"argv"`
			Cwd  string   `json:"cwd"`
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		want, err := filepath.EvalSymlinks(sessionChild)
		if err != nil {
			t.Fatal(err)
		}
		if got.Cwd != want {
			t.Fatalf("child cwd = %q, want %q", got.Cwd, want)
		}
		if model := selectedModelFrom(got.Argv); model != "session-model" {
			t.Fatalf("child model = %q, want session-model", model)
		}
	})

	t.Run("uses and confirms the project agent from the child task cwd", func(t *testing.T) {
		rig := newSubagentRig(t, "", true, false)
		childCwd := filepath.Join(rig.dir, "confirmed-project")
		if err := os.MkdirAll(childCwd, 0o755); err != nil {
			t.Fatal(err)
		}
		rig.createProjectAgent(rig.dir, "worker", "root-model")
		rig.createProjectAgent(childCwd, "worker", "child-model")
		argvFile := filepath.Join(rig.dir, "confirmed-child-argv.json")
		rig.mustExecute(singleTask("record-argv:"+argvFile, map[string]any{"cwd": childCwd, "confirmProjectAgents": true}))
		if got := selectedModel(t, argvFile); got != "child-model" {
			t.Errorf("child model = %q, want child-model", got)
		}
		calls := rig.ui.confirmations()
		if len(calls) == 0 || calls[0][0] != "Run project-local agents?" || !strings.Contains(calls[0][1], filepath.Join(childCwd, ".pig", "agents")) {
			t.Fatalf("confirmations = %q", calls)
		}
	})

	// Regression coverage for #8261 when a trusted session delegates into another project.
	t.Run("confirms an external task cwd agent even when the session trust context reports trusted", func(t *testing.T) {
		root := t.TempDir()
		sessionCwd := filepath.Join(root, "projects", "session-project")
		externalCwd := filepath.Join(root, "projects", "external-project")
		for _, dir := range []string{sessionCwd, externalCwd} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		rig := newSubagentRig(t, sessionCwd, true, true)
		rig.createProjectAgent(sessionCwd, "worker", "session-model")
		rig.createProjectAgent(externalCwd, "worker", "external-model")
		argvFile := filepath.Join(rig.dir, "external-child-argv.json")
		rig.mustExecute(singleTask("record-argv:"+argvFile, map[string]any{"cwd": externalCwd, "confirmProjectAgents": true}))
		if got := selectedModel(t, argvFile); got != "external-model" {
			t.Errorf("child model = %q, want external-model", got)
		}
		calls := rig.ui.confirmations()
		if len(calls) != 1 || calls[0][0] != "Run project-local agents?" || !strings.Contains(calls[0][1], filepath.Join(externalCwd, ".pig", "agents")) {
			t.Fatalf("confirmations = %q, want one for the external agents", calls)
		}
		if strings.Contains(calls[0][1], filepath.Join(sessionCwd, ".pig", "agents")) {
			t.Fatalf("the confirmation names the session agents: %q", calls[0][1])
		}
	})

	t.Run("resolves and confirms each project-agent source for parallel task cwd values", func(t *testing.T) {
		rig := newSubagentRig(t, "", true, false)
		projectA := filepath.Join(rig.dir, "parallel-a")
		projectB := filepath.Join(rig.dir, "parallel-b")
		for _, dir := range []string{projectA, projectB} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		rig.createProjectAgent(projectA, "worker", "model-a")
		rig.createProjectAgent(projectB, "worker", "model-b")
		argvA1 := filepath.Join(rig.dir, "parallel-a1-argv.json")
		argvA2 := filepath.Join(rig.dir, "parallel-a2-argv.json")
		argvB := filepath.Join(rig.dir, "parallel-b-argv.json")
		rig.mustExecute(map[string]any{
			"tasks": []map[string]any{
				{"agent": "worker", "task": "record-argv:" + argvA1, "cwd": projectA},
				{"agent": "worker", "task": "record-argv:" + argvA2, "cwd": projectA},
				{"agent": "worker", "task": "record-argv:" + argvB, "cwd": projectB},
			},
			"agentScope": "project", "confirmProjectAgents": true,
		})
		for path, want := range map[string]string{argvA1: "model-a", argvA2: "model-a", argvB: "model-b"} {
			if got := selectedModel(t, path); got != want {
				t.Errorf("%s: model = %q, want %q", filepath.Base(path), got, want)
			}
		}
		var text []string
		for _, call := range rig.ui.confirmations() {
			text = append(text, call[1])
		}
		all := strings.Join(text, "\n")
		for _, source := range []string{filepath.Join(projectA, ".pig", "agents"), filepath.Join(projectB, ".pig", "agents")} {
			if n := strings.Count(all, source); n != 1 {
				t.Errorf("%s is named %d times in the confirmations, want once: %q", source, n, all)
			}
		}
	})
}

func selectedModelFrom(argv []string) string {
	for i, arg := range argv {
		if arg == "--model" && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

func TestSubagentExampleModelFacingOutputAndRendering(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params func() map[string]any
	}{
		{"single", func() map[string]any { return singleTask("large-single", nil) }},
		{"chain", func() map[string]any {
			return map[string]any{"chain": []map[string]any{{"agent": "worker", "task": "large-chain"}}, "agentScope": "project", "confirmProjectAgents": false}
		}},
	} {
		t.Run("limits "+tc.name+" output while retaining complete details", func(t *testing.T) {
			rig := newSubagentRig(t, "", false, false)
			result := rig.mustExecute(tc.params())
			text := subagentText(result)
			if len(text) > subagentMaxBytes || len(strings.Split(text, "\n")) > subagentMaxLines {
				t.Fatalf("model-facing text is %d bytes, %d lines", len(text), len(strings.Split(text, "\n")))
			}
			if !strings.Contains(strings.ToLower(text), "truncat") {
				t.Fatalf("text does not say it was truncated: %.200q", text)
			}
			full := finalAssistantText(subagentResults(t, result)[0])
			if len(full) <= subagentMaxBytes || len(strings.Split(full, "\n")) <= subagentMaxLines {
				t.Fatalf("details hold %d bytes, %d lines, want more than the limits", len(full), len(strings.Split(full, "\n")))
			}
		})
	}

	failedChain := func(rig *subagentRig, task string) agent.AgentToolResult {
		return rig.mustExecute(map[string]any{"chain": []map[string]any{{"agent": "worker", "task": task}}, "agentScope": "project", "confirmProjectAgents": false})
	}

	t.Run("shows failure diagnostics in collapsed results", func(t *testing.T) {
		rig := newSubagentRig(t, "", false, false)
		result := failedChain(rig, "signal")
		if render := rig.renderText(result, false, "SIGTERM"); !strings.Contains(render, "SIGTERM") {
			t.Fatalf("render = %q", render)
		}
	})

	t.Run("preserves provider errors, stderr, and assistant output as distinct evidence", func(t *testing.T) {
		rig := newSubagentRig(t, "", false, false)
		result := failedChain(rig, "error")
		child := subagentResults(t, result)[0]
		if child.ErrorMessage != "provider evidence" {
			t.Errorf("errorMessage = %q", child.ErrorMessage)
		}
		if !strings.Contains(child.Stderr, "stderr evidence") {
			t.Errorf("stderr = %q", child.Stderr)
		}
		if !strings.Contains(finalAssistantText(child), "assistant evidence") {
			t.Errorf("assistant text = %q", finalAssistantText(child))
		}
		expanded := rig.renderText(result, true, "provider evidence", "stderr evidence", "assistant evidence")
		for _, want := range []string{"provider evidence", "stderr evidence", "assistant evidence"} {
			if !strings.Contains(expanded, want) {
				t.Errorf("expanded render lacks %q: %q", want, expanded)
			}
		}
	})

	t.Run("renders zero-exit model errors as failed chain steps", func(t *testing.T) {
		rig := newSubagentRig(t, "", false, false)
		result := failedChain(rig, "model-error")
		if render := rig.renderText(result, false, "0/1 steps"); !strings.Contains(render, "0/1 steps") {
			t.Fatalf("render = %q", render)
		}
	})
}
