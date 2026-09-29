package coding

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// A mode drains its own work after the before hooks approved a replacement and before the outgoing Session aborts and shuts down (rpc-mode.ts awaits session.abort() there). A cancelled replacement never drains. session_start belongs to the mode's rebind, which this test does not install.
func TestRuntimeDrainRunsBetweenBeforeHookAndShutdown(t *testing.T) {
	var order []string
	cancel := ""
	h := newRuntimeTestHarness(t, runtimeTestOptions{extension: func() extension.Extension {
		return extension.Extension{Handlers: map[string][]extension.HandlerFn{
			"session_before_switch": {func(args ...any) (any, error) {
				order = append(order, "before")
				if event := args[0].(extension.SessionBeforeSwitchEvent); event.Reason == cancel {
					return extension.SessionBeforeSwitchResult{Cancel: true}, nil
				}
				return nil, nil
			}},
			"session_shutdown": {func(...any) (any, error) { order = append(order, "shutdown"); return nil, nil }},
			"session_start":    {func(...any) (any, error) { order = append(order, "start"); return nil, nil }},
		}}
	}})
	order = nil
	h.runtime.SetBeforeSessionReplacement(func(context.Context) error { order = append(order, "drain"); return nil })
	cancel = "new"
	if result, err := h.runtime.NewSession(t.Context(), nil); err != nil || !result.Cancelled {
		t.Fatalf("cancelled new = %v, %v", result, err)
	}
	if !reflect.DeepEqual(order, []string{"before"}) {
		t.Fatalf("a cancelled replacement ran %v, want only the before hook", order)
	}
	order, cancel = nil, ""
	if _, err := h.runtime.NewSession(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if want := []string{"before", "drain", "shutdown"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("replacement order = %v, want %v", order, want)
	}
	h.runtime.SetBeforeSessionReplacement(func(context.Context) error { return errors.New("drain failed") })
	previous := h.runtime.Session()
	if _, err := h.runtime.NewSession(t.Context(), nil); err == nil || h.runtime.Session() != previous {
		t.Fatalf("a failed drain must stop the replacement and keep the Session; err=%v", err)
	}
}

// EmitQuitShutdown lets a mode emit session_shutdown before it tears its terminal down, and Close then does not repeat it (agent-session-runtime.ts dispose emits it once).
func TestRuntimeEmitsQuitShutdownOnce(t *testing.T) {
	var reasons []string
	h := newRuntimeTestHarness(t, runtimeTestOptions{extension: func() extension.Extension {
		return extension.Extension{Handlers: map[string][]extension.HandlerFn{
			"session_shutdown": {func(args ...any) (any, error) {
				reasons = append(reasons, args[0].(extension.SessionShutdownEvent).Reason)
				return nil, nil
			}},
		}}
	}})
	h.runtime.EmitQuitShutdown()
	h.runtime.EmitQuitShutdown()
	if err := h.runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reasons, []string{"quit"}) {
		t.Fatalf("shutdown reasons = %v, want one quit", reasons)
	}
}

// Pi's switchSession cwdOverride (agent-session-runtime.ts) replaces a stored working directory that no longer exists. Without it the switch fails before the outgoing Session changes.
func TestRuntimeSwitchSessionHonorsCWDOverride(t *testing.T) {
	h := newRuntimeTestHarness(t, runtimeTestOptions{})
	runtimePrompt(t, h.runtime, "hello")
	original := h.runtime.Session()
	missing := filepath.Join(t.TempDir(), "gone")
	sessionFile := filepath.Join(t.TempDir(), "elsewhere.jsonl")
	log := `{"type":"session","version":3,"id":"elsewhere","timestamp":"2026-01-01T00:00:00.000Z","cwd":` + strconv.Quote(missing) + `}` + "\n" +
		`{"type":"message","id":"u1","parentId":null,"timestamp":"2026-01-01T00:00:00.000Z","message":{"role":"user","content":"hi","timestamp":1}}` + "\n"
	if err := os.WriteFile(sessionFile, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.runtime.SwitchSession(t.Context(), sessionFile); err == nil {
		t.Fatal("switching to a Session whose directory is gone succeeded without an override")
	} else if _, ok := errors.AsType[*icodingagent.MissingSessionCwdError](err); !ok || h.runtime.Session() != original {
		t.Fatalf("switch without override = %v, session kept %v", err, h.runtime.Session() == original)
	}
	fallback := t.TempDir()
	if result, err := h.runtime.SwitchSessionWithCWD(t.Context(), sessionFile, fallback); err != nil || result.Cancelled {
		t.Fatalf("switch with override = %v, %v", result, err)
	}
	if got := h.runtime.CWD(); got != fallback {
		t.Fatalf("Runtime cwd after override = %q, want %q", got, fallback)
	}
}
