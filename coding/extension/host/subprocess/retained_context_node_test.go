package subprocess

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

type retainedContextUI struct {
	extension.UIContext
	entered chan struct{}
	calls   chan error
	values  []string
}

func (ui *retainedContextUI) Notify(string, string) { ui.entered <- struct{}{} }
func (ui *retainedContextUI) Input(ctx context.Context, _, _ string, _ extension.ExtensionUIDialogOptions) (string, error) {
	ui.calls <- ctx.Err()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	value := ui.values[0]
	ui.values = ui.values[1:]
	return value, nil
}

// Pi runner.ts:868-946 (createContext) leaves a retained Context live after a command completes. A timer retains Node's async-local origin, but normal completion must retire that wire parent.
func TestNodeRetainedRequestContextNormalAndCancelled(t *testing.T) {
	for _, tc := range []struct {
		name      string
		isolation string
		cancelled bool
	}{
		{"packed/completed", "", false},
		{"packed/cancelled", "", true},
		{"isolated/completed", "isolated", false},
		{"isolated/cancelled", "isolated", true},
	} {
		cancelled := tc.cancelled
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path, resultPath := filepath.Join(dir, "retained.mjs"), filepath.Join(dir, "result.json")
			encoded, _ := json.Marshal(resultPath)
			releasePath := filepath.Join(dir, "release")
			releaseJSON, _ := json.Marshal(releasePath)
			// A cancelled command never settles: Pi's ctx.signal is the run's, so the handler has no cancellation of its own to wait on (runner.ts:917-920).
			wait := ""
			if cancelled {
				wait = `await new Promise(() => {});`
			}
			write(t, path, `import {writeFileSync,existsSync} from "node:fs";
export default function(pi) {
 pi.registerCommand("capture", {handler: async (_, ctx) => {
   ctx.ui.notify("entered");
   setTimeout(async () => {
     while (!existsSync(`+string(releaseJSON)+`)) await new Promise(resolve => setTimeout(resolve, 1));
     try {
       const values = [];
       for (let i = 0; i < 3; i++) values.push(await ctx.ui.input("retained"));
       writeFileSync(`+string(encoded)+`, JSON.stringify({values}));
     } catch (error) { writeFileSync(`+string(encoded)+`, JSON.stringify({error:String(error)})); }
   }, 0);
   `+wait+`
 }});
 pi.registerCommand("barrier", {handler: async () => {}});
}`)
			h := NewHost(dir)
			t.Cleanup(func() { h.Shutdown("test done") })
			entered := make(chan struct{}, 1)
			calls := make(chan error, 3)
			bridge := NewUIBridge(nil)
			bridge.SetUIContext(&retainedContextUI{UIContext: extension.NoopUIContext, entered: entered, calls: calls, values: []string{"nondefault editor", "", "replacement editor"}})
			h.SetUIBridge(bridge)
			extensions, failures := h.LoadAll(t.Context(), []ExtConfig{{Name: "retained", Source: path, Enabled: true, Isolation: tc.isolation}})
			if len(failures) != 0 || len(extensions) != 1 {
				t.Fatalf("load: %v", failures)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- extensions[0].Commands["capture"].Handler(ctx, "") }()
			<-entered
			if cancelled {
				cancel()
			}
			<-done
			if cancelled {
				// The host queues the cancel before Handler returns, but the extension may not have read it. A retained call made first is concurrent with the cancel and is rejected as a pending call of the parent, not as a call by a cancelled owner. The extension reads frames in order, so the response to a later request proves it applied the cancel before the call below.
				if err := extensions[0].Commands["barrier"].Handler(t.Context(), ""); err != nil {
					t.Fatalf("barrier: %v", err)
				}
			}
			write(t, releasePath, "released after host response")
			deadline := time.After(testbudget.Wait(t))
			tick := time.NewTicker(time.Millisecond)
			defer tick.Stop()
			var result struct {
				Values []string `json:"values"`
				Error  string   `json:"error"`
			}
			for {
				data, err := os.ReadFile(resultPath)
				if err == nil && json.Unmarshal(data, &result) == nil {
					break
				}
				select {
				case <-tick.C:
				case <-deadline:
					t.Fatal("retained call never resolved or cancelled")
				}
			}
			if cancelled {
				if result.Error != "Error: host call cancelled with its parent request" || len(calls) != 0 {
					t.Fatalf("cancelled origin promoted: result=%+v, calls=%d", result, len(calls))
				}
				return
			}
			if result.Error != "" || len(result.Values) != 3 || result.Values[0] != "nondefault editor" || result.Values[1] != "" || result.Values[2] != "replacement editor" {
				t.Fatalf("result=%+v", result)
			}
			for range result.Values {
				if err := <-calls; err != nil {
					t.Errorf("retained callback used an invalid parent: %v", err)
				}
			}
		})
	}
}
