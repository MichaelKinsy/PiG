package codingagent_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi 0.87.1 agent-session-runtime.ts:167-353 orders before, shutdown(old,target),
// then start(new,previous). interactive-mode.ts:1984,2020-2045 rebinds resources
// and renders BEFORE start handlers notify, so their output survives replacement.
func TestInteractiveBuiltinSessionLifecycle(t *testing.T) {
	for _, operation := range []string{"new", "resume", "fork", "clone"} {
		t.Run(operation, func(t *testing.T) {
			services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			var trace []string
			var shutdown extension.SessionShutdownEvent
			var start extension.SessionStartEvent
			runner := inproc.NewRunner([]extension.Extension{{Path: "/lifecycle-example.mjs", ResolvedPath: "/lifecycle-example.mjs", Handlers: map[string][]extension.HandlerFn{
				"session_before_switch": {func(args ...any) (any, error) {
					trace = append(trace, "before:"+args[0].(extension.SessionBeforeSwitchEvent).Reason)
					return nil, nil
				}},
				"session_before_fork": {func(args ...any) (any, error) {
					trace = append(trace, "before:"+args[0].(extension.SessionBeforeForkEvent).Position)
					return nil, nil
				}},
				"session_shutdown": {func(args ...any) (any, error) {
					shutdown = args[0].(extension.SessionShutdownEvent)
					trace = append(trace, "shutdown:"+shutdown.Reason)
					return nil, nil
				}},
				"session_start": {func(args ...any) (any, error) {
					start = args[0].(extension.SessionStartEvent)
					trace = append(trace, "start:"+start.Reason)
					ui, err := extension.FromContext(args[1].(context.Context)).UI()
					if err != nil {
						return nil, err
					}
					ui.Notify("replacement start notification", "info")
					return nil, nil
				}},
			}}}, services.CWD())
			model := &ai.Model{ID: "faux-1", Provider: &scriptedProvider{}}
			session, err := coding.NewSession(services, coding.SessionOptions{Model: model, Runner: runner, SkipBuiltinTools: true, SessionDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			userID, err := session.Inner().AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText("fork prefill")}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := session.Inner().AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "old answer"}}, StopReason: ai.StopReasonStop}}); err != nil {
				t.Fatal(err)
			}
			previous := session.Path()
			h := icodingagent.NewTestHarness(t, icodingagent.InteractiveModeOptions{CWD: services.CWD(), AgentDir: services.AgentDir(), SessionHandle: session, Model: model, ExtensionRunner: runner, SettingsManager: services.SettingsManager(), ModelRegistry: services.Registry().ModelRegistry}, nil)
			h.SeedReplacementTranscript()
			target := userID
			if operation == "resume" {
				target = previous
			}
			if err := h.ReplaceSession(operation, target); err != nil {
				t.Fatal(err)
			}
			reason, before := operation, operation
			if operation == "clone" {
				reason, before = "fork", "at"
			}
			if operation == "fork" {
				before = "before"
			}
			want := []string{"before:" + before, "shutdown:" + reason, "start:" + reason}
			if !reflect.DeepEqual(trace, want) {
				t.Fatalf("lifecycle = %v, want %v", trace, want)
			}
			if shutdown.TargetSessionFile != session.Path() || start.PreviousSessionFile != previous {
				t.Fatalf("transition files: shutdown=%+v start=%+v old=%s new=%s", shutdown, start, previous, session.Path())
			}
			if strings.Contains(h.Chat(), "outgoing transient notification") {
				t.Fatal("replacement retained the outgoing transcript")
			}
			if !strings.Contains(h.Chat(), "replacement start notification") {
				t.Fatal("replacement erased session_start notification")
			}
			if !strings.Contains(h.LoadedResources(), "[Extensions]") {
				t.Fatal("replacement dropped loaded extensions")
			}
			if operation == "fork" && h.EditorText() != "fork prefill" {
				t.Fatalf("prefill = %q", h.EditorText())
			}
		})
	}
}
