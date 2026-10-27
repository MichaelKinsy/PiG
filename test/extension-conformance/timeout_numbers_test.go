package extensionconformance

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

type dialogTimeoutUI struct {
	*recordingUI
	mu       sync.Mutex
	timeouts []any
}

func (u *dialogTimeoutUI) Select(ctx context.Context, title string, options []string, opts extension.ExtensionUIDialogOptions) (string, error) {
	u.mu.Lock()
	u.timeouts = append(u.timeouts, opts)
	u.mu.Unlock()
	return u.recordingUI.Select(ctx, title, options, opts)
}

// Pi's exec and dialog `timeout` options are JavaScript numbers (exec.ts:15,75). Fractional values
// and values beyond int32 and int64 are meaningful, so every SDK must send them intact. Each value
// differs from an integer carrier's truncation or overflow.
func TestTimeoutNumbersAcrossSDKs(t *testing.T) {
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
			if h.bridge == nil {
				t.Fatal("subprocess harness has no bridge")
			}
			var mu sync.Mutex
			var execTimeouts []float64
			h.bridge.SetHostAction("exec", func(_ context.Context, command string, _ []string, opts *extension.ExecOptions) (extension.ExecResult, error) {
				mu.Lock()
				defer mu.Unlock()
				if command != "timeout-command" || opts == nil {
					t.Errorf("exec(%q, %v)", command, opts)
					return extension.ExecResult{}, nil
				}
				execTimeouts = append(execTimeouts, opts.Timeout)
				return extension.ExecResult{Stdout: "ok"}, nil
			})
			ui := &dialogTimeoutUI{recordingUI: h.ui}
			h.bridge.SetUIContext(ui)
			command, ok := findCommand(h.runner, "timeout-probe")
			if !ok {
				t.Fatal("timeout-probe command missing")
			}
			if err := command.Handler(h.runner.DispatchContext(t.Context()), ""); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if want := []float64{0.5, 4294967296.5, 1e21}; !reflect.DeepEqual(execTimeouts, want) {
				t.Errorf("exec timeouts = %v, want %v", execTimeouts, want)
			}
			ui.mu.Lock()
			defer ui.mu.Unlock()
			if len(ui.timeouts) != 1 {
				t.Fatalf("dialog options = %v", ui.timeouts)
			}
			encoded, err := json.Marshal(ui.timeouts[0])
			if err != nil {
				t.Fatal(err)
			}
			var opts struct {
				Timeout float64 `json:"timeout"`
			}
			if err := json.Unmarshal(encoded, &opts); err != nil || opts.Timeout != 1500.5 {
				t.Errorf("dialog options = %s (%v), want timeout 1500.5", encoded, err)
			}
		})
	}
}
