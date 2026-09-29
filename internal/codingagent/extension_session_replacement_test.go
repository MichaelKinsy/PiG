package codingagent

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

type replacementRecordingHandle struct {
	*recordingCompactHandle
	calls []string
}

func (h *replacementRecordingHandle) ExtensionCommandActions() extension.CommandActions {
	return extension.CommandActions{
		NewSessionContext: func(ctx context.Context, opts *extension.NewSessionOptions) (extension.CancelledResult, error) {
			extension.CallInitiated(ctx)
			h.calls = append(h.calls, "new:"+opts.ParentSession)
			return extension.CancelledResult{Cancelled: true}, nil
		},
		ForkContext: func(ctx context.Context, id string, opts *extension.ForkOptions) (extension.CancelledResult, error) {
			extension.CallInitiated(ctx)
			h.calls = append(h.calls, "fork:"+id+":"+opts.Position)
			return extension.CancelledResult{Cancelled: true}, nil
		},
		SwitchSessionContext: func(ctx context.Context, path string, _ *extension.SwitchSessionOptions) (extension.CancelledResult, error) {
			extension.CallInitiated(ctx)
			h.calls = append(h.calls, "resume:"+path)
			return extension.CancelledResult{Cancelled: true}, nil
		},
	}
}

// interactive-mode.ts:1921-1940 and 5592-5625 make replacement errors fatal,
// unlike ordinary command failures. The crash path must execute on the UI owner.
func TestInteractiveExtensionReplacementFailureUsesFatalPath(t *testing.T) {
	for reason, prefix := range map[string]string{"new": "Failed to create session", "fork": "Failed to fork session", "resume": "Failed to resume session"} {
		t.Run(reason, func(t *testing.T) {
			m, _ := newExtensionDialogProbe(t)
			m.opts.AgentDir = t.TempDir()
			done := make(chan error, 1)
			go func() {
				_, err := m.finishExtensionSessionUI(t.Context(), extension.CancelledResult{}, errors.New("disk failure"), reason, "")
				done <- err
			}()
			select {
			case err := <-done:
				t.Fatalf("replacement error returned without owner-loop crash handling: %v", err)
			case task := <-m.uiTaskCh:
				if m.fatalRuntime.Load() {
					t.Fatal("crash state changed off the owner loop")
				}
				task()
			}
			if err := <-done; !errors.Is(err, ErrInteractiveCrashed) {
				t.Fatalf("replacement error = %v", err)
			}
			if !m.fatalRuntime.Load() || !m.requestExit.Load() {
				t.Fatal("fatal replacement did not request exit")
			}
			if rendered := strings.Join(m.chatContainer.Render(200), "\n"); !strings.Contains(rendered, prefix+": disk failure") {
				t.Fatalf("missing fatal prefix in %q", rendered)
			}
		})
	}
}

// interactive-mode.ts:1921-1963 forwards options and cancelled results to the
// runtime host. Both inproc and subprocess callbacks must reach that owner.
func TestInteractiveExtensionReplacementBindings(t *testing.T) {
	handle := &replacementRecordingHandle{recordingCompactHandle: &recordingCompactHandle{}}
	bridge := subprocess.NewUIBridge(func() {})
	m := &InteractiveMode{opts: InteractiveOptions{SessionHandle: handle, SubprocessUIBridge: bridge}, newRunner: inproc.NewRunner(nil, t.TempDir())}
	m.wireInprocContextActions()
	command := m.newRunner.CreateCommandContext()
	result, err := command.NewSession(&extension.NewSessionOptions{ParentSession: "source"})
	if err != nil || !result.Cancelled {
		t.Fatalf("new = %+v, %v", result, err)
	}
	result, err = command.Fork("entry", &extension.ForkOptions{Position: "at"})
	if err != nil || !result.Cancelled {
		t.Fatalf("fork = %+v, %v", result, err)
	}
	result, err = command.SwitchSession("target", nil)
	if err != nil || !result.Cancelled {
		t.Fatalf("resume = %+v, %v", result, err)
	}
	for _, call := range []struct{ method, args string }{
		{"newSession", `{"parentSession":"source"}`},
		{"fork", `{"entryId":"entry","position":"at"}`},
		{"switchSession", `{"sessionPath":"target"}`},
	} {
		response, err := bridge.HandleCall("test", &subprocess.CallPayload{Method: call.method, Args: json.RawMessage(call.args)})
		if err != nil || response.Error != nil || string(response.Result) != `{"cancelled":true}` {
			t.Fatalf("%s = %+v, %v", call.method, response, err)
		}
	}
	want := []string{"new:source", "fork:entry:at", "resume:target", "new:source", "fork:entry:at", "resume:target"}
	if !slices.Equal(handle.calls, want) {
		t.Fatalf("calls = %v, want %v", handle.calls, want)
	}
}
