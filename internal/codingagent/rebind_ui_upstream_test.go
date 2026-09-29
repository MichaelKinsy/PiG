package codingagent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

type rebindFixture struct {
	runtime *coding.Runtime
	h       *icodingagent.TestHarness
	runners map[*coding.Session]*inproc.Runner
	events  map[*coding.Session]*extension.SessionStartEvent
}

func newRebindFixture(t testing.TB, handler func(*coding.Session, ...any) (any, error), onEvent func(*icodingagent.TestHarness, agent.AgentEvent), mutate ...func(*icodingagent.InteractiveOptions)) *rebindFixture {
	t.Helper()
	f := &rebindFixture{runners: make(map[*coding.Session]*inproc.Runner), events: make(map[*coding.Session]*extension.SessionStartEvent)}
	model := &ai.Model{ID: "faux-1", Provider: &scriptedProvider{replies: []scriptedReply{reply("assistant from start", ai.Usage{})}}, Capabilities: ai.ModelCapabilities{ContextWindow: 128000}}
	cwd, dir := t.TempDir(), t.TempDir()
	manager, err := coding.NewInMemorySessionManager(cwd)
	if err != nil {
		t.Fatal(err)
	}
	factory := func(_ context.Context, options coding.CreateAgentSessionRuntimeOptions) (coding.CreateAgentSessionRuntimeResult, error) {
		services, err := coding.NewServices(coding.ServicesOptions{CWD: options.CWD, AgentDir: options.AgentDir})
		if err != nil {
			return coding.CreateAgentSessionRuntimeResult{}, err
		}
		// Pi prompt() requires configured auth before dispatching the startup user prompt.
		if err := services.Auth().Set("faux", ai.Credential{Type: ai.CredentialAPIKey, Key: "faux-key"}); err != nil {
			return coding.CreateAgentSessionRuntimeResult{}, err
		}
		var session *coding.Session
		runner := inproc.NewRunner([]extension.Extension{{Path: "/rebind-original", Handlers: map[string][]extension.HandlerFn{"session_start": {func(args ...any) (any, error) { return handler(session, args...) }}}}}, options.CWD)
		session, err = coding.NewSession(services, coding.SessionOptions{SessionManager: options.SessionManager, Model: model, Runner: runner, SkipBuiltinTools: true})
		if err != nil {
			return coding.CreateAgentSessionRuntimeResult{}, err
		}
		if f.runtime != nil {
			delete(f.runners, f.runtime.Session())
			delete(f.events, f.runtime.Session())
		}
		f.runners[session], f.events[session] = runner, options.SessionStartEvent
		return coding.CreateAgentSessionRuntimeResult{Session: session, Services: services}, nil
	}
	f.runtime, err = coding.CreateAgentSessionRuntime(t.Context(), factory, coding.CreateAgentSessionRuntimeOptions{CWD: cwd, AgentDir: dir, SessionManager: manager})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.runtime.Close() })
	opts := icodingagent.InteractiveOptions{CWD: cwd, AgentDir: dir, SessionHandle: f.runtime.Session(), Model: model, SettingsManager: f.runtime.Services().SettingsManager(), ModelRegistry: f.runtime.Services().Registry().ModelRegistry, NoThemes: true, NoSkills: true, NoPromptTemplates: true}
	for _, apply := range mutate {
		apply(&opts)
	}
	f.h = icodingagent.NewTestHarness(t, opts, onEvent)
	f.runtime.SetRebindSession(func(ctx context.Context, session *coding.Session) error {
		f.h.Do(func() {
			f.h.SetRebindResources(session.Services().SettingsManager(), session.Services().Registry().ModelRegistry)
		})
		return f.h.RebindSession(ctx, session, f.runners[session], f.events[session], true)
	})
	return f
}

func rebindOriginalRecord(t *testing.T, family string, site int, title string) {
	t.Helper()
	if os.Getenv("PIG_RUNTIME_ORIGINAL_PROBE") == "" {
		return
	}
	data, err := json.Marshal([]any{family, site, title})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("RUNTIME_ORIGINAL " + string(data))
}

// Original #5943 sites276/313/368, with actual Runtime replacement rather than a leaf move or a successful Session facade.
func TestSessionStartNotifyOriginalReplacement(t *testing.T) {
	for _, site := range []int{276, 313, 368} {
		t.Run(fmt.Sprint(site), func(t *testing.T) {
			icodingagent.ObserveRebindTitles(t, func(string) {})
			var f *rebindFixture
			var received []string
			assistantDone := make(chan struct{})
			f = newRebindFixture(t, func(session *coding.Session, args ...any) (any, error) {
				f.h.Do(func() {
					if !f.h.RebindOwnsSession() {
						t.Error("session_start ran before applying the replacement runtime")
					}
					if strings.Contains(f.h.ChatOnOwner(), "old session chat") {
						t.Error("session_start ran before replacement rendering")
					}
					if !f.h.RebindSubscribed() {
						t.Error("session_start ran before the replacement event subscription")
					}
				})
				if t.Failed() {
					return nil, fmt.Errorf("replacement pre-bind invariant failed")
				}
				switch site {
				case 276:
					ui, err := extension.FromContext(args[1].(context.Context)).UI()
					if err != nil {
						return nil, err
					}
					ui.Notify("Hello Error", "error")
				case 313:
					return nil, session.SendMessage(extension.CustomMessageRef{CustomType: "session-start", Content: "custom from start", Display: true}, nil)
				case 368:
					return nil, extension.FromContext(args[1].(context.Context)).SendUserMessage("user from start", nil)
				}
				return nil, nil
			}, func(_ *icodingagent.TestHarness, event agent.AgentEvent) {
				var message agent.AgentMessage
				var kind string
				switch e := event.(type) {
				case agent.MessageStartEvent:
					kind, message = "message_start", e.Message
				case agent.MessageEndEvent:
					kind, message = "message_end", e.Message
				default:
					return
				}
				wire, _ := json.Marshal(message)
				received = append(received, kind+":"+message.Role()+":"+string(wire))
				if kind == "message_end" && message.Assistant != nil {
					close(assistantDone)
				}
			})
			previous := f.runtime.Session()
			f.h.Do(func() { f.h.Notify("old session chat") })
			result, err := f.runtime.NewSession(t.Context(), nil)
			if err != nil || result.Cancelled || f.runtime.Session() == previous {
				t.Fatalf("replacement = %+v, %v; same Session=%v", result, err, f.runtime.Session() == previous)
			}
			if t.Failed() {
				t.FailNow()
			}
			if site == 368 {
				select {
				case <-assistantDone:
				case <-time.After(testbudget.Wait(t)):
					t.Fatalf("startup user message did not finish: %s", f.h.Chat())
				}
				if err := f.runtime.Session().WaitForIdle(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.runtime.Session().FlushEvents(t.Context()); err != nil {
				t.Fatal(err)
			}
			chat := f.h.Chat()
			title := "renders replacement session state before session_start handlers can notify"
			switch site {
			case 276:
				if !strings.Contains(chat, "Hello Error") {
					t.Fatalf("replacement lost session_start notification: %q", chat)
				}
			case 313:
				title = "subscribes before replacement session_start handlers send messages"
				if len(received) != 2 || !strings.HasPrefix(received[0], "message_start:custom:") || !strings.HasPrefix(received[1], "message_end:custom:") || !strings.Contains(received[0], "custom from start") || !strings.Contains(received[1], "custom from start") {
					t.Fatalf("custom event order = %v", received)
				}
			case 368:
				title = "subscribes before replacement session_start handlers send user messages"
				for _, want := range []struct{ kind, text string }{{"message_start:user:", "user from start"}, {"message_end:user:", "user from start"}, {"message_end:assistant:", "assistant from start"}} {
					if !slices.ContainsFunc(received, func(got string) bool { return strings.HasPrefix(got, want.kind) && strings.Contains(got, want.text) }) {
						t.Errorf("missing %s%s in %v", want.kind, want.text, received)
					}
				}
			}
			rebindOriginalRecord(t, "notify", site, title)
		})
	}
}

type countedRebindSession struct {
	icodingagent.InteractiveSessionHandle
	subscriptions *atomic.Int32
}

func (s *countedRebindSession) Events() <-chan agent.AgentEvent {
	s.subscriptions.Add(1)
	return s.InteractiveSessionHandle.Events()
}

// Original startup-session-rebind-duplicate-subscription.test.ts:23. Each bind completion is released independently; count the actual subscription boundary as well as the title updates.
func TestStartupRebindOriginalStaleCompletion(t *testing.T) {
	var titles, binds, subscriptions atomic.Int32
	icodingagent.ObserveRebindTitles(t, func(string) { titles.Add(1) })
	startupEntered, replacementEntered := make(chan struct{}), make(chan struct{})
	startupRelease, replacementRelease := make(chan struct{}), make(chan struct{})
	releaseStartup := sync.OnceFunc(func() { close(startupRelease) })
	releaseReplacement := sync.OnceFunc(func() { close(replacementRelease) })
	f := newRebindFixture(t, func(_ *coding.Session, args ...any) (any, error) {
		binds.Add(1)
		event := args[0].(extension.SessionStartEvent)
		if event.Reason == "startup" {
			close(startupEntered)
			<-startupRelease
		} else {
			close(replacementEntered)
			<-replacementRelease
		}
		return nil, nil
	}, nil)
	t.Cleanup(func() { releaseStartup(); releaseReplacement() })
	startup := f.runtime.Session()
	startupHandle := &countedRebindSession{InteractiveSessionHandle: startup, subscriptions: &subscriptions}
	f.runtime.SetRebindSession(func(ctx context.Context, session *coding.Session) error {
		f.h.Do(func() {
			f.h.SetRebindResources(session.Services().SettingsManager(), session.Services().Registry().ModelRegistry)
		})
		handle := &countedRebindSession{InteractiveSessionHandle: session, subscriptions: &subscriptions}
		return f.h.RebindSession(ctx, handle, f.runners[session], f.events[session], true)
	})
	startupDone := make(chan error, 1)
	go func() { startupDone <- f.h.RebindSession(t.Context(), startupHandle, f.runners[startup], nil, false) }()
	<-startupEntered
	if got := binds.Load(); got != 1 {
		t.Errorf("startup binds=%d, want1", got)
	}
	replacementDone := make(chan error, 1)
	go func() { _, err := f.runtime.NewSession(t.Context(), nil); replacementDone <- err }()
	<-replacementEntered
	if got := binds.Load(); got != 2 {
		t.Errorf("overlapping binds=%d, want2", got)
	}
	if got := subscriptions.Load(); got != 1 {
		t.Errorf("subscriptions before replacement bind completes=%d, want1", got)
	}
	f.h.Do(func() {
		if !f.h.RebindSubscribedTo(f.runtime.Session().Events()) {
			t.Error("replacement did not select its event channel before binding")
		}
	})
	releaseStartup()
	if err := <-startupDone; err != nil {
		t.Fatal(err)
	}
	if got := subscriptions.Load(); got != 1 {
		t.Errorf("subscriptions after stale startup completion=%d, want1", got)
	}
	f.h.Do(func() {
		if !f.h.RebindSubscribedTo(f.runtime.Session().Events()) {
			t.Error("stale startup replaced the live event channel")
		}
	})
	if got := titles.Load(); got != 0 {
		t.Errorf("stale startup updated terminal title %d times", got)
	}
	releaseReplacement()
	if err := <-replacementDone; err != nil {
		t.Fatal(err)
	}
	if got := subscriptions.Load(); got != 1 {
		t.Errorf("subscriptions after replacement completion=%d, want1", got)
	}
	if got := binds.Load(); got != 2 {
		t.Errorf("final bind count=%d, want2", got)
	}
	if got := titles.Load(); got != 1 {
		t.Errorf("replacement title updates = %d, want1", got)
	}
	rebindOriginalRecord(t, "rebind", 23, "does not subscribe from the stale startup rebind")
}
