//go:build unix

package evals

// pi: packages/evals/src/harness.ts

// Tests for harness.ts runPiCodingAgent over a real pig process. Upstream has no test for the Session runner, so the
// cases derive from reading harness.ts:272-469 and run the complete path against a scripted OpenAI-compatible server:
// the model request, the bridge extension, the sandbox-free isolation, the session snapshot and the cleanup.
//
// The file is unix-only: the runner drives a pig process through a Unix-socket bridge and the sandbox cases need POSIX
// user APIs (sandbox_other.go), and the documentation evals run in Linux containers. A !unix-only helper here would make
// internal/evals a Windows-native test package whose test pig binary has no .exe.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/evals/bridge"
)

var (
	buildOnce             sync.Once
	pigBinary, evalBinary string
	buildErr              error
)

// builtBinaries builds pig and the bridge extension once per test binary.
func builtBinaries(t *testing.T) (string, string) {
	t.Helper()
	buildOnce.Do(func() {
		directory, err := os.MkdirTemp("", "pig-evals-binaries-")
		if err != nil {
			buildErr = err
			return
		}
		if err := os.Chmod(directory, 0o755); err != nil { // a sandboxed agent must be able to execute the binaries
			buildErr = err
			return
		}
		pigBinary, evalBinary = filepath.Join(directory, "pig"), filepath.Join(directory, "pig-eval-extension")
		for target, output := range map[string]string{"../../cmd/pig": pigBinary, "../../cmd/pig-eval-extension": evalBinary} {
			if out, err := exec.Command("go", "build", "-o", output, target).CombinedOutput(); err != nil {
				buildErr = fmt.Errorf("build %s: %w\n%s", target, err, out)
				return
			}
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return pigBinary, evalBinary
}

// scriptedReply is one model response: text, or a tool call when tool is set.
type scriptedReply struct {
	text, tool, arguments string
}

// scriptedModel serves OpenAI chat completions from a script and records every request body.
type scriptedModel struct {
	server   *httptest.Server
	mu       sync.Mutex
	replies  []scriptedReply
	requests []map[string]any
	// authorizations holds each request's Authorization header.
	authorizations []string
	status         int
	// delay holds each response back; a request the client cancels ends early.
	delay time.Duration
}

func newScriptedModel(t *testing.T, replies ...scriptedReply) *scriptedModel {
	t.Helper()
	model := &scriptedModel{replies: replies}
	model.server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		var decoded map[string]any
		_ = json.Unmarshal(body, &decoded)
		model.mu.Lock()
		model.requests = append(model.requests, decoded)
		model.authorizations = append(model.authorizations, request.Header.Get("Authorization"))
		reply := scriptedReply{text: "Paris"}
		if len(model.replies) > 0 {
			reply, model.replies = model.replies[0], model.replies[1:]
		}
		status, delay := model.status, model.delay
		model.mu.Unlock()
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-request.Context().Done():
				return
			}
		}
		if status != 0 {
			http.Error(response, `{"error":{"message":"scripted failure"}}`, status)
			return
		}
		response.Header().Set("content-type", "text/event-stream")
		write := func(value any) {
			encoded, _ := json.Marshal(value)
			_, _ = io.WriteString(response, "data: "+string(encoded)+"\n\n")
		}
		delta, finish := map[string]any{"role": "assistant", "content": reply.text}, "stop"
		if reply.tool != "" {
			delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "call_1", "type": "function",
				"function": map[string]any{"name": reply.tool, "arguments": reply.arguments}}}}
			finish = "tool_calls"
		}
		write(map[string]any{"id": "c1", "object": "chat.completion.chunk", "model": "fixture-chat", "choices": []any{map[string]any{"index": 0, "delta": delta}}})
		write(map[string]any{"id": "c1", "object": "chat.completion.chunk", "model": "fixture-chat", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}},
			"usage": map[string]any{"prompt_tokens": 7, "completion_tokens": 3, "total_tokens": 10}})
		_, _ = io.WriteString(response, "data: [DONE]\n\n")
	}))
	t.Cleanup(model.server.Close)
	return model
}

func (model *scriptedModel) systemPrompt(t *testing.T, request int) string {
	t.Helper()
	model.mu.Lock()
	defer model.mu.Unlock()
	messages, _ := model.requests[request]["messages"].([]any)
	for _, message := range messages {
		if record, _ := message.(map[string]any); record["role"] == "system" || record["role"] == "developer" {
			text, _ := record["content"].(string)
			return text
		}
	}
	t.Fatalf("request %d has no system message", request)
	return ""
}

// harnessFixture isolates the host agent directory and returns options that reach model.
func harnessFixture(t *testing.T, model *scriptedModel) PiCodingAgentHarnessOptions {
	t.Helper()
	pig, extension := builtBinaries(t)
	t.Setenv(agentDirEnvironment(), t.TempDir())
	t.Setenv("PI_PROVIDER", "")
	t.Setenv("PI_MODEL", "")
	modelsJSON, err := json.Marshal(map[string]any{"providers": map[string]any{"fixture": map[string]any{
		"baseUrl": model.server.URL + "/v1", "api": "openai-completions", "apiKey": "fixture-key",
		"models": []any{map[string]any{"id": "fixture-chat", "name": "Fixture Chat", "input": []string{"text"}, "contextWindow": 8192, "maxTokens": 1024}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return PiCodingAgentHarnessOptions{
		Name: "fixture", Model: &PiCodingAgentModelSelection{Provider: "fixture", ID: "fixture-chat"}, PigPath: pig, ExtensionPath: extension,
		agentFiles: map[string]string{"models.json": string(modelsJSON)},
	}
}

func TestRunPiCodingAgentAnswersAndRecordsTheRun(t *testing.T) {
	model := newScriptedModel(t)
	options := harnessFixture(t, model)
	options.NoTools = "all"
	options.WorkspaceFiles = map[string]string{"notes/a.txt": "alpha"}
	var seen AgentRun
	options.Output = func(_ context.Context, run AgentRun) (any, error) {
		seen = run
		content, err := os.ReadFile(filepath.Join(run.Workspace, "notes", "a.txt"))
		return map[string]any{"response": run.Response, "fixture": string(content)}, err
	}
	run, err := RunPiCodingAgent(t.Context(), PromptInput("What is the capital of France?"), options)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"response": "Paris", "fixture": "alpha"}; !reflect.DeepEqual(run.Output, want) {
		t.Errorf("output = %v, want %v", run.Output, want)
	}
	if run.Usage.Provider != "fixture" || run.Usage.Model != "fixture-chat" || run.Usage.InputTokens != 7 || run.Usage.OutputTokens != 3 || run.Usage.TotalTokens != 10 {
		t.Errorf("usage = %+v", run.Usage)
	}
	if _, priced := run.Usage.Metadata["estimatedCostUsd"]; priced {
		t.Errorf("an unpriced model reported a cost: %v", run.Usage.Metadata)
	}
	if want := []TranscriptEvent{
		{"type": "message", "role": "user", "content": "What is the capital of France?"},
		{"type": "message", "role": "assistant", "content": "Paris"},
	}; !reflect.DeepEqual(run.Events, want) {
		t.Errorf("events = %v", run.Events)
	}
	// The recorded system prompt is the one the provider received, and its digest is in the metadata.
	received := model.systemPrompt(t, 0)
	if seen.SystemPrompt != received {
		t.Errorf("system prompt differs from the request:\n got %q\nwant %q", seen.SystemPrompt, received)
	}
	digest := sha256.Sum256([]byte(received))
	if run.Metadata["systemPromptSha256"] != hex.EncodeToString(digest[:]) {
		t.Errorf("metadata = %v", run.Metadata)
	}
	snapshot, _ := run.Artifacts[PiSessionSnapshotArtifact].(string)
	if !strings.Contains(snapshot, `"type":"session"`) || !strings.Contains(snapshot, "Paris") {
		t.Errorf("session snapshot = %q", snapshot)
	}
	// harness.ts:344 names the run by its Session ID.
	var header struct {
		ID string `json:"id"`
	}
	firstLine, _, _ := strings.Cut(snapshot, "\n")
	if err := json.Unmarshal([]byte(firstLine), &header); err != nil || header.ID == "" || run.Artifacts["runId"] != header.ID {
		t.Errorf("runId artifact = %v, want the Session ID %q (%v)", run.Artifacts["runId"], header.ID, err)
	}
	if run.TotalMs <= 0 {
		t.Errorf("totalMs = %v", run.TotalMs)
	}
	if _, err := os.Stat(seen.Workspace); !os.IsNotExist(err) {
		t.Errorf("the run's root outlived the run: %v", err)
	}
}

func TestRunPiCodingAgentTransformsTheSystemPromptThroughTheBridge(t *testing.T) {
	model := newScriptedModel(t)
	options := harnessFixture(t, model)
	options.NoTools = "all"
	variant := false
	options.Name, options.ExpectedPiDocumentation = "without_docs", &variant
	options.TransformSystemPrompt = ExcludePiDocumentation
	run, err := RunPiCodingAgent(t.Context(), PromptInput("hi"), options)
	if err != nil {
		t.Fatal(err)
	}
	sent := model.systemPrompt(t, 0)
	if strings.Contains(sent, "\n<docs>\n") || !strings.Contains(sent, "\n<rules>\n") {
		t.Errorf("provider received the unstripped prompt:\n%s", sent)
	}
	digest := sha256.Sum256([]byte(sent))
	if run.Metadata["systemPromptSha256"] != hex.EncodeToString(digest[:]) {
		t.Errorf("recorded prompt is not the transformed prompt the provider received: %v", run.Metadata)
	}

	// The with_docs variant expects the documentation section and sends the prompt unchanged.
	withDocs := true
	options.Name, options.ExpectedPiDocumentation, options.TransformSystemPrompt = "with_docs", &withDocs, nil
	if _, err := RunPiCodingAgent(t.Context(), PromptInput("hi"), options); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(model.systemPrompt(t, 1), "\n<docs>\n") {
		t.Error("with_docs prompt lost its documentation section")
	}

	// The without_docs variant leaves no documentation on disk for the agent to read; with_docs keeps it.
	for variant, wantDocs := range map[string]bool{"without_docs": false, "with_docs": true} {
		check := variant == "with_docs"
		options.Name, options.ExpectedPiDocumentation, options.TransformSystemPrompt = variant, &check, nil
		if !check {
			options.TransformSystemPrompt = ExcludePiDocumentation
		}
		options.Output = func(_ context.Context, run AgentRun) (any, error) {
			_, err := os.Stat(filepath.Join(run.AgentDir, "..", "docs", "README.md"))
			return err == nil, nil
		}
		docsRun, err := RunPiCodingAgent(t.Context(), PromptInput("hi"), options)
		if err != nil || docsRun.Output != wantDocs {
			t.Errorf("%s: docs on disk = %v, %v; want %v", variant, docsRun, err, wantDocs)
		}
	}
	options.Output = nil

	// A harness that expects documentation fails when the transform removed it, and keeps the measured run.
	options.Name, options.ExpectedPiDocumentation, options.TransformSystemPrompt = "mismatch", &withDocs, ExcludePiDocumentation
	_, err = RunPiCodingAgent(t.Context(), PromptInput("hi"), options)
	var failed *HarnessRunError
	if !errors.As(err, &failed) || failed.Err.Error() != "Pi system prompt does not match the mismatch eval variant." || failed.Run == nil || failed.Run.Usage.TotalTokens != 10 {
		t.Fatalf("mismatch error = %v", err)
	}
}

func TestRunPiCodingAgentServesCustomToolsThroughTheBridge(t *testing.T) {
	model := newScriptedModel(t, scriptedReply{tool: "submit_audit", arguments: `{"verdict":"match"}`})
	options := harnessFixture(t, model)
	var received map[string]any
	var receivedID string
	options.Tools = []string{"read", "submit_audit"}
	options.CustomTools = []CustomTool{{
		Tool: bridge.Tool{Name: "submit_audit", Description: "Submit the audit.", PromptSnippet: "Submit the audit verdict", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"verdict": map[string]any{"type": "string"}}, "required": []string{"verdict"},
		}},
		Execute: func(_ context.Context, toolCallID string, params map[string]any) (CustomToolResult, error) {
			received, receivedID = params, toolCallID
			return CustomToolResult{Content: "Audit submitted.", Details: params, Terminate: true}, nil
		},
	}}
	options.Output = func(_ context.Context, run AgentRun) (any, error) { return run.SuccessfulTools, nil }
	run, err := RunPiCodingAgent(t.Context(), PromptInput("Audit it."), options)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(received, map[string]any{"verdict": "match"}) || receivedID != "call_1" {
		t.Errorf("tool received %v for call %q", received, receivedID)
	}
	// defineTool's promptSnippet lists the custom tool in the system prompt the model receives (system-prompt.ts:157-159).
	if sent := model.systemPrompt(t, 0); !strings.Contains(sent, "\n- submit_audit: Submit the audit verdict\n") {
		t.Errorf("the custom tool's prompt snippet is missing from the system prompt:\n%s", sent)
	}
	if !reflect.DeepEqual(run.Output, map[string]string{"submit_audit": "Audit submitted."}) {
		t.Errorf("successful tools = %v", run.Output)
	}
	if len(model.requests) != 1 {
		t.Errorf("a terminating tool must end the run without another model request; got %d requests", len(model.requests))
	}
	var calls []TranscriptEvent
	for _, event := range run.Events {
		if event["type"] == "tool_call" || event["type"] == "tool_result" {
			calls = append(calls, event)
		}
	}
	if len(calls) != 2 || calls[0]["name"] != "submit_audit" || !reflect.DeepEqual(calls[0]["arguments"], map[string]any{"verdict": "match"}) || calls[1]["content"] != "Audit submitted." {
		t.Errorf("transcript tool events = %v", calls)
	}
}

// TestRunPiCodingAgentRejectsUnexpectedExtensions is harness.ts:357-363: a configured extension that is not the
// harness's own fails the run before any prompt, naming the extension's path.
func TestRunPiCodingAgentRejectsUnexpectedExtensions(t *testing.T) {
	model := newScriptedModel(t)
	options := harnessFixture(t, model)
	options.NoTools = "all"
	options.TransformSystemPrompt = func(prompt string) (string, error) { return prompt, nil }
	other := filepath.Join(t.TempDir(), "pig-eval-extension")
	data, err := os.ReadFile(options.ExtensionPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, data, 0o755); err != nil {
		t.Fatal(err)
	}
	settings, _ := json.Marshal(map[string]any{"extensions": []string{other}})
	options.agentFiles["settings.json"] = string(settings)
	run, err := RunPiCodingAgent(t.Context(), PromptInput("hi"), options)
	if run != nil || err == nil || !strings.Contains(err.Error(), "Isolated eval loaded unexpected extensions: "+other) {
		t.Fatalf("run = %v, err = %v", run, err)
	}
	if len(model.requests) != 0 {
		t.Errorf("the model received %d requests before the extension check failed", len(model.requests))
	}
}

func TestRunPiCodingAgentReloadContinuesTheSession(t *testing.T) {
	model := newScriptedModel(t, scriptedReply{text: "first"}, scriptedReply{text: "second"})
	options := harnessFixture(t, model)
	options.NoTools = "all"
	run, err := RunPiCodingAgent(t.Context(), PiCodingAgentInput{
		{Type: PiCodingAgentStepPrompt, Content: "one"}, {Type: PiCodingAgentStepReload}, {Type: PiCodingAgentStepPrompt, Content: "two"},
	}, options)
	if err != nil {
		t.Fatal(err)
	}
	if run.Output != "second" {
		t.Errorf("output = %v, want the last prompt's answer", run.Output)
	}
	var users []string
	for _, event := range run.Events {
		if event["type"] == "message" && event["role"] == "user" {
			users = append(users, event["content"].(string))
		}
	}
	if !reflect.DeepEqual(users, []string{"one", "two"}) {
		t.Errorf("one Session must hold both prompts after a reload; user messages = %v", users)
	}
	if run.Usage.TotalTokens != 20 {
		t.Errorf("session totals cover both prompts: %+v", run.Usage)
	}
}

func TestRunPiCodingAgentFailures(t *testing.T) {
	t.Run("no model selected", func(t *testing.T) {
		model := newScriptedModel(t)
		options := harnessFixture(t, model)
		options.Model = nil
		if _, err := RunPiCodingAgent(t.Context(), PromptInput("x"), options); err == nil || err.Error() != "Select a harness model explicitly or set both PI_PROVIDER and PI_MODEL as defaults." {
			t.Errorf("error = %v", err)
		}
	})
	t.Run("unknown model", func(t *testing.T) {
		options := harnessFixture(t, newScriptedModel(t))
		options.Model = &PiCodingAgentModelSelection{Provider: "fixture", ID: "missing"}
		if _, err := RunPiCodingAgent(t.Context(), PromptInput("x"), options); err == nil || err.Error() != "Eval model not found: fixture/missing" {
			t.Errorf("error = %v", err)
		}
	})
	t.Run("no prompt step", func(t *testing.T) {
		options := harnessFixture(t, newScriptedModel(t))
		if _, err := RunPiCodingAgent(t.Context(), PiCodingAgentInput{{Type: PiCodingAgentStepReload}}, options); err == nil || !strings.Contains(err.Error(), "Pi eval input must include at least one prompt step.") {
			t.Errorf("error = %v", err)
		}
	})
	t.Run("workspace fixture escapes", func(t *testing.T) {
		options := harnessFixture(t, newScriptedModel(t))
		for name, want := range map[string]string{"../x": "Workspace fixture escapes the workspace: ../x", "/abs": "Invalid workspace fixture path: /abs", "": "Invalid workspace fixture path: "} {
			options.WorkspaceFiles = map[string]string{name: "x"}
			if _, err := RunPiCodingAgent(t.Context(), PromptInput("x"), options); err == nil || err.Error() != want {
				t.Errorf("fixture %q error = %v, want %q", name, err, want)
			}
		}
	})
	t.Run("provider failure keeps no result", func(t *testing.T) {
		model := newScriptedModel(t)
		model.status = http.StatusInternalServerError
		options := harnessFixture(t, model)
		options.NoTools = "all"
		run, err := RunPiCodingAgent(t.Context(), PromptInput("x"), options)
		if err == nil || run != nil {
			t.Fatalf("run = %v, err = %v; want a failure", run, err)
		}
	})
	t.Run("output failure is the run failure", func(t *testing.T) {
		options := harnessFixture(t, newScriptedModel(t))
		options.NoTools = "all"
		options.Output = func(context.Context, AgentRun) (any, error) { return nil, errors.New("output failed") }
		_, err := RunPiCodingAgent(t.Context(), PromptInput("x"), options)
		var failed *HarnessRunError
		if !errors.As(err, &failed) || failed.Err.Error() != "output failed" || failed.Run == nil {
			t.Errorf("error = %v", err)
		}
	})
	t.Run("empty answer", func(t *testing.T) {
		options := harnessFixture(t, newScriptedModel(t, scriptedReply{text: ""}))
		options.NoTools = "all"
		if _, err := RunPiCodingAgent(t.Context(), PromptInput("x"), options); err == nil || !strings.Contains(err.Error(), "produced no assistant text") {
			t.Errorf("error = %v", err)
		}
	})
	t.Run("cancelled during a prompt aborts the agent", func(t *testing.T) {
		model := newScriptedModel(t)
		model.delay = time.Minute
		options := harnessFixture(t, model)
		options.NoTools = "all"
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		started := time.Now()
		run, err := RunPiCodingAgent(ctx, PromptInput("x"), options)
		if run != nil || err == nil || !strings.Contains(err.Error(), "Request aborted") {
			t.Errorf("run = %v, error = %v; want an aborted run", run, err)
		}
		if time.Since(started) > 30*time.Second {
			t.Errorf("the abort did not stop the run promptly: %v", time.Since(started))
		}
	})
	t.Run("cancelled before start", func(t *testing.T) {
		options := harnessFixture(t, newScriptedModel(t))
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := RunPiCodingAgent(ctx, PromptInput("x"), options); !errors.Is(err, context.Canceled) {
			t.Errorf("error = %v", err)
		}
	})
}

// TestRunPiCodingAgentSandbox runs with a sandbox identity. A non-root runner must refuse, as enterToolSandbox does
// (harness.ts:165-167); as root the run's tree belongs to the identity and the agent runs as that user.
func TestRunPiCodingAgentSandbox(t *testing.T) {
	model := newScriptedModel(t)
	options := harnessFixture(t, model)
	options.NoTools = "all"
	t.Setenv("PI_EVAL_SANDBOX_UID", "65532")
	t.Setenv("PI_EVAL_SANDBOX_GID", "65532")
	// harness.ts:328-329 removes the runner's auth.json before the sandbox drop, so the agent cannot read it.
	hostAuth := filepath.Join(os.Getenv(agentDirEnvironment()), "auth.json")
	if err := os.WriteFile(hostAuth, []byte(`{"fixture":{"type":"api_key","key":"fixture-key"},"other":{"type":"api_key","key":"other-key"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := os.Stat(hostAuth); !os.IsNotExist(err) {
			t.Errorf("the runner's auth.json outlived the sandboxed run: %v", err)
		}
	})
	var owner uint32
	options.Output = func(_ context.Context, run AgentRun) (any, error) {
		info, err := os.Stat(run.Workspace)
		if err != nil {
			return nil, err
		}
		owner = sandboxOwner(info)
		return nil, nil
	}
	run, err := RunPiCodingAgent(t.Context(), PromptInput("hi"), options)
	if os.Geteuid() != 0 {
		if err == nil || !strings.Contains(err.Error(), "The eval runner must start as root before entering the unprivileged tool sandbox.") {
			t.Fatalf("non-root run = %v, %v", run, err)
		}
		return
	}
	if err != nil || owner != 65532 {
		t.Fatalf("root run: err = %v, workspace owner = %d, want 65532", err, owner)
	}
}

// TestCurrentSystemPromptReplaysEverySystemMessage pins getCurrentSystemPrompt (pi-ai utils/transcript.ts:73-102):
// later content is appended to the base prompt, sections are patched by name in first-seen order, and a null section
// removes it.
func TestCurrentSystemPromptReplaysEverySystemMessage(t *testing.T) {
	messages := []json.RawMessage{
		json.RawMessage(`{"role":"system","content":"base","sections":{"a":"A1","b":"B","c":"C"},"timestamp":1}`),
		json.RawMessage(`{"role":"user","content":"hi","timestamp":2}`),
		json.RawMessage(`{"role":"system","content":"more","sections":{"b":null,"a":"A2","d":"D"},"timestamp":3}`),
	}
	got, err := currentSystemPrompt(messages)
	if err != nil {
		t.Fatal(err)
	}
	if want := "base\n\nmore\n\nA2\n\nC\n\nD"; got != want {
		t.Errorf("currentSystemPrompt = %q, want %q", got, want)
	}
	if got, err := currentSystemPrompt([]json.RawMessage{json.RawMessage(`{"role":"user","content":"hi","timestamp":1}`)}); err != nil || got != "" {
		t.Errorf("no system message: %q, %v", got, err)
	}
}

// TestRunPiCodingAgentKeepsTheAPIKeyOutOfTheAgentsFiles pins Pi's in-memory credential (harness.ts:312-326): the
// provider receives the host's API key, while the isolated auth.json the agent can read holds only the command that
// fetches it from the bridge.
func TestRunPiCodingAgentKeepsTheAPIKeyOutOfTheAgentsFiles(t *testing.T) {
	model := newScriptedModel(t)
	options := harnessFixture(t, model)
	options.NoTools = "all"
	modelsJSON, err := json.Marshal(map[string]any{"providers": map[string]any{"fixture": map[string]any{
		"baseUrl": model.server.URL + "/v1", "api": "openai-completions",
		"models": []any{map[string]any{"id": "fixture-chat", "name": "Fixture Chat", "input": []string{"text"}, "contextWindow": 8192, "maxTokens": 1024}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	options.agentFiles = map[string]string{"models.json": string(modelsJSON)}
	hostAuth := filepath.Join(os.Getenv(agentDirEnvironment()), "auth.json")
	if err := os.WriteFile(hostAuth, []byte(`{"fixture":{"type":"api_key","key":"host-secret-key"},"other":{"type":"api_key","key":"other-secret-key"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var agentAuth string
	options.Output = func(_ context.Context, run AgentRun) (any, error) {
		data, err := os.ReadFile(filepath.Join(run.AgentDir, "auth.json"))
		agentAuth = string(data)
		return run.Response, err
	}
	if _, err := RunPiCodingAgent(t.Context(), PromptInput("hi"), options); err != nil {
		t.Fatal(err)
	}
	if len(model.authorizations) != 1 || model.authorizations[0] != "Bearer host-secret-key" {
		t.Errorf("provider authorization = %q, want the host API key", model.authorizations)
	}
	if strings.Contains(agentAuth, "secret-key") || !strings.Contains(agentAuth, `"key":"!`) {
		t.Errorf("the agent's auth.json exposes a credential: %s", agentAuth)
	}
}
