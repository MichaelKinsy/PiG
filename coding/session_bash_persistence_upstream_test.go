package coding

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

type bashPersistenceOperations func(context.Context, string, string, extension.BashOperationsExecOptions) (extension.BashOperationsResult, error)

func (operations bashPersistenceOperations) Exec(ctx context.Context, command, cwd string, options extension.BashOperationsExecOptions) (extension.BashOperationsResult, error) {
	return operations(ctx, command, cwd, options)
}

func bashEntryTypes(session *Session) []string {
	var types []string
	for _, entry := range session.Inner().GetEntries() {
		types = append(types, entry.Base().Type)
	}
	return types
}
func assertLastBashRole(t *testing.T, session *Session) {
	t.Helper()
	messages := session.Messages()
	if len(messages) == 0 || messages[len(messages)-1].Role() != agent.RoleBashExecution {
		t.Fatalf("last message must be bashExecution: %+v", messages)
	}
}

func TestUpstreamBashPersistence(t *testing.T) {
	harness := func(t *testing.T, opts harnessOptions, responses ...scriptedResponse) *recoveryHarness {
		t.Helper()
		opts.defaultTools = opts.tools == nil
		h := newRecoveryHarness(t, opts, responses...)

		return h
	}

	// upstream: packages/coding-agent/test/suite/agent-session-bash-persistence.test.ts:40
	t.Run("records bash results immediately while idle", func(t *testing.T) {
		h := harness(t, harnessOptions{emptySessionManager: true})
		if err := h.session.RecordBashResult("echo hi", BashResult{Output: "hi", ExitCode: new(0), Cancelled: false, Truncated: false}, false); err != nil {
			t.Fatal(err)
		}
		if bashHasPending(h.session) {
			t.Fatal("idle bash result remained pending")
		}
		assertLastBashRole(t, h.session)
		found := false
		for _, kind := range bashEntryTypes(h.session) {
			found = found || kind == "message"
		}
		if !found {
			t.Fatalf("entry types=%v want a message entry", bashEntryTypes(h.session))
		}
	})
	// upstream: packages/coding-agent/test/suite/agent-session-bash-persistence.test.ts:56
	t.Run("defers bash results while streaming and flushes them before the next prompt", func(t *testing.T) {
		release := make(chan struct{})
		releaseTool := sync.OnceFunc(func() { close(release) })
		h := harness(t, harnessOptions{emptySessionManager: true, tools: []agent.AgentTool{bashPersistenceWaitTool{release: release}}}, fauxToolCall("wait"), fauxReply("done", ai.StopReasonStop, 0), fauxReply("after flush", ai.StopReasonStop, 0))
		started := make(chan struct{})
		unsubscribe := h.session.Subscribe(func(event agent.AgentEvent) {
			if _, ok := event.(agent.ToolExecutionStartEvent); ok {
				close(started)
			}
		})
		done := make(chan error, 1)
		go func() { _, err := h.session.Send(t.Context(), "start"); done <- err }()
		join := sync.OnceFunc(func() {
			releaseTool()
			if err := <-done; err != nil {
				t.Error(err)
			}
		})
		t.Cleanup(join)
		<-started
		unsubscribe()
		if err := h.session.RecordBashResult("echo hi", BashResult{Output: "hi", ExitCode: new(0), Cancelled: false, Truncated: false}, false); err != nil {
			t.Fatal(err)
		}
		if !bashHasPending(h.session) {
			t.Fatal("streaming bash result was not deferred")
		}
		if slices.ContainsFunc(h.session.Messages(), func(message agent.AgentMessage) bool { return message.Role() == agent.RoleBashExecution }) {
			t.Fatal("bashExecution entered streaming history")
		}
		join()
		if bashHasPending(h.session) {
			t.Fatal("pending result survived first prompt")
		}
		if !slices.ContainsFunc(h.session.Messages(), func(message agent.AgentMessage) bool { return message.Role() == agent.RoleBashExecution }) {
			t.Fatal("bashExecution missing after first prompt")
		}
		if _, err := h.session.Send(t.Context(), "next turn"); err != nil {
			t.Fatal(err)
		}
		if bashHasPending(h.session) {
			t.Fatal("pending result survived next prompt")
		}
		if !slices.ContainsFunc(h.session.Messages(), func(message agent.AgentMessage) bool { return message.Role() == agent.RoleBashExecution }) {
			t.Fatal("bashExecution missing after next prompt")
		}
		if !slices.Contains(bashEntryTypes(h.session), "message") {
			t.Fatal("no message entries persisted")
		}
	})
	// upstream: packages/coding-agent/test/suite/agent-session-bash-persistence.test.ts:116
	t.Run("executes bash commands and records the result", func(t *testing.T) {
		h := harness(t, harnessOptions{emptySessionManager: true})
		result, err := h.session.ExecuteBash(t.Context(), "printf 'hello'", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(result.Output, "hello") {
			t.Fatalf("output=%q", result.Output)
		}
		assertLastBashRole(t, h.session)
	})
	// upstream: packages/coding-agent/test/suite/agent-session-bash-persistence.test.ts:126
	t.Run("cancels running bash commands with abortBash", func(t *testing.T) {
		h := harness(t, harnessOptions{emptySessionManager: true})
		started := make(chan struct{})
		operations := bashPersistenceOperations(func(ctx context.Context, _, _ string, _ extension.BashOperationsExecOptions) (extension.BashOperationsResult, error) {
			close(started)
			<-ctx.Done()
			return extension.BashOperationsResult{}, errors.New("aborted")
		})
		result := startBashPersistenceCall(t, h.session, "sleep", operations)
		<-started
		if !bashIsRunning(h.session) {
			t.Fatal("bash must be running before abort")
		}
		h.session.AbortBash()
		if !result().Cancelled {
			t.Fatal("bash result must be cancelled")
		}
		if bashIsRunning(h.session) {
			t.Fatal("bash remains running after completion")
		}
	})
	// upstream: packages/coding-agent/test/suite/agent-session-bash-persistence.test.ts:153
	t.Run("keeps newer bash execution tracked when an older execution finishes", func(t *testing.T) {
		h := harness(t, harnessOptions{emptySessionManager: true})
		invocations, operations := controlledBashPersistenceOperations()
		firstBash := startBashPersistenceCall(t, h.session, "first", operations)
		first := <-invocations
		secondBash := startBashPersistenceCall(t, h.session, "second", operations)
		second := <-invocations
		t.Cleanup(first.finish)
		t.Cleanup(second.finish)
		first.finish()
		firstResult := firstBash()
		runningAfterFirstSettles := bashIsRunning(h.session)
		h.session.AbortBash()
		secondWasAborted := second.signal.Err() != nil
		second.finish()
		secondResult := secondBash()
		if firstResult.Cancelled {
			t.Fatal("first result cancelled")
		}
		if !runningAfterFirstSettles {
			t.Fatal("newer execution was lost")
		}
		if !secondWasAborted {
			t.Fatal("second signal not aborted")
		}
		if !secondResult.Cancelled {
			t.Fatal("second result not cancelled")
		}
		if bashIsRunning(h.session) {
			t.Fatal("bash remains running")
		}
	})
	// upstream: packages/coding-agent/test/suite/agent-session-bash-persistence.test.ts:178
	t.Run("aborts all active bash executions", func(t *testing.T) {
		h := harness(t, harnessOptions{emptySessionManager: true})
		invocations, operations := controlledBashPersistenceOperations()
		firstBash := startBashPersistenceCall(t, h.session, "first", operations)
		first := <-invocations
		secondBash := startBashPersistenceCall(t, h.session, "second", operations)
		second := <-invocations
		t.Cleanup(first.finish)
		t.Cleanup(second.finish)
		h.session.AbortBash()
		abortedSignals := []bool{first.signal.Err() != nil, second.signal.Err() != nil}
		first.finish()
		second.finish()
		results := []bool{firstBash().Cancelled, secondBash().Cancelled}
		if !reflect.DeepEqual(abortedSignals, []bool{true, true}) {
			t.Fatalf("signals=%v", abortedSignals)
		}
		if !reflect.DeepEqual(results, []bool{true, true}) {
			t.Fatalf("cancelled=%v", results)
		}
		if bashIsRunning(h.session) {
			t.Fatal("bash remains running")
		}
	})
	// upstream: packages/coding-agent/test/suite/agent-session-bash-persistence.test.ts:199
	t.Run("persists user, assistant, toolResult, and custom messages in order", func(t *testing.T) {
		h := harness(t, harnessOptions{emptySessionManager: true, tools: []agent.AgentTool{bashPersistenceEchoTool{}}}, bashPersistenceEchoCalls("hello"), fauxReply("done", ai.StopReasonStop, 0))
		if err := h.session.SendCustomMessage(t.Context(), extension.CustomMessageRef{CustomType: "note", Content: "hello", Display: true, Details: map[string]any{"a": 1}}, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := h.session.Send(t.Context(), "start"); err != nil {
			t.Fatal(err)
		}
		wantTypes := []string{"custom_message", "message", "message", "message", "message", "message"}
		if got := bashEntryTypes(h.session); !reflect.DeepEqual(got, wantTypes) {
			t.Fatalf("entry types=%v want=%v", got, wantTypes)
		}
		var roles []string
		for _, message := range h.session.Messages() {
			roles = append(roles, message.Role())
		}
		if want := []string{"custom", "system", "user", "assistant", "toolResult", "assistant"}; !reflect.DeepEqual(roles, want) {
			t.Fatalf("roles=%v want=%v", roles, want)
		}
	})
	// upstream: packages/coding-agent/test/suite/agent-session-bash-persistence.test.ts:245
	t.Run("does not emit message_end for bash execution messages", func(t *testing.T) {
		h := harness(t, harnessOptions{emptySessionManager: true})
		var roles []string
		h.session.Subscribe(func(event agent.AgentEvent) {
			if end, ok := event.(agent.MessageEndEvent); ok {
				roles = append(roles, end.Message.Role())
			}
		})
		if err := h.session.RecordBashResult("echo hi", BashResult{Output: "hi", ExitCode: new(0), Cancelled: false, Truncated: false}, false); err != nil {
			t.Fatal(err)
		}
		if len(roles) != 0 {
			t.Fatalf("message_end roles=%v want []", roles)
		}
	})
	// upstream: packages/coding-agent/test/suite/agent-session-bash-persistence.test.ts:265
	t.Run("persists aborted assistant messages", func(t *testing.T) {
		h := harness(t, harnessOptions{emptySessionManager: true})
		provider := &bashPersistenceAbortProvider{text: strings.Repeat("x", 20_000), done: make(chan struct{})}
		model := *h.session.Agent().Model()
		model.Provider = provider
		h.session.Agent().SetModel(&model)
		updated := make(chan struct{})
		// The subscriber barrier prevents the producer from completing before the caller observes the first update.
		release := make(chan struct{})
		releaseUpdate := sync.OnceFunc(func() { close(release) })
		once := sync.OnceFunc(func() { close(updated); <-release })
		unsubscribe := h.session.Subscribe(func(event agent.AgentEvent) {
			if _, ok := event.(agent.MessageUpdateEvent); ok {
				once()
			}
		})
		defer unsubscribe()
		done := make(chan error, 1)
		go func() { _, err := h.session.Send(t.Context(), "hi"); done <- err }()
		join := sync.OnceFunc(func() {
			h.session.RequestAbort()
			releaseUpdate()
			if err := <-done; err != nil {
				t.Error(err)
			}
			<-provider.done
		})
		t.Cleanup(join)
		<-updated
		h.session.RequestAbort()
		releaseUpdate()
		if err := h.session.Abort(t.Context()); err != nil {
			t.Fatal(err)
		}
		join()
		entries := h.session.Inner().GetEntries()
		if len(entries) == 0 {
			t.Fatal("no persisted assistant")
		}
		last := entries[len(entries)-1]
		if last.Base().Type != "message" {
			t.Fatalf("last type=%q", last.Base().Type)
		}
		message, ok := last.(icodingagent.MessageEntry)
		if !ok || message.Message.Assistant == nil {
			t.Fatalf("last entry is not assistant: %s", last.Raw())
		}
		if message.Message.Assistant.StopReason != ai.StopReasonAborted {
			t.Fatalf("stopReason=%s", message.Message.Assistant.StopReason)
		}
	})
	// upstream: packages/coding-agent/test/suite/agent-session-bash-persistence.test.ts:294
	t.Run("records bash output through custom operations", func(t *testing.T) {
		h := harness(t, harnessOptions{emptySessionManager: true})
		operations := bashPersistenceOperations(func(_ context.Context, _, _ string, options extension.BashOperationsExecOptions) (extension.BashOperationsResult, error) {
			options.OnData([]byte("hello from custom ops"))
			return extension.BashOperationsResult{ExitCode: new(0)}, nil
		})
		result, err := h.session.ExecuteBash(t.Context(), "custom", nil, &ExecuteBashOptions{Operations: operations})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(result.Output, "hello from custom ops") {
			t.Fatalf("output=%q", result.Output)
		}
		assertLastBashRole(t, h.session)
	})
}

// Regression tests for https://github.com/earendil-works/pi/issues/10504: an escape sequence split across output chunks.
// upstream: packages/coding-agent/test/suite/agent-session-bash-persistence.test.ts:342 (v1.1.0)
func TestUpstreamBashEscapeSequencesSplitAcrossChunks(t *testing.T) {
	// runChunks runs a user bash command whose fake shell emits the given chunks; beforeExit sees what was streamed by then.
	runChunks := func(t *testing.T, chunks [][]byte, beforeExit func(streamed string)) (output, streamed, recorded string) {
		t.Helper()
		h := newRecoveryHarness(t, harnessOptions{emptySessionManager: true, defaultTools: true})
		var deltas []string
		operations := bashPersistenceOperations(func(_ context.Context, _, _ string, options extension.BashOperationsExecOptions) (extension.BashOperationsResult, error) {
			for _, chunk := range chunks {
				options.OnData(chunk)
			}
			if beforeExit != nil {
				beforeExit(strings.Join(deltas, ""))
			}
			return extension.BashOperationsResult{ExitCode: new(0)}, nil
		})
		result, err := h.session.ExecuteBash(t.Context(), "custom", func(delta string) { deltas = append(deltas, delta) }, &ExecuteBashOptions{Operations: operations})
		if err != nil {
			t.Fatal(err)
		}
		messages := h.session.Messages()
		last := messages[len(messages)-1]
		if last.Role() == agent.RoleBashExecution {
			recorded, _ = last.Custom["output"].(string)
		}
		return result.Output, strings.Join(deltas, ""), recorded
	}
	text := func(chunks ...string) [][]byte {
		out := make([][]byte, len(chunks))
		for i, chunk := range chunks {
			out[i] = []byte(chunk)
		}
		return out
	}

	// upstream: packages/coding-agent/test/suite/agent-session-bash-persistence.test.ts:366
	t.Run("strips a color reset split inside its parameters", func(t *testing.T) {
		output, streamed, recorded := runChunks(t, text("\x1b[31mERROR: file.py:1\x1b[0", "m\n"), nil)
		const want = "ERROR: file.py:1\n"
		if output != want || streamed != want || recorded != want {
			t.Fatalf("output=%q streamed=%q recorded=%q want %q", output, streamed, recorded, want)
		}
	})
	// upstream: packages/coding-agent/test/suite/agent-session-bash-persistence.test.ts:373
	t.Run("strips a color code split right after ESC", func(t *testing.T) {
		output, streamed, _ := runChunks(t, text("before\x1b", "[32mafter\n"), nil)
		const want = "beforeafter\n"
		if output != want || streamed != want {
			t.Fatalf("output=%q streamed=%q want %q", output, streamed, want)
		}
	})
	// upstream: packages/coding-agent/test/suite/agent-session-bash-persistence.test.ts:379
	t.Run("strips an OSC sequence split before its terminator", func(t *testing.T) {
		output, _, _ := runChunks(t, text("a\x1b]0;window ", "title\x1b", "\\b\n"), nil)
		if output != "ab\n" {
			t.Fatalf("output=%q want %q", output, "ab\n")
		}
	})
	// upstream: packages/coding-agent/test/suite/agent-session-bash-persistence.test.ts:384
	t.Run("flushes an incomplete multi-byte character at the end of output", func(t *testing.T) {
		output, streamed, _ := runChunks(t, [][]byte{[]byte("ok"), []byte("\u00e9")[:1]}, nil)
		const want = "ok\uFFFD"
		if output != want || streamed != want {
			t.Fatalf("output=%q streamed=%q want %q", output, streamed, want)
		}
	})
	// upstream: packages/coding-agent/test/suite/agent-session-bash-persistence.test.ts:390
	t.Run("does not hold back output behind a long unterminated sequence", func(t *testing.T) {
		long := strings.Repeat("x", 300)
		var streamedBeforeExit string
		runChunks(t, text("\x1b]"+long), func(streamed string) { streamedBeforeExit = streamed })
		if want := "]" + long; streamedBeforeExit != want {
			t.Fatalf("streamed before exit = %q want %q", streamedBeforeExit, want)
		}
	})
}
