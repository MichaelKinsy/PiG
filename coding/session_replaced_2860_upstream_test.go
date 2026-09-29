package coding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func recordReplacedOriginal(t *testing.T, site int, title string) {
	t.Helper()
	if os.Getenv("PIG_REPLACED_2860_PROBE") == "" {
		return
	}
	t.Cleanup(func() {
		if t.Failed() {
			return
		}
		data, err := json.Marshal([]any{"replaced", site, title})
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("RUNTIME_ORIGINAL " + string(data))
	})
}

func bindReplacementCommands(t *testing.T, runtime *Runtime) {
	t.Helper()
	actions := func(session *Session) extension.CommandActions {
		bound := session.ExtensionCommandActions()
		bound.NewSessionContext = runtime.NewSession
		bound.ForkContext = func(ctx context.Context, id string, options *extension.ForkOptions) (extension.CancelledResult, error) {
			result, err := runtime.Fork(ctx, id, options)
			return extension.CancelledResult{Cancelled: result.Cancelled}, err
		}
		bound.SwitchSessionContext = func(ctx context.Context, path string, options *extension.SwitchSessionOptions) (extension.CancelledResult, error) {
			return runtime.SwitchSession(ctx, path, options)
		}
		return bound
	}
	runtime.Session().currentRunner().BindCommandActions(actions(runtime.Session()))
	runtime.SetRebindSession(func(ctx context.Context, session *Session) error {
		return session.BindExtensions(ctx, ExtensionBindings{CommandContextActions: actions(session)})
	})
}

// The native-system filter is part of #2860 itself; it is not used by the branching cases.
func replacedConversation(session *Session) []string {
	var rows []string
	for _, message := range session.Messages() {
		if message.System != nil {
			continue
		}
		rows = append(rows, message.Role()+":"+messageText(message))
	}
	return rows
}

// packages/coding-agent/test/suite/regressions/2860-replaced-session-context.test.ts:147.
func TestReplacedSession2860New(t *testing.T) {
	recordReplacedOriginal(t, 147, "rebinds before withSession, targets the replacement session, and invalidates stale pi/ctx")
	var events []string
	var oldFile, replacementFile string
	var staleContext, staleAPI bool
	instance := 0
	h := newRuntimeTestHarness(t, runtimeTestOptions{extension: func() extension.Extension {
		instance++
		current := instance
		return extension.Extension{Handlers: map[string][]extension.HandlerFn{
			"session_start": {func(...any) (any, error) { events = append(events, fmt.Sprintf("start:%d", current)); return nil, nil }},
			"session_shutdown": {func(...any) (any, error) {
				events = append(events, fmt.Sprintf("shutdown:%d", current))
				return nil, nil
			}},
		}, Commands: map[string]extension.RegisteredCommand{"repro": {Name: "repro", Description: "repro", Handler: func(ctx context.Context, _ string) error {
			oldContext := extension.CommandContextFromContext(ctx)
			oldAPI := oldContext.Context
			manager, err := oldContext.SessionManager()
			if err != nil {
				return err
			}
			oldFile = manager.(*SessionManager).Path()
			_, err = oldContext.NewSession(&extension.NewSessionOptions{ParentSession: oldFile, WithSession: func(replaced *extension.ReplacedSessionContext) error {
				events = append(events, fmt.Sprintf("with:%d", current))
				manager, err := replaced.SessionManager()
				if err != nil {
					return err
				}
				replacementFile = manager.(*SessionManager).Path()
				_, err = oldContext.SessionManager()
				staleContext = errors.Is(err, extension.ErrStaleContext)
				staleAPI = errors.Is(oldAPI.SendUserMessage("stale message", nil), extension.ErrStaleContext)
				return replaced.SendUserMessage("Hello from the new session!", nil)
			}})
			return err
		}}}}
	}})
	h.provider.responses = []scriptedResponse{fauxReply("hello reply", ai.StopReasonStop, 0)}
	bindReplacementCommands(t, h.runtime)
	if !reflect.DeepEqual(events, []string{"start:1"}) {
		t.Fatalf("startup events=%v", events)
	}
	runtimePrompt(t, h.runtime, "/repro")
	if !reflect.DeepEqual(events, []string{"start:1", "shutdown:1", "start:2", "with:1"}) {
		t.Fatalf("events=%v", events)
	}
	if replacementFile == "" || replacementFile == oldFile {
		t.Fatalf("replacement=%q old=%q", replacementFile, oldFile)
	}
	if !staleContext || !staleAPI {
		t.Fatalf("stale ctx=%v pi=%v", staleContext, staleAPI)
	}
	want := []string{"user:Hello from the new session!", "assistant:hello reply"}
	if got := replacedConversation(h.runtime.Session()); !reflect.DeepEqual(got, want) {
		t.Fatalf("conversation=%v want=%v", got, want)
	}
}

// packages/coding-agent/test/suite/regressions/2860-replaced-session-context.test.ts:211.
func TestReplacedSession2860Fork(t *testing.T) {
	recordReplacedOriginal(t, 211, "supports withSession for fork")
	h := newRuntimeTestHarness(t, runtimeTestOptions{extension: func() extension.Extension {
		return extension.Extension{Commands: map[string]extension.RegisteredCommand{"fork-it": {Name: "fork-it", Description: "fork-it", Handler: func(ctx context.Context, _ string) error {
			command := extension.CommandContextFromContext(ctx)
			manager, err := command.SessionManager()
			if err != nil {
				return err
			}
			leaf := manager.(*SessionManager).LeafID()
			if leaf == nil {
				return errors.New("Missing leaf id")
			}
			_, err = command.Fork(*leaf, &extension.ForkOptions{Position: "at", WithSession: func(replaced *extension.ReplacedSessionContext) error {
				return replaced.SendUserMessage("fork callback message", nil)
			}})
			return err
		}}}}
	}})
	h.provider.responses = []scriptedResponse{fauxReply("seed reply", ai.StopReasonStop, 0), fauxReply("fork reply", ai.StopReasonStop, 0)}
	bindReplacementCommands(t, h.runtime)
	runtimePrompt(t, h.runtime, "seed")
	runtimePrompt(t, h.runtime, "/fork-it")
	want := []string{"user:seed", "assistant:seed reply", "user:fork callback message", "assistant:fork reply"}
	if got := replacedConversation(h.runtime.Session()); !reflect.DeepEqual(got, want) {
		t.Fatalf("conversation=%v want=%v", got, want)
	}
}

// packages/coding-agent/test/suite/regressions/2860-replaced-session-context.test.ts:243.
func TestReplacedSession2860Switch(t *testing.T) {
	recordReplacedOriginal(t, 243, "supports withSession for switchSession")
	var target string
	h := newRuntimeTestHarness(t, runtimeTestOptions{extension: func() extension.Extension {
		return extension.Extension{Commands: map[string]extension.RegisteredCommand{"switch-it": {Name: "switch-it", Description: "switch-it", Handler: func(ctx context.Context, _ string) error {
			_, err := extension.CommandContextFromContext(ctx).SwitchSession(target, &extension.SwitchSessionOptions{WithSession: func(replaced *extension.ReplacedSessionContext) error {
				return replaced.SendUserMessage("switch callback message", nil)
			}})
			return err
		}}}}
	}})
	h.provider.responses = []scriptedResponse{fauxReply("root reply", ai.StopReasonStop, 0), fauxReply("target reply", ai.StopReasonStop, 0), fauxReply("switch reply", ai.StopReasonStop, 0)}
	bindReplacementCommands(t, h.runtime)
	runtimePrompt(t, h.runtime, "root")
	original := h.runtime.Session().Path()
	if result, err := h.runtime.NewSession(t.Context(), nil); err != nil || result.Cancelled {
		t.Fatalf("new=%v err=%v", result, err)
	}
	runtimePrompt(t, h.runtime, "target")
	target = h.runtime.Session().Path()
	if _, err := h.runtime.SwitchSession(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	runtimePrompt(t, h.runtime, "/switch-it")
	if h.runtime.Session().Path() != target {
		t.Fatalf("path=%s want=%s", h.runtime.Session().Path(), target)
	}
	want := []string{"user:target", "assistant:target reply", "user:switch callback message", "assistant:switch reply"}
	if got := replacedConversation(h.runtime.Session()); !reflect.DeepEqual(got, want) {
		t.Fatalf("conversation=%v want=%v", got, want)
	}
}
