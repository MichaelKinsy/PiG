package extensionconformance

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi guards ExtensionContext.cwd, mode, hasUI and model with runner.assertActive(), so each throws the runner's stale message once the runtime is invalidated (runner.ts:571-600, 721-735).
// The Host sends NotifyInvalidate to every connected extension so a runtime that answers these members locally raises the same message. The fixtures' stale-probe command reads all four: the active runtime reports their values, and the invalidated one fails the command with the message and reports nothing.
func TestStaleContextLocalMembersRaiseTheStaleMessageInEverySDK(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	cases := sdkHarnessCases()
	for _, language := range []string{"go", "python", "rust"} {
		cases = append(cases, harnessCase{name: "packed-" + language, make: func(t *testing.T) *harness { return makePackedUIHarness(t, language) }})
	}
	const stale = "This extension ctx is stale after session replacement or reload."
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
			run := func() error {
				t.Helper()
				command, ok := findCommand(h.runner, "stale-probe")
				if !ok {
					t.Fatal("stale-probe not registered")
				}
				cc := h.runner.CreateCommandContext()
				return command.Handler(extension.WithCommandContext(extension.WithContext(context.Background(), cc.Context), cc), "")
			}
			h.ui.ClearRecorded()
			if err := run(); err != nil {
				t.Fatalf("active runtime: %v", err)
			}
			active := h.ui.Recorded()
			// The report is stale:<cwd>|<mode>|<hasUI>|<model>:info. The working directory is absolute for the host: it
			// starts with a drive letter on Windows, not with a slash.
			var cwd string
			if len(active) == 1 {
				cwd, _, _ = strings.Cut(strings.TrimPrefix(active[0], "stale:"), "|")
			}
			if len(active) != 1 || !strings.HasPrefix(active[0], "stale:") || !strings.HasSuffix(active[0], ":info") || !filepath.IsAbs(cwd) {
				t.Fatalf("active stale-probe = %v, want one stale:<cwd>|<mode>|<hasUI>|<model> report with an absolute working directory", active)
			}
			h.host.Invalidate(stale)
			h.ui.ClearRecorded()
			err := run()
			if err == nil || !strings.Contains(err.Error(), stale) {
				t.Fatalf("invalidated stale-probe error = %v, want the stale message", err)
			}
			if got := h.ui.Recorded(); !slices.Equal(got, []string(nil)) && len(got) != 0 {
				t.Fatalf("invalidated runtime still reported %v", got)
			}
		})
	}
}
