//go:build !pig_strip_node_extensions

package codingagent_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// A Node extension whose first session_start of the run (across processes: a flag file) stays pending until the test writes its verdict into the shared directory: "crash" ends the process, "fail" throws, anything else returns. Every later session_start, in the process a replacement starts, returns at once, as the probe's extension of the parity scenario does with its 1.5 s wait.
const heldStartSource = `import fs from "node:fs";
import path from "node:path";
const dir = process.env.HELD_START_DIR;
export default function register(pi) {
  pi.on("session_start", async () => {
    try {
      fs.writeFileSync(path.join(dir, "held"), "1", { flag: "wx" });
    } catch {
      return;
    }
    fs.writeFileSync(path.join(dir, "started"), "1");
    for (;;) {
      try {
        const verdict = fs.readFileSync(path.join(dir, "verdict"), "utf8");
        if (verdict === "crash") process.exit(3);
        if (verdict === "fail") throw new Error("own failure");
        return;
      } catch (error) {
        if (error && error.code !== "ENOENT") throw error;
      }
      await new Promise((resolve) => setTimeout(resolve, 20));
    }
  });
}
`

// swapRuntime adapts *coding.Runtime to the Session replacement contract of InteractiveMode as coding/cli's interactiveRuntime does.
type swapRuntime struct{ rt *coding.Runtime }

func (r swapRuntime) NewSession(ctx context.Context, options *extension.NewSessionOptions) (extension.CancelledResult, error) {
	return r.rt.NewSession(ctx, options)
}

func (r swapRuntime) SwitchSession(ctx context.Context, path, cwdOverride string, projectTrustUI func(cwd string) extension.ProjectTrustContext, options *extension.SwitchSessionOptions) (extension.CancelledResult, error) {
	switchOptions := &coding.SwitchSessionOptions{CwdOverride: cwdOverride, ProjectTrustContextFactory: projectTrustUI}
	if options != nil {
		switchOptions.WithSession = options.WithSession
	}
	return r.rt.SwitchSession(ctx, path, switchOptions)
}

func (r swapRuntime) Fork(ctx context.Context, entryID string, options *extension.ForkOptions) (icodingagent.InteractiveForkResult, error) {
	result, err := r.rt.Fork(ctx, entryID, options)
	return icodingagent.InteractiveForkResult{Cancelled: result.Cancelled, SelectedText: result.SelectedText}, err
}

func (r swapRuntime) ImportFromJsonl(ctx context.Context, path, cwdOverride string) (extension.CancelledResult, error) {
	return r.rt.ImportFromJsonl(ctx, path, cwdOverride)
}

func (r swapRuntime) TakeUnreportedDiagnostics() []icodingagent.AgentSessionRuntimeDiagnostic {
	return r.rt.TakeUnreportedDiagnostics()
}

func (r swapRuntime) ExtensionCommandActions(session icodingagent.InteractiveSessionHandle) extension.CommandActions {
	current, ok := session.(*coding.Session)
	if !ok {
		return extension.CommandActions{}
	}
	return r.rt.ExtensionCommandActions(current)
}

func (r swapRuntime) EmitQuitShutdown()          { r.rt.EmitQuitShutdown() }
func (r swapRuntime) EmitQuitShutdownAfterStop() { r.rt.EmitQuitShutdown() }
func (r swapRuntime) SetBeforeQuitDispose(settle func()) {
	r.rt.SetBeforeQuitDispose(settle)
}
func (r swapRuntime) SetBeforeSessionReplacement(drain func(context.Context) error) {
	r.rt.SetBeforeSessionReplacement(drain)
}
func (r swapRuntime) SetBeforeSessionInvalidate(hook func()) { r.rt.SetBeforeSessionInvalidate(hook) }
func (r swapRuntime) SetRebindSession(rebind func(context.Context, icodingagent.InteractiveSessionHandle) error) {
	r.rt.SetRebindSession(func(ctx context.Context, session *coding.Session) error { return rebind(ctx, session) })
}

// heldStart is an interactive mode over a Runtime whose every Session has its own Node extension process, as the production factory builds them (coding/cli cliSessionFactory), and whose startup session_start is still pending.
type heldStart struct {
	dir       string
	runtime   *coding.Runtime
	h         *icodingagent.TestHarness
	first     *coding.Session
	bound     <-chan error
	forkEntry string
	other     string
	runnersMu sync.Mutex
	runners   map[*coding.Session]*inproc.Runner
	// reported is every error any runner of the test reported to its listeners: the error sink, which no replacement of the transcript clears.
	reported []extension.ExtensionError
}

func (f *heldStart) reports() []extension.ExtensionError {
	f.runnersMu.Lock()
	defer f.runnersMu.Unlock()
	return append([]extension.ExtensionError(nil), f.reported...)
}

func newHeldStart(t *testing.T) *heldStart {
	t.Helper()
	f := &heldStart{dir: t.TempDir(), runners: map[*coding.Session]*inproc.Runner{}}
	t.Setenv("PIG_HOME", filepath.Join(f.dir, "pig-home"))
	t.Setenv("HELD_START_DIR", f.dir)
	source := filepath.Join(f.dir, "ext")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"package.json": `{"type":"module","main":"main.js"}`, "main.js": heldStartSource} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cwd, agentDir, sessions := t.TempDir(), t.TempDir(), filepath.Join(f.dir, "sessions")
	model := &ai.Model{ID: "faux-1", Provider: &scriptedProvider{}, Capabilities: ai.ModelCapabilities{ContextWindow: 128000}}
	factory := func(_ context.Context, options coding.CreateAgentSessionRuntimeOptions) (coding.CreateAgentSessionRuntimeResult, error) {
		services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: options.CWD, AgentDir: options.AgentDir})
		if err != nil {
			return coding.CreateAgentSessionRuntimeResult{}, err
		}
		if err := configureFauxAuth(services, "faux-key"); err != nil {
			return coding.CreateAgentSessionRuntimeResult{}, err
		}
		host := subprocess.NewHost(t.TempDir())
		host.SetMode("tui")
		loaded, err := host.Load(t.Context(), subprocess.ExtConfig{Name: "held-start", Source: source, Enabled: true})
		if err != nil {
			return coding.CreateAgentSessionRuntimeResult{}, err
		}
		runner := inproc.NewRunner([]extension.Extension{*loaded}, options.CWD)
		runner.AddErrorListener(func(err *extension.ExtensionError) {
			f.runnersMu.Lock()
			f.reported = append(f.reported, *err)
			f.runnersMu.Unlock()
		})
		session, err := coding.NewSession(services, coding.SessionOptions{SessionManager: options.SessionManager, Model: model, Runner: runner, SkipBuiltinTools: true})
		if err != nil {
			host.Shutdown("construct session failed")
			return coding.CreateAgentSessionRuntimeResult{}, err
		}
		f.runnersMu.Lock()
		f.runners[session] = runner
		f.runnersMu.Unlock()
		// The production factory's Dispose releases the outgoing Session's host at once when no command handler of it is running (cliRetirement.retire).
		return coding.CreateAgentSessionRuntimeResult{Session: session, Services: services, Dispose: func(reason string) { host.Shutdown(reason) }}, nil
	}
	manager, err := icodingagent.NewSessionManagerWithDir(cwd, sessions).Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	f.runtime, err = coding.CreateAgentSessionRuntime(t.Context(), factory, coding.CreateAgentSessionRuntimeOptions{CWD: cwd, AgentDir: agentDir, SessionManager: manager})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.runtime.Close() })
	f.first = f.runtime.Session()
	var entryErr error
	f.forkEntry, entryErr = f.first.Inner().AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText("fork prefill")}})
	if entryErr != nil {
		t.Fatal(entryErr)
	}
	if _, err := f.first.Inner().AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "old answer"}}, StopReason: ai.StopReasonStop}}); err != nil {
		t.Fatal(err)
	}
	// The session /resume opens: a second session file of the same directory.
	otherManager, err := icodingagent.NewSessionManagerWithDir(cwd, sessions).Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := otherManager.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText("other question")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := otherManager.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "other answer"}}, StopReason: ai.StopReasonStop}}); err != nil {
		t.Fatal(err)
	}
	f.other = *otherManager.GetSessionFile()

	opts := icodingagent.InteractiveModeOptions{CWD: cwd, AgentDir: agentDir, SessionHandle: f.first, Model: model, ExtensionRunner: f.runner(f.first), SettingsManager: f.runtime.Services().SettingsManager(), ModelRegistry: f.runtime.Services().Registry().ModelRegistry, NoThemes: true, NoSkills: true, NoPromptTemplates: true}
	f.h = icodingagent.NewTestHarness(t, opts, nil)
	f.h.SetRuntimeHost(swapRuntime{f.runtime})
	f.runtime.SetRebindSession(func(ctx context.Context, session *coding.Session) error {
		f.h.Do(func() {
			f.h.SetRebindResources(session.Services().SettingsManager(), session.Services().Registry().ModelRegistry)
		})
		return f.h.RebindSession(ctx, session, f.runner(session), nil, true)
	})
	// The mode binds the startup Session: its session_start handler is the one the extension holds.
	bound := make(chan error, 1)
	f.bound = bound
	go func() { bound <- f.h.RebindSession(t.Context(), f.first, f.runner(f.first), nil, true) }()
	f.waitFor(t, "started")
	return f
}

func (f *heldStart) runner(session *coding.Session) *inproc.Runner {
	f.runnersMu.Lock()
	defer f.runnersMu.Unlock()
	return f.runners[session]
}

// waitFor waits until the extension wrote the file name into the shared directory.
func (f *heldStart) waitFor(t *testing.T, name string) {
	t.Helper()
	deadline := time.Now().Add(testbudget.Wait(t))
	for {
		if _, err := os.Stat(filepath.Join(f.dir, name)); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the extension never wrote %s", name)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (f *heldStart) verdict(t *testing.T, verdict string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, "verdict"), []byte(verdict), 0o644); err != nil {
		t.Fatal(err)
	}
}

// startupBindEnded returns what the startup bind of the held session_start returned once it ended.
func (f *heldStart) startupBindEnded(t *testing.T) error {
	t.Helper()
	select {
	case err := <-f.bound:
		return err
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("the startup bind never ended")
		return nil
	}
}

// shown is what the user sees of the extension errors: the lines of the transcript once the main loop showed the queued errors, and the queue itself before.
func (f *heldStart) shown() (queued []extension.ExtensionError, chat string) {
	queued = f.h.PendingExtensionErrors()
	f.h.ShowPendingExtensionErrors()
	return queued, f.h.Chat()
}

// A user who orders /new, /resume or /fork while a session_start handler of the extension has not returned replaces the Session; the outgoing Session's extension process stops and the handler is cut off. Pi runs the extension in its own process, lets the handler finish and shows nothing, so the cut-off is no error of the extension: the user sees no `Extension "..." error: connection closed`, and the replacement Session works.
func TestSessionReplacementDuringAHeldSessionStartShowsNoExtensionError(t *testing.T) {
	for _, operation := range []string{"new", "resume", "fork"} {
		t.Run(operation, func(t *testing.T) {
			f := newHeldStart(t)
			target := ""
			switch operation {
			case "resume":
				target = f.other
			case "fork":
				target = f.forkEntry
			}
			if err := f.h.ReplaceSession(operation, target); err != nil {
				t.Fatalf("/%s during the hold: %v", operation, err)
			}
			bindErr := f.startupBindEnded(t)
			queued, chat := f.shown()
			reported := f.reports()
			t.Logf("startup bind returned %v; reported %+v", bindErr, reported)
			if len(reported) != 0 || len(queued) != 0 || strings.Contains(chat, "connection closed") || strings.Contains(chat, "error:") {
				t.Fatalf("/%s during the hold showed an extension error; startup bind returned %v; reported %+v; transcript:\n%s", operation, bindErr, reported, chat)
			}
			current := f.runtime.Session()
			if current == f.first {
				t.Fatalf("/%s did not replace the Session", operation)
			}
			// The replacement's extension process answers: its own session_start returned at once and the runner reaches it.
			if _, err := f.runner(current).Emit(t.Context(), extension.SessionStartEvent{Type: "session_start", Reason: "startup"}); err != nil {
				t.Fatalf("the replacement Session's extension: %v", err)
			}
			if reported := f.reports(); len(reported) != 0 {
				t.Fatalf("the replacement Session's extension reported %+v", reported)
			}
		})
	}
}

// The controls: a handler that ends on its own account while it is held is still reported. The extension process crashing during the hold, and the handler's own error answer, are shown as they were.
func TestHeldSessionStartFailuresAreStillShown(t *testing.T) {
	for _, tc := range []struct{ verdict, want string }{{"crash", ""}, {"fail", "own failure"}} {
		t.Run(tc.verdict, func(t *testing.T) {
			f := newHeldStart(t)
			f.verdict(t, tc.verdict)
			bindErr := f.startupBindEnded(t)
			queued, chat := f.shown()
			t.Logf("startup bind returned %v", bindErr)
			// Two listeners of one runner (the harness's and the rebind's) queue every report twice.
			if len(queued) == 0 || !strings.Contains(chat, "Extension") || !strings.Contains(chat, "error:") || !strings.Contains(queued[0].Error, tc.want) {
				t.Fatalf("a held session_start that %s: queued %+v; transcript:\n%s", tc.verdict, queued, chat)
			}
			if got := f.reports(); len(got) != 1 {
				t.Fatalf("a held session_start that %s was reported %d times, want once: %+v", tc.verdict, len(got), got)
			}
		})
	}
}
