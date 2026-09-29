package coding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// Pi agent-session.ts:3563 does not await a name handler. A suspended subprocess handler must not block later events, and Close owns its cancellation and draining.
func TestSessionNameNotificationsDoNotAwaitSuspendedHandlers(t *testing.T) {
	started, second, stopped := make(chan struct{}), make(chan struct{}), make(chan struct{})
	h := newRecoveryHarness(t, harnessOptions{extension: extension.Extension{Handlers: map[string][]extension.HandlerFn{
		"session_info_changed": {func(args ...any) (any, error) {
			ctx := args[1].(context.Context)
			if args[0].(extension.SessionInfoChangedEvent).Name == "first" {
				close(started)
				invocation.Acknowledge(ctx)
				<-ctx.Done()
				close(stopped)
			} else {
				close(second)
			}
			return nil, nil
		}},
	}}})
	if err := h.session.SetSessionName("first"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	default:
		t.Fatal("SetSessionName returned before the first handler's synchronous prefix")
	}
	if err := h.session.SetSessionName("second"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-second:
	default:
		t.Fatal("SetSessionName returned before the second handler's synchronous prefix")
	}
	if err := h.session.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("Close did not drain the cancelled handler")
	}
}

// Pi agent-session.ts:3559-3563 notifies subscribers before entering the extension handler. A handler can rename again without waiting on the Session's event publisher.
func TestSessionNameNotificationReentrantPrefix(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var s *Session
		var mu sync.Mutex
		var trace []string
		record := func(value string) {
			mu.Lock()
			defer mu.Unlock()
			trace = append(trace, value)
		}
		h := newRecoveryHarness(t, harnessOptions{extension: extension.Extension{Handlers: map[string][]extension.HandlerFn{
			"session_info_changed": {func(args ...any) (any, error) {
				name := args[0].(extension.SessionInfoChangedEvent).Name
				record("extension:" + name)
				if name == "first" {
					if err := s.SetSessionName("nested"); err != nil {
						return nil, err
					}
					record("returned:nested")
				}
				return nil, nil
			}},
		}}})
		s = h.session
		s.Subscribe(func(event agent.AgentEvent) {
			if info, ok := event.(agent.SessionInfoChangedEvent); ok {
				record("subscriber:" + info.Name)
			}
		})
		done := make(chan error, 1)
		go func() {
			err := s.SetSessionName("first")
			record("returned:first")
			done <- err
		}()
		synctest.Wait()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		default:
			_ = s.Close()
			<-done
			t.Fatal("reentrant name notification deadlocked")
		}
		mu.Lock()
		want := []string{"subscriber:first", "extension:first", "subscriber:nested", "extension:nested", "returned:nested", "returned:first"}
		if !reflect.DeepEqual(trace, want) {
			t.Errorf("trace = %q, want %q", trace, want)
		}
		mu.Unlock()
		if got := s.SessionName(); got != "nested" {
			t.Errorf("name = %q, want nested", got)
		}
		// Only the wire check drains the publisher; the immediate prefix assertion above does not.
		if err := s.FlushEvents(t.Context()); err != nil {
			t.Fatal(err)
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		var names []string
		for _, event := range h.events {
			if info, ok := event.(agent.SessionInfoChangedEvent); ok {
				names = append(names, info.Name)
			}
		}
		if !reflect.DeepEqual(names, []string{"first", "nested"}) {
			t.Fatalf("wire events = %q", names)
		}
	})
}

// Pi runner.ts:995 awaits even a synchronous first handler before invoking the second. Name admission therefore waits for the first prefix, not every handler's completion.
func TestSessionNameNotificationDoesNotAwaitLaterHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		first := make(chan struct{})
		h := newRecoveryHarness(t, harnessOptions{extension: extension.Extension{Handlers: map[string][]extension.HandlerFn{
			"session_info_changed": {
				func(...any) (any, error) { close(first); return nil, nil },
				func(...any) (any, error) { <-release; return nil, nil },
			},
		}}})
		done := make(chan error, 1)
		go func() { done <- h.session.SetSessionName("first") }()
		synctest.Wait()
		select {
		case <-first:
		default:
			t.Error("first handler did not run")
		}
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		default:
			t.Error("SetSessionName awaited a later handler after the first prefix returned")
		}
		close(release)
	})
}

// Pi runner.ts:1004-1012 reports a failed name handler without rejecting setSessionName. Close drains an error delivered after a suspended handler resumes.
func TestSessionNameNotificationReportsSuspendedError(t *testing.T) {
	release := make(chan struct{})
	h := newRecoveryHarness(t, harnessOptions{extension: extension.Extension{Path: "name-error.ts", Handlers: map[string][]extension.HandlerFn{
		"session_info_changed": {func(args ...any) (any, error) {
			invocation.Acknowledge(args[1].(context.Context))
			<-release
			return nil, errors.New("name handler failed")
		}},
	}}})
	var reports []extension.ExtensionError
	h.session.currentRunner().AddErrorListener(func(report *extension.ExtensionError) { reports = append(reports, *report) })
	if err := h.session.SetSessionName("first"); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := h.session.Close(); err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || reports[0].ExtensionPath != "name-error.ts" || reports[0].Event != "session_info_changed" || reports[0].Error != "name handler failed" {
		t.Fatalf("extension errors = %+v", reports)
	}
}

// The real Node transport must admit the synchronous handler body before SetSessionName returns, including when an earlier Promise remains pending (Pi agent-session.ts:3563 and runner.ts:995).
func TestSessionNameNotificationNodePrefix(t *testing.T) {
	for _, packed := range []bool{false, true} {
		t.Run(fmt.Sprintf("packed=%t", packed), func(t *testing.T) {
			root := t.TempDir()
			trace := filepath.Join(root, "trace")
			if err := os.WriteFile(trace, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			var configs []subprocess.ExtConfig
			names := []string{"name-observer"}
			if packed {
				names = append(names, "sibling")
			}
			for _, name := range names {
				path := filepath.Join(root, name+".mjs")
				source := fmt.Sprintf(`import {appendFileSync, writeFileSync} from "node:fs";
export default function(pi) {
  writeFileSync(%q, String(process.pid));
  if (%q === "sibling") return;
  pi.on("session_info_changed", async event => {
    appendFileSync(%q, event.name + "\n");
    if (event.name === "first") await new Promise(() => {});
  });
}`, filepath.Join(root, name+".pid"), name, trace)
				if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
					t.Fatal(err)
				}
				configs = append(configs, subprocess.ExtConfig{Name: name, Source: path, Enabled: true})
			}
			host := subprocess.NewHost(t.TempDir())
			t.Cleanup(func() { host.Shutdown("name test complete") })
			var loaded []extension.Extension
			if packed {
				var errs []error
				loaded, errs = host.LoadAll(t.Context(), configs)
				if len(errs) != 0 || len(loaded) != len(configs) {
					t.Fatalf("loaded = %d, errors = %v", len(loaded), errs)
				}
			} else {
				ext, err := host.Load(t.Context(), configs[0])
				if err != nil {
					t.Fatal(err)
				}
				loaded = []extension.Extension{*ext}
			}
			if packed {
				firstPID, err := os.ReadFile(filepath.Join(root, "name-observer.pid"))
				if err != nil {
					t.Fatal(err)
				}
				siblingPID, err := os.ReadFile(filepath.Join(root, "sibling.pid"))
				if err != nil || string(firstPID) != string(siblingPID) {
					t.Fatalf("members did not share a process: %q, %q, %v", firstPID, siblingPID, err)
				}
			}
			h := newRecoveryHarness(t, harnessOptions{extension: loaded[0]})
			setNameThroughBridge(t, h.session, "first")
			if err := h.session.SetSessionName("second"); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(trace)
			if err != nil || string(data) != "first\nsecond\n" {
				t.Fatalf("immediate extension trace = %q, %v", data, err)
			}
			if err := h.session.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func BenchmarkSessionNameNotifications(b *testing.B) {
	b.Setenv("PIG_HOME", b.TempDir())
	services, err := NewServices(ServicesOptions{CWD: b.TempDir()})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		runner := inproc.NewRunner([]extension.Extension{{Handlers: map[string][]extension.HandlerFn{
			"session_info_changed": {func(...any) (any, error) { return nil, nil }},
		}}}, services.CWD())
		s, err := NewSession(services, SessionOptions{NoSession: true, Runner: runner})
		if err != nil {
			b.Fatal(err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			for range s.Events() {
			}
		}()
		for range 16 {
			if err := s.SetSessionName("hello\nworld\r\nagain"); err != nil {
				b.Fatal(err)
			}
		}
		if err := s.Close(); err != nil {
			b.Fatal(err)
		}
		<-done
	}
}

func setNameThroughBridge(t *testing.T, s *Session, name string) {
	t.Helper()
	bridge := subprocess.NewUIBridge(nil)
	bridge.SetHostAction("setSessionName", s.SetSessionName)
	args, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bridge.HandleCall("name-test", &subprocess.CallPayload{Method: "setSessionName", Args: args}); err != nil {
		t.Fatal(err)
	}
}

func TestUpstreamSessionNameEvents(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
		bridge            bool
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/3686-session-name-event.test.ts:14
		{"emits session_info_changed when AgentSession.setSessionName is called", "hello world", "hello world", false},
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/3686-session-name-event.test.ts:24
		{"emits session_info_changed when an extension calls pi.setSessionName", "from extension", "from extension", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newRecoveryHarness(t, harnessOptions{}).session
			var names []string
			s.Subscribe(func(event agent.AgentEvent) {
				if info, ok := event.(agent.SessionInfoChangedEvent); ok {
					if got := s.SessionManager().GetSessionName(); got != info.Name {
						t.Errorf("session manager name at notification = %q, event name = %q", got, info.Name)
					}
					names = append(names, info.Name)
				}
			})
			if tc.bridge {
				setNameThroughBridge(t, s, tc.input)
			} else if err := s.SetSessionName(tc.input); err != nil {
				t.Fatal(err)
			}
			if got := s.SessionName(); got != tc.want {
				t.Errorf("name = %q, want %q", got, tc.want)
			}
			if !reflect.DeepEqual(names, []string{tc.want}) {
				t.Errorf("events = %q, want [%q]", names, tc.want)
			}
		})
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/3686-session-name-event.test.ts:42
func TestUpstreamSessionNameEmitsSessionInfoChangedToExtensions(t *testing.T) {
	var mu sync.Mutex
	var names []string
	s := newRecoveryHarness(t, harnessOptions{extension: extension.Extension{Handlers: map[string][]extension.HandlerFn{
		"session_info_changed": {func(args ...any) (any, error) {
			mu.Lock()
			defer mu.Unlock()
			names = append(names, args[0].(extension.SessionInfoChangedEvent).Name)
			return nil, nil
		}},
	}}}).session
	setNameThroughBridge(t, s, "first")
	if err := s.SetSessionName("second"); err != nil {
		t.Fatal(err)
	}
	// Pi's immediate assertion follows both calls without awaiting an event barrier (3686-session-name-event.test.ts:56-62).
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(names, []string{"first", "second"}) {
		t.Fatalf("extension events = %q", names)
	}
}
