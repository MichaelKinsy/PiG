package codingagent

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func TestSessionModelExtensionLifecycleUpstream(t *testing.T) {
	restoreStartupTheme(t) // Reload applies the theme setting, which selects the process-wide active theme.
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-model-extension.test.ts:509
	t.Run("bindExtensions emits session_start and reload emits session_shutdown then session_start", func(t *testing.T) {
		var events []string
		factory := func() extension.Extension {
			return extension.Extension{Path: "lifecycle", Handlers: map[string][]extension.HandlerFn{
				"session_start": {func(args ...any) (any, error) {
					event := args[0].(extension.SessionStartEvent)
					events = append(events, "start:"+event.Reason)
					return nil, nil
				}},
				"session_shutdown": {func(args ...any) (any, error) {
					event := args[0].(extension.SessionShutdownEvent)
					events = append(events, "shutdown:"+event.Reason)
					return nil, nil
				}},
			}}
		}
		dir := t.TempDir()
		mode := reloadTestMode(InteractiveModeOptions{CWD: dir, AgentDir: dir, NoPromptTemplates: true, NoThemes: true, ReloadBuiltinExtensions: func() []extension.Extension { return []extension.Extension{factory()} }})
		mode.newRunner = inproc.NewRunner([]extension.Extension{factory()}, dir)
		emitSessionStart(mode.newRunner, "startup")
		if err := mode.buildSlashContext(t.Context()).Reload(); err != nil {
			t.Fatal(err)
		}
		want := []string{"start:startup", "shutdown:reload", "start:reload"}
		if !reflect.DeepEqual(events, want) {
			t.Fatalf("lifecycle=%v, want %v", events, want)
		}
	})
}
