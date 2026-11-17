package codingagent

import (
	"errors"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// panicText is what f panics with, or "" when it returns.
func panicText(f func()) (text string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			text = fmt.Sprint(recovered)
		}
	}()
	f()
	return ""
}

// Pi's session.reload() invalidates the old runner right after session_shutdown (agent-session.ts:3825-3826), and the runner's invalidate invalidates its runtime
// (runner.ts:729-735 calls runtime.invalidate), which makes every API of that load throw the stale message and unsubscribes its event-bus subscriptions (loader.ts:198-210).
// A throw before the rebuild (:3844) leaves the session on that old runner (interactive-mode.ts:6670-6704 shows the error), so the old extensions' handlers still run (the
// runner's emit methods look at no staleness) and whatever they do through the API they captured must fail. A /reload that follows loads again and works.
//
// mutation-checked: dropping the runtime's Invalidate from the failure path of the Reload closure (interactive_commands.go) leaves the captured API live and the subscription
// retained; dropping the Reactivate before the next reload's factories fails their load with the stale message.
func TestFailedInteractiveReloadInvalidatesTheOldRunnersRuntimeAndTheNextReloadWorks(t *testing.T) {
	restoreStartupTheme(t) // Reload applies the theme setting, which selects the process-wide active theme.
	runtime := extension.CreateExtensionRuntime()
	var captured extension.API
	load := func() (extension.Extension, error) {
		return factoryload.LoadExtensionFromFactory(func(pi extension.API) error {
			captured = pi
			return nil
		}, ".", extension.CreateEventBus(), runtime, "builtin:captured")
	}
	first, err := load()
	if err != nil {
		t.Fatal(err)
	}
	firstAPI := captured
	var unsubscribed int
	runtime.TrackEventBusSubscription(func() { unsubscribed++ })

	failing := true
	var loadErr error
	m := reloadTestMode(InteractiveModeOptions{
		CWD: t.TempDir(),
		ReloadBuiltinExtensions: func() []extension.Extension {
			loaded, err := load()
			if err != nil {
				loadErr = err
				return nil
			}
			return []extension.Extension{loaded}
		},
		ReloadResourceProvider: func() ReloadResourceSnapshot {
			if failing {
				return ReloadResourceSnapshot{Err: errors.New("resource resolution failed")}
			}
			return ReloadResourceSnapshot{}
		},
	})
	previous := inproc.NewRunner([]extension.Extension{first}, m.opts.CWD, runtime)
	m.newRunner = previous
	slash := m.buildSlashContext(t.Context())

	if err := slash.Reload(); err == nil {
		t.Fatal("the reload did not fail")
	}
	if m.newRunner != previous || !previous.IsStale() {
		t.Fatal("a reload that failed after session_shutdown must leave the mode on its old, stale runner")
	}
	if err := runtime.AssertActive(); err == nil || err.Error() != extension.DefaultStaleRuntimeMessage {
		t.Errorf("runtime.AssertActive() = %v after the failed reload, want the stale message", err)
	}
	if got := panicText(func() { firstAPI.GetActiveTools() }); got != extension.DefaultStaleRuntimeMessage {
		t.Errorf("the API the old extension captured panicked with %q, want the stale message", got)
	}
	if unsubscribed != 1 {
		t.Errorf("the failed reload ended %d event-bus subscriptions of the old runtime, want 1", unsubscribed)
	}

	failing = false
	if err := slash.Reload(); err != nil {
		t.Fatalf("the reload after the failed one: %v", err)
	}
	if loadErr != nil {
		t.Fatalf("the reload after the failed one could not load the factory: %v", loadErr)
	}
	if m.newRunner == previous || m.newRunner.IsStale() || m.newRunner.ExtensionCount() != 1 {
		t.Fatalf("the reload after the failed one left runner %p (stale %v, %d extensions), want a new live runner with the factory", m.newRunner, m.newRunner.IsStale(), m.newRunner.ExtensionCount())
	}
	if err := runtime.AssertActive(); err != nil {
		t.Errorf("runtime.AssertActive() = %v after the reload that loaded, want it active", err)
	}
	if got := panicText(func() { captured.GetActiveTools() }); got == extension.DefaultStaleRuntimeMessage {
		t.Errorf("the API of the reloaded factory is stale: %q", got)
	}
}
