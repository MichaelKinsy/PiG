package extensionconformance

import (
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi's editor starts a request in the Promise jobs after the terminal read (editor.ts startAutocompleteRequest): a provider chain that settles
// there has opened its menu before the next read, one that awaits real I/O has not. A synchronous provider settles when it returns, even if it blocks
// on I/O, as it holds Pi's whole loop until it returns: the Go, Rust, Python and Lisp SDKs have only synchronous providers and never announce I/O.
// The Node runtime's providers are async: a chain that awaits a timer announces it before its answer, and a chain that only waits on the host's
// answer (the current provider, however slow) never does (D213).
func TestAutocompleteSettlementAcrossSDKs(t *testing.T) {
	t.Parallel()
	cases := sdkHarnessCases()
	for _, language := range []string{"go", "python", "rust", "lisp"} {
		cases = append(cases, harnessCase{name: "packed-" + language, make: func(t *testing.T) *harness { return makePackedUIHarness(t, language) }})
	}
	for _, tc := range cases {
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
			ui := &autocompleteUI{recordingUI: h.ui}
			h.runner.SetUIContext(ui)
			if h.bridge != nil {
				h.bridge.SetUIContext(ui)
			}
			command, ok := findCommand(h.runner, "autocomplete-settle-register")
			if !ok {
				t.Fatal("autocomplete-settle-register is missing")
			}
			cc := h.runner.CreateCommandContext()
			ctx := extension.WithCommandContext(extension.WithContext(t.Context(), cc.Context), cc)
			if err := command.Handler(ctx, ""); err != nil {
				t.Fatal(err)
			}
			provider, _ := ui.AutocompleteProvider(t.Context())
			if provider == nil {
				t.Fatal("no installed provider")
			}
			slowAwaits := int32(0)
			if strings.Contains(tc.name, "node") {
				slowAwaits = 1
			}
			for _, tc := range []struct {
				input string
				// awaits is how many times the chain announced real I/O before it answered.
				awaits int32
			}{{"fast", 0}, {"slow", slowAwaits}, {"hostwait", 0}, {"fast", 0}, {"slow", slowAwaits}} {
				var awaited atomic.Int32
				requestCtx := tui.WithAutocompleteUnhold(t.Context(), func() { awaited.Add(1) })
				result, err := provider.GetSuggestions(requestCtx, []string{tc.input}, 0, len(tc.input), false)
				if err != nil {
					t.Fatalf("%s: %v", tc.input, err)
				}
				want := &extension.AutocompleteSuggestions{Prefix: tc.input, Items: []extension.AutocompleteItem{{Value: tc.input, Label: "settled:" + tc.input}}}
				if result == nil || result.Prefix != want.Prefix || !slices.Equal(result.Items, want.Items) {
					t.Fatalf("%s: answer %+v, want %+v", tc.input, result, want)
				}
				if got := awaited.Load(); got != tc.awaits {
					t.Fatalf("%s: the chain announced real I/O %d times before its answer, want %d", tc.input, got, tc.awaits)
				}
			}
		})
	}
}
