package extensionconformance

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestConformance_UserBashOperationsRunInTheExtension pins the `{ operations }` result of pi.on("user_bash") (types.ts UserBashEventResult;
// runner.ts isUserBashEventResult; core/tools/bash.ts BashOperations): a handler in every SDK returns an object whose exec runs in the
// extension, and the host runs the user's command through it. Pi's exec receives `onData`, `signal`, `timeout` and `env`, resolves
// `{ exitCode: number | null }` and rejects to fail the command. The fixtures' exec is driven by the command it receives, so each
// observation below comes from the extension process: the bytes onData wrote in order (binary included), the options it was handed, the
// exit code (null stays null), a rejection's message and the abort that reaches its signal.
func TestConformance_UserBashOperationsRunInTheExtension(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	for _, tc := range sdkHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			ctx := t.Context()
			result, err := h.runner.EmitUserBash(ctx, extension.UserBashEvent{Type: "user_bash", Command: "operations", Cwd: t.TempDir()})
			if err != nil || result == nil || result.Operations == nil || result.Result != nil {
				t.Fatalf("user_bash operations reply: result=%+v error=%v, want exactly one operations object", result, err)
			}
			operations := result.Operations
			run := func(command, cwd string, options extension.BashOperationsExecOptions) ([][]byte, extension.BashOperationsResult, error) {
				t.Helper()
				var chunks [][]byte
				options.OnData = func(data []byte) { chunks = append(chunks, bytes.Clone(data)) }
				exec, err := operations.Exec(ctx, command, cwd, options)
				return chunks, exec, err
			}
			joined := func(chunks [][]byte) string { return string(bytes.Join(chunks, nil)) }

			timeout := 2.5
			chunks, exec, err := run("echo", "/work", extension.BashOperationsExecOptions{Timeout: &timeout, Env: []string{"B=2", "A=1"}})
			if err != nil || exec.ExitCode == nil || *exec.ExitCode != 3 {
				t.Fatalf("echo: exit=%v error=%v, want exit code 3", exec.ExitCode, err)
			}
			if want := "cmd:echo\ncwd:/work\nenv:A=1\nenv:B=2\ntimeout:2.5\n"; joined(chunks) != want || len(chunks) != 5 {
				t.Fatalf("echo output = %q in %d chunks, want %q in 5 chunks: the extension receives the command, cwd, env and timeout and onData keeps its chunks", joined(chunks), len(chunks), want)
			}

			chunks, exec, err = run("echo", "/work", extension.BashOperationsExecOptions{})
			if err != nil || joined(chunks) != "cmd:echo\ncwd:/work\n" {
				t.Fatalf("echo without options: output=%q error=%v, want no env and no timeout lines", joined(chunks), err)
			}

			// An empty env is not an absent one: Pi's `env: {}` replaces the inherited environment with nothing.
			chunks, exec, err = run("echo", "/work", extension.BashOperationsExecOptions{Env: []string{}})
			if err != nil || joined(chunks) != "cmd:echo\ncwd:/work\nenv-empty\n" {
				t.Fatalf("echo with an empty env: output=%q error=%v, want the extension to see an empty env object", joined(chunks), err)
			}

			chunks, exec, err = run("chunks", "/work", extension.BashOperationsExecOptions{})
			if err != nil || exec.ExitCode != nil || len(chunks) != 3 || string(chunks[0]) != "a" || string(chunks[1]) != "b" || string(chunks[2]) != "c" {
				t.Fatalf("chunks: chunks=%q exit=%v error=%v, want a, b, c in order and a null exit code", chunks, exec.ExitCode, err)
			}

			chunks, exec, err = run("binary", "/work", extension.BashOperationsExecOptions{})
			if err != nil || exec.ExitCode == nil || *exec.ExitCode != 0 || len(chunks) != 1 || !bytes.Equal(chunks[0], []byte{0xff, 0x00, 0x80}) {
				t.Fatalf("binary: chunks=%v exit=%v error=%v, want the raw bytes ff 00 80", chunks, exec.ExitCode, err)
			}

			_, _, err = run("missing", "/work", extension.BashOperationsExecOptions{})
			if err == nil || !strings.Contains(err.Error(), "exec failed: missing") {
				t.Fatalf("a rejecting exec: error=%v, want the extension's message", err)
			}

			// The same object runs again: Pi's object is not single-use.
			if chunks, _, err = run("chunks", "/work", extension.BashOperationsExecOptions{}); err != nil || joined(chunks) != "abc" {
				t.Fatalf("second run through the same operations: output=%q error=%v", joined(chunks), err)
			}

			// The production Session runs a user's command through the extension's object: the output, the exit code and the persisted
			// bash execution come from the extension's exec (agent-session.ts executeBash with options.operations).
			services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			provider := &productionToolProvider{}
			session, err := coding.NewSession(services, coding.SessionOptions{
				Model: &ai.Model{ID: "production-model", Provider: provider, ProviderMeta: ai.ProviderMetadata{ProviderID: provider.ID()}},
				Tools: []agent.AgentTool{}, SkipBuiltinTools: true, Runner: h.runner, SessionDir: t.TempDir(),
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			var streamed []string
			bash, err := session.ExecuteBash(ctx, "echo", func(chunk string) { streamed = append(streamed, chunk) }, &coding.ExecuteBashOptions{Operations: operations})
			if err != nil || bash.ExitCode == nil || *bash.ExitCode != 3 || bash.Cancelled {
				t.Fatalf("Session.ExecuteBash through the extension's operations: result=%+v error=%v, want exit code 3", bash, err)
			}
			if want := "cmd:echo\ncwd:" + session.Inner().CWD() + "\n"; bash.Output != want || strings.Join(streamed, "") != want {
				t.Fatalf("Session.ExecuteBash output = %q, streamed %q, want %q from the extension", bash.Output, streamed, want)
			}

			// An abort reaches the extension's signal, and Exec awaits the extension's exec until it settles (bash-executor.ts:120-128):
			// the chunk the exec writes after observing the abort is delivered, and the error is the extension's rejection.
			cancelCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			started := make(chan struct{}, 1)
			done := make(chan error, 1)
			var mu sync.Mutex
			var waited []string
			go func() {
				_, err := operations.Exec(cancelCtx, "wait", "/work", extension.BashOperationsExecOptions{OnData: func(data []byte) {
					mu.Lock()
					waited = append(waited, string(data))
					mu.Unlock()
					select {
					case started <- struct{}{}:
					default:
					}
				}})
				done <- err
			}()
			select {
			case <-started:
			case err := <-done:
				t.Fatalf("wait returned before the abort: %v", err)
			case <-time.After(10 * time.Second):
				t.Fatal("the extension's exec never started")
			}
			cancel()
			select {
			case err := <-done:
				if err == nil || err.Error() != "aborted" {
					t.Fatalf("an aborted exec: error=%v, want \"aborted\"", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("an abort never ended the exec")
			}
			mu.Lock()
			defer mu.Unlock()
			if want := []string{"waiting", "stopped"}; !slices.Equal(waited, want) {
				t.Fatalf("aborted exec chunks = %q, want %q: Exec returns only after the extension's exec settled", waited, want)
			}
		})
	}
}
