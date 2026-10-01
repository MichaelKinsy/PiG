package extensionconformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Pi runs an extension's host calls in its own process, where a call belongs to no request: a handler that starts a call without awaiting it and returns leaves the call running, and only the call's own AbortSignal ends it (packages/coding-agent/src/core/extensions/loader.ts createExtensionRuntime has no request scope; interactive-mode.ts:2858-2940 settles a call only through its own completion, as the Node runtime does since #103). A native SDK's handler starts the same call on a goroutine. Its response must not end the call.
func TestExtensionAPIHostCallStartedByAHandlerOutlivesItsResponseGo(t *testing.T) {
	t.Parallel()
	for _, placement := range extensionAPIPlacements() {
		t.Run(placement, func(t *testing.T) {
			h := newExtensionAPIHarness(t, placement)
			dir := t.TempDir()
			type done struct {
				text string
				err  error
			}
			args, err := json.Marshal(map[string]string{"dir": dir})
			if err != nil {
				t.Fatal(err)
			}
			returned := make(chan done, 1)
			go func() {
				result, err := h.run(t.Context(), "detached_call", "call-d", string(args))
				returned <- done{result.Text(), err}
			}()
			select {
			case <-h.waitStarted:
			case <-time.After(10 * time.Second):
				t.Fatal("the goroutine's nested call never reached the host")
			}
			if err := os.WriteFile(filepath.Join(dir, "started"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-returned:
				if got.err != nil || got.text != "returned" {
					t.Fatalf("detached_call = %q, %v", got.text, got.err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("the handler never returned")
			}
			close(h.release)
			select {
			case got := <-h.waitEnded:
				if got != "released" {
					t.Fatalf("the nested call ended %q, want released: its request's response cancelled it", got)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("the nested call never ended")
			}
			var record []byte
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				if data, err := os.ReadFile(filepath.Join(dir, "out")); err == nil && len(data) > 0 {
					record = data
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if string(record) != "ok:released" {
				t.Fatalf("the goroutine's call recorded %q, want ok:released", record)
			}
		})
	}
}
