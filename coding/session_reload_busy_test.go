package coding

import (
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Ports packages/coding-agent/test/suite/regressions/9221-reload-during-tool.test.ts (Pi c3b938e3e): reload is rejected while the session
// is running or compacting, before session_shutdown or any runner invalidation, so a running tool keeps its context.
func TestSessionReloadRejectedWhileRunningOrCompacting(t *testing.T) {
	for _, tc := range []struct {
		name string
		busy func(*Session) func()
		want string
	}{
		{"running", func(s *Session) func() {
			s.runState.active.Store(true)
			return func() { s.runState.active.Store(false) }
		}, "Wait for the current response to finish before reloading."},
		{"compacting", func(s *Session) func() {
			s.compacting.Store(true)
			return func() { s.compacting.Store(false) }
		}, "Wait for compaction to finish before reloading."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			shutdowns := 0
			runner := inproc.NewRunner([]extension.Extension{{Path: "ext", Handlers: map[string][]extension.HandlerFn{
				"session_shutdown": {func(...any) (any, error) { shutdowns++; return nil, nil }},
			}}}, root)
			loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{
				CWD: root, AgentDir: filepath.Join(root, "agent"), NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true,
			})
			session, err := NewSession(newTestServices(t), SessionOptions{NoSession: true, Runner: runner, ResourceLoader: loader})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			release := tc.busy(session)
			err = session.Reload(t.Context())
			if err == nil || err.Error() != tc.want {
				t.Fatalf("Reload error = %v, want %q", err, tc.want)
			}
			if shutdowns != 0 || runner.IsStale() || session.ExtensionRunner() != runner {
				t.Fatalf("a rejected reload touched the runner: shutdowns=%d stale=%v same=%v", shutdowns, runner.IsStale(), session.ExtensionRunner() == runner)
			}
			release()
			if err := session.Reload(t.Context()); err != nil {
				t.Fatalf("idle Reload: %v", err)
			}
			if shutdowns != 1 || !runner.IsStale() {
				t.Fatalf("an idle reload must shut the old runner down: shutdowns=%d stale=%v", shutdowns, runner.IsStale())
			}
		})
	}
}
