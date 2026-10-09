package codingagent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

func reloadOriginalPair(t testing.TB, handler extension.HandlerFn) (*coding.Session, *icodingagent.TestHarness, *inproc.Runner) {
	t.Helper()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := services.SettingsManager().SetTheme("dark"); err != nil {
		t.Fatal(err)
	}
	runner := inproc.NewRunner([]extension.Extension{{Path: "/session-start-notify", Handlers: map[string][]extension.HandlerFn{"session_start": {handler}}}}, services.CWD())
	model := &ai.Model{ID: "faux-1", Provider: &scriptedProvider{}}
	manager, err := coding.NewInMemorySessionManager(services.CWD())
	if err != nil {
		t.Fatal(err)
	}
	session, err := coding.NewSession(services, coding.SessionOptions{Model: model, Runner: runner, SessionManager: manager, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	h := icodingagent.NewTestHarness(t, icodingagent.InteractiveModeOptions{CWD: services.CWD(), AgentDir: services.AgentDir(), Model: model, SessionHandle: session, SettingsManager: services.SettingsManager(), Settings: services.SettingsManager().Get(), ExtensionRunner: runner, NoSkills: true, NoThemes: true, NoPromptTemplates: true}, nil)
	return session, h, runner
}

type reloadRequestKey struct{}

func TestExtensionReloadUsesUIOwnerAndRequestContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered := make(chan context.Context, 1)
		_, h, _ := reloadOriginalPair(t, func(args ...any) (any, error) {
			entered <- args[1].(context.Context)
			return nil, nil
		})
		ownerBlocked, releaseOwner, ownerReturned := make(chan struct{}), make(chan struct{}), make(chan struct{})
		go func() {
			defer close(ownerReturned)
			h.Do(func() { close(ownerBlocked); <-releaseOwner })
		}()
		<-ownerBlocked
		parent := context.WithValue(t.Context(), reloadRequestKey{}, "originating-command")
		done := make(chan error, 1)
		go func() { done <- h.ReloadFromExtension(parent) }()
		synctest.Wait()
		var request context.Context
		select {
		case request = <-entered:
			t.Error("extension reload mutated mode state before the UI owner accepted it")
		default:
		}
		close(releaseOwner)
		<-ownerReturned
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if request == nil {
			request = <-entered
		}
		if got := request.Value(reloadRequestKey{}); got != "originating-command" {
			t.Fatalf("reload lost the originating command context: %v", got)
		}
	})
}

func reloadOriginalRecord(t *testing.T, site int, title string) {
	t.Helper()
	if os.Getenv("PIG_RUNTIME_ORIGINAL_PROBE") == "" {
		return
	}
	data, err := json.Marshal([]any{"notify", site, title})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("RUNTIME_ORIGINAL " + string(data))
}

// Original 5943-session-start-notify.test.ts:386 (v1.0.0); agent-session.ts:3618 awaits the render hook before session_start.
func TestSessionStartNotifyOriginalReloadRender(t *testing.T) {
	var events []string
	var h *icodingagent.TestHarness
	session, harness, runner := reloadOriginalPair(t, func(args ...any) (any, error) {
		event := args[0].(extension.SessionStartEvent)
		if event.Reason == "reload" {
			h.Do(func() {
				if strings.Contains(h.ChatOnOwner(), "stale chat") {
					t.Error("session_start ran before the reload render hook")
				}
			})
		}
		events = append(events, "start:"+event.Reason)
		ui, err := extension.FromContext(args[1].(context.Context)).UI()
		if err != nil {
			return nil, err
		}
		ui.Notify("notify:"+event.Reason, "error")
		return nil, nil
	})
	h = harness
	if err := session.BindExtensions(t.Context(), coding.ExtensionBindings{UIContext: runner.GetUIContext(), Mode: extension.ModeTUI}); err != nil {
		t.Fatal(err)
	}
	if got := h.Chat(); !strings.Contains(got, "notify:startup") {
		t.Fatalf("startup notification missing: %q", got)
	}
	events = nil
	h.Do(func() {
		h.Notify("stale chat")
		if err := h.Reload(); err != nil {
			t.Error(err)
		}
	})
	if len(events) != 1 || events[0] != "start:reload" {
		t.Fatalf("reload starts = %v", events)
	}
	if got := h.Chat(); strings.Contains(got, "stale chat") || strings.Contains(got, "notify:startup") || !strings.Contains(got, "notify:reload") {
		t.Fatalf("reload rebuilt after notify or retained stale chat: %q", got)
	}
	reloadOriginalRecord(t, 386, "runs the reload render hook before reload session_start handlers can notify")
}

// Original sites419/443; independent completion barriers preserve the original pending-reload observation.
func TestSessionStartNotifyOriginalReloadDisplayAndFocus(t *testing.T) {
	for _, site := range []int{419, 443} {
		t.Run(fmt.Sprint(site), func(t *testing.T) {
			waiting, finish := make(chan struct{}), make(chan struct{})
			session, h, _ := reloadOriginalPair(t, func(...any) (any, error) {
				close(waiting)
				<-finish
				return nil, nil
			})
			if site == 419 {
				if _, err := session.Inner().AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Provider: "faux", ModelID: "faux-1", Content: []ai.AssistantContentBlock{ai.ThinkingContent{Thinking: "private-plan-marker"}, ai.TextContent{Text: "visible-answer-marker"}}}}); err != nil {
					t.Fatal(err)
				}
				h.Do(func() {
					if err := h.SetHideThinkingForReload(true); err != nil {
						t.Error(err)
					}
				})
			}
			h.Do(func() { h.Notify("stale chat") })
			done := make(chan struct{})
			go func() {
				defer close(done)
				h.Do(func() {
					if err := h.Reload(); err != nil {
						t.Error(err)
					}
				})
			}()
			<-waiting
			// The handler is suspended after all pre-start owner mutations. No event producer or input writer is active at this barrier.
			focused, hidden := h.ReloadUIState()
			chat := h.ChatOnOwner()
			close(finish)
			<-done
			if strings.Contains(chat, "stale chat") {
				t.Errorf("chat was not restored before session_start: %q", chat)
			}
			if site == 419 && (!hidden || strings.Contains(chat, "private-plan-marker") || !strings.Contains(chat, "visible-answer-marker")) {
				t.Errorf("hideThinkingBlock was not refreshed before rebuilding chat: hidden=%v chat=%q", hidden, chat)
			}
			if site == 443 && focused {
				t.Error("editor retained focus while reload was pending")
			}
			h.Do(func() {
				if focused, _ := h.ReloadUIState(); !focused {
					t.Error("editor focus was not restored after reload")
				}
			})
			title := "refreshes hideThinkingBlock before rebuilding chat during reload"
			if site == 443 {
				title = "keeps the reload blocker focused until async reload completes"
			}
			reloadOriginalRecord(t, site, title)
		})
	}
}
