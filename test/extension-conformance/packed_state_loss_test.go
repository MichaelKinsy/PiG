package extensionconformance

import (
	"context"
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// D274: when one packed member's own thread ends the shared process, the other members start again in a new process with their in-memory state lost, and a call that was in flight
// when the process ended, the ended member's and the sibling's, returns an error and is not replayed. (The owner approved this loss on 2026-11-07; the sibling's factory runs again.)
// Go and Lisp are the SDKs whose threads cannot be recovered by the SDK, so both run; a Lisp row needs sbcl.
func TestPackedProcessEndLosesTheSiblingsStateAndInFlightCalls(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping crash blame conformance in short mode (builds runner images)")
	}
	for _, language := range []string{"go", "lisp"} {
		t.Run(language, func(t *testing.T) {
			if language == "lisp" {
				if _, err := exec.LookPath("sbcl"); err != nil {
					t.Skip("skipped: sbcl")
				}
			}
			t.Parallel()
			h := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			t.Cleanup(func() { h.Shutdown("test done") })
			messages := make(chan string, 64)
			bridge := subprocess.NewUIBridge(func() {})
			bridge.SetUIContext(newRecordingUI(new([]string), new([]string)))
			bridge.SetNotifyFunc(func(message, _ string) { messages <- message })
			h.SetUIBridge(bridge)
			configs := []subprocess.ExtConfig{crashFactory(t, language, "culprit", "shared-ok"), crashFactory(t, language, "sibling", "shared-ok")}
			h.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return configs, nil })
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
			defer cancel()
			loaded, errs := h.LoadAll(ctx, configs)
			if len(errs) != 0 || len(loaded) != 2 {
				t.Fatalf("loaded %d, errors %v", len(loaded), errs)
			}
			handle := func(list []extension.Extension, name, command string) func(context.Context) error {
				for _, ext := range list {
					if ext.Name == name {
						return func(ctx context.Context) error { return ext.Commands[command].Handler(ctx, "") }
					}
				}
				return func(context.Context) error { return fmt.Errorf("%s is not loaded", name) }
			}
			expect := func(want string) {
				t.Helper()
				select {
				case got := <-messages:
					if got != want {
						t.Fatalf("notification %q, want %q", got, want)
					}
				case <-time.After(time.Minute):
					t.Fatalf("no notification %q", want)
				}
			}
			if len(h.Extensions()) != 2 {
				t.Fatalf("%d extensions are loaded, want 2", len(h.Extensions()))
			}
			for i := 1; i <= 2; i++ {
				if err := handle(loaded, "sibling", "count")(ctx); err != nil {
					t.Fatal(err)
				}
				expect(fmt.Sprintf("count-sibling-%d", i))
			}

			// A call of each member is in flight when the culprit's thread ends the shared process.
			siblingCall, culpritCall := make(chan error, 1), make(chan error, 1)
			go func() { siblingCall <- handle(loaded, "sibling", "hold")(ctx) }()
			expect("held-sibling")
			go func() { culpritCall <- handle(loaded, "culprit", "crashhold")(ctx) }()
			expect("held-culprit")
			for name, call := range map[string]chan error{"the ended member's call": culpritCall, "the sibling's call": siblingCall} {
				select {
				case err := <-call:
					if err == nil {
						t.Errorf("%s in flight when the process ended returned no error", name)
					} else {
						t.Logf("%s returned: %v", name, err)
					}
				case <-time.After(2 * time.Minute):
					t.Fatalf("%s did not return after the process ended", name)
				}
			}

			// The sibling comes back in a new process: its factory ran again, so its count starts over.
			deadline := time.Now().Add(2 * time.Minute)
			for {
				if err := handle(h.Extensions(), "sibling", "count")(ctx); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("the sibling did not answer after the shared process ended")
				}
				time.Sleep(200 * time.Millisecond)
			}
			expect("count-sibling-1")
			// Nothing in flight was replayed: the next notification is the answer to the next call.
			if err := handle(h.Extensions(), "sibling", "ping")(ctx); err != nil {
				t.Fatal(err)
			}
			expect("pong-sibling")
		})
	}
}
