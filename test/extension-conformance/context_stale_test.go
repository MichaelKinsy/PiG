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
//
// The stale-members command reads the rest of the context's members (the theme, the terminal size, the session name, the three widget forms, the tool update, and where the SDK has them the
// settings and the late tool renderer): it reports each outcome on the active runtime, and on the invalidated one fails with one line per member, each carrying the message (runner.ts:883-1037).
func TestStaleContextLocalMembersRaiseTheStaleMessageInEverySDK(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	cases := sdkHarnessCases()
	for _, language := range []string{"go", "python", "rust", "lisp"} {
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
	for _, tc := range cases {
		t.Run(tc.name+"/members", func(t *testing.T) {
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
				command, ok := findCommand(h.runner, "stale-members")
				if !ok {
					t.Fatal("stale-members not registered")
				}
				cc := h.runner.CreateCommandContext()
				return command.Handler(extension.WithCommandContext(extension.WithContext(context.Background(), cc.Context), cc), "")
			}
			members := membersOf(tc.name)
			h.ui.ClearRecorded()
			if err := run(); err != nil {
				t.Fatalf("active runtime: %v", err)
			}
			active := strings.Join(h.ui.Recorded(), "\n")
			for _, member := range members {
				line := ""
				for candidate := range strings.SplitSeq(active, "\n") {
					if strings.HasPrefix(candidate, member+"=") {
						line = candidate
					}
				}
				if line == "" || strings.Contains(line, "This extension ctx is stale") {
					t.Fatalf("active %s = %q in %q, want an outcome that is not the stale message", member, line, active)
				}
			}
			h.host.Invalidate(stale)
			h.ui.ClearRecorded()
			err := run()
			if err == nil {
				t.Fatalf("invalidated stale-members reported %v, want the failure", h.ui.Recorded())
			}
			for _, member := range members {
				found := false
				for line := range strings.SplitSeq(err.Error(), "\n") {
					if strings.HasPrefix(line, member+"=") {
						found = true
						if !strings.Contains(line, stale) {
							t.Errorf("invalidated %s = %q, want the stale message", member, line)
						}
					}
				}
				if !found {
					t.Errorf("invalidated stale-members error has no %s line: %v", member, err)
				}
			}
		})
	}
}

// The members each SDK has on its context: Pi's Node context has no terminal size, tool update or settings member.
func membersOf(name string) []string {
	switch {
	case strings.Contains(name, "node"):
		return []string{"theme", "sessionName", "setWidgetLines", "setWidgetView"}
	case strings.Contains(name, "rust"):
		return []string{"theme", "width", "height", "sessionName", "setWidgetLines", "setWidgetView", "setWidgetRenderer", "onUpdate", "getSettings", "registerToolRenderer"}
	case strings.Contains(name, "python"), strings.Contains(name, "lisp"):
		return []string{"theme", "width", "height", "sessionName", "setWidgetLines", "setWidgetView", "setWidgetRenderer", "onUpdate", "getSettings"}
	}
	return []string{"theme", "width", "height", "sessionName", "setWidgetLines", "setWidgetView", "setWidgetRenderer", "onUpdate"}
}

// Pi runs session_shutdown handlers with a context that is still active and invalidates the runtime afterwards, so a handler reads every member of its own context. The fixtures read the
// members from their session_shutdown handler when the reason is stale-members-test.
func TestShutdownHandlerContextIsNotStaleInEverySDK(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	cases := sdkHarnessCases()
	for _, language := range []string{"go", "python", "rust", "lisp"} {
		cases = append(cases, harnessCase{name: "packed-" + language, make: func(t *testing.T) *harness { return makePackedUIHarness(t, language) }})
	}
	for _, tc := range cases {
		if strings.Contains(tc.name, "node") {
			continue
		}
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
			h.ui.ClearRecorded()
			if _, err := h.runner.Emit(context.Background(), extension.SessionShutdownEvent{Type: "session_shutdown", Reason: "stale-members-test"}); err != nil {
				t.Fatalf("session_shutdown emit: %v", err)
			}
			recorded := strings.Join(h.ui.Recorded(), "\n")
			for _, member := range membersOf(tc.name) {
				line := ""
				for candidate := range strings.SplitSeq(recorded, "\n") {
					if strings.HasPrefix(candidate, member+"=") {
						line = candidate
					}
				}
				if line == "" || strings.Contains(line, "This extension ctx is stale") {
					t.Errorf("shutdown handler %s = %q in %q, want an outcome that is not the stale message", member, line, recorded)
				}
			}
		})
	}
}
