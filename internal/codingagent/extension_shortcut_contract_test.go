package codingagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type shortcutOracleBehavior struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type shortcutOracleShortcut struct {
	Key      string                 `json:"key"`
	Behavior shortcutOracleBehavior `json:"behavior"`
}

type shortcutOracleProbe struct {
	Shortcuts []shortcutOracleShortcut `json:"shortcuts"`
	Inputs    []string                 `json:"inputs"`
}

type shortcutOracleStep struct {
	Consumed bool     `json:"consumed"`
	Called   []string `json:"called"`
	Errors   []string `json:"errors"`
}

type shortcutOracleResult struct {
	Installed bool                 `json:"installed"`
	Steps     []shortcutOracleStep `json:"steps"`
}

func shortcutOracleProbes() []shortcutOracleProbe {
	ok := shortcutOracleBehavior{Kind: "ok"}
	reject := func(message string) shortcutOracleBehavior {
		return shortcutOracleBehavior{Kind: "reject", Message: message}
	}
	thrownValue := shortcutOracleBehavior{Kind: "rejectValue", Message: "a thrown string, not an Error"}
	return []shortcutOracleProbe{
		// Two registered keys that match one input: the first registered runs, the second never does.
		{
			Shortcuts: []shortcutOracleShortcut{{"ctrl+shift+x", ok}, {"shift+ctrl+x", reject("second never runs")}},
			Inputs:    []string{"\x1b[120;6u", "\x1b[120;5u", "x", "\x1b[120;6u"},
		},
		{
			Shortcuts: []shortcutOracleShortcut{{"shift+ctrl+x", reject("first registered fails")}, {"ctrl+shift+x", ok}},
			Inputs:    []string{"\x1b[120;6u", "\x1b[120;6u"},
		},
		// Rejection forms, a key that is not registered, a modifier variant, a release event and the same key twice.
		{
			Shortcuts: []shortcutOracleShortcut{{"f5", reject("boom: f5")}, {"alt+j", ok}, {"ctrl+alt+k", thrownValue}, {"shift+f5", ok}},
			Inputs:    []string{"\x1b[15~", "\x1bj", "\x1b[107;7u", "\x1b[15;2~", "\x1b[15~", "\x1b[15;5~", "\x1b[120;6:3u", "\x1b[15;1:3~", "j", ""},
		},
		{
			Shortcuts: []shortcutOracleShortcut{{"ctrl+shift+a", reject("a failed")}, {"ctrl+shift+b", ok}, {"alt+shift+c", reject("c failed")}, {"ctrl+alt+shift+d", ok}},
			Inputs:    []string{"\x1b[97;6u", "\x1b[98;6u", "\x1b[99;4u", "\x1b[100;8u", "\x1b[65;6u", "A", "\x1b[97;5u", "\x1b[97;6:2u", "\x1b[97;6:3u"},
		},
		// Function and navigation keys, and a message with a newline-free colon.
		{
			Shortcuts: []shortcutOracleShortcut{{"f1", ok}, {"f12", reject("f12: down")}, {"pageup", ok}, {"ctrl+home", reject("home")}, {"shift+tab", ok}},
			Inputs:    []string{"\x1bOP", "\x1b[24~", "\x1b[5~", "\x1b[1;5H", "\x1b[Z", "\x1b[6~", "\x1b[1;5F"},
		},
		// No shortcut: Pi installs no handler at all.
		{Shortcuts: []shortcutOracleShortcut{}, Inputs: []string{"a"}},
	}
}

// shortcutRun records which handlers ran.
type shortcutRun struct {
	mu     sync.Mutex
	called []string
}

func (r *shortcutRun) record(key string) {
	r.mu.Lock()
	r.called = append(r.called, key)
	r.mu.Unlock()
}

func (r *shortcutRun) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	called := slices.Clone(r.called)
	r.called = nil
	return called
}

// shortcutRunnerFor registers the shortcuts, in order, on one in-process extension: a handler records itself and then returns nil or the probe's error.
func shortcutRunnerFor(run *shortcutRun, shortcuts []shortcutOracleShortcut) *inproc.Runner {
	ext := extension.Extension{Name: "probe", Path: "/ext/probe.ts"}
	for _, shortcut := range shortcuts {
		ext.SetRegisteredShortcut(extension.KeyID(shortcut.Key), extension.ExtensionShortcut{
			Shortcut:      extension.KeyID(shortcut.Key),
			ExtensionPath: "/ext/probe.ts",
			Handler: func(context.Context) error {
				run.record(shortcut.Key)
				if shortcut.Behavior.Kind == "ok" {
					return nil
				}
				return errors.New(shortcut.Behavior.Message)
			},
		})
	}
	return inproc.NewRunner([]extension.Extension{ext}, "")
}

// chatErrorRows is the chat's rows that are error blocks, without their padding, in order.
func chatErrorRows(m *InteractiveMode) []string {
	var rows []string
	for line := range strings.SplitSeq(chatPlainText(m), "\n") {
		if row := strings.TrimSpace(line); strings.HasPrefix(row, "Error: ") {
			rows = append(rows, row)
		}
	}
	return rows
}

// InteractiveMode.setupExtensionShortcuts' onExtensionShortcut (interactive-mode.ts) against pinned Pi: the shortcuts are tried in registration order, the first whose key
// matches the raw input (matchesKey, so legacy and Kitty encodings, modifier order and key release events as Pi reads them) runs its handler and consumes the input, an
// input no shortcut matches is not consumed, and a handler's rejection is shown as "Error: Shortcut handler error: <message>". No shortcut installs no listener.
func TestExtensionShortcutDispatchMatchesPi(t *testing.T) {
	probes := shortcutOracleProbes()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/extension_shortcuts.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []shortcutOracleResult
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	consumedSomething, errorsSeen := false, false
	for i, probe := range probes {
		synctest.Test(t, func(t *testing.T) {
			run := &shortcutRun{}
			m := newExtensionErrorProbe(t)
			m.runCtx = t.Context()
			m.newRunner = shortcutRunnerFor(run, probe.Shortcuts)
			m.setupExtensionShortcutListener(t.Context())
			if installed := m.extensionShortcutListener != nil; installed != want[i].Installed {
				t.Fatalf("probe %d: listener installed = %v, Pi installs %v", i, installed, want[i].Installed)
			}
			if !want[i].Installed {
				return
			}
			for k, data := range probe.Inputs {
				before := len(chatErrorRows(m))
				consumed := m.extensionShortcutListener(data)
				synctest.Wait()
				for len(m.uiTaskCh) > 0 {
					(<-m.uiTaskCh)()
				}
				got := shortcutOracleStep{Consumed: consumed, Called: run.take(), Errors: chatErrorRows(m)[before:]}
				wantStep := want[i].Steps[k]
				wantErrors := make([]string, len(wantStep.Errors))
				for e, message := range wantStep.Errors {
					wantErrors[e] = "Error: " + message
				}
				if got.Consumed != wantStep.Consumed || !slices.Equal(got.Called, wantStep.Called) || !slices.Equal(got.Errors, wantErrors) {
					t.Errorf("probe %d input %q: Pig consumed=%v called=%q errors=%q; Pi consumed=%v called=%q errors=%q", i, data, got.Consumed, got.Called, got.Errors, wantStep.Consumed, wantStep.Called, wantErrors)
				}
				consumedSomething = consumedSomething || wantStep.Consumed
				errorsSeen = errorsSeen || len(wantStep.Errors) > 0
			}
		})
	}
	if !consumedSomething || !errorsSeen {
		t.Fatalf("the probes consumed no input (%v) or reported no handler error (%v)", consumedSomething, errorsSeen)
	}
}

// The handler runs without blocking input (interactive-mode.ts: "Run handler async, don't block input"): the listener consumes the key and returns while the handler is
// still running, another shortcut fires meanwhile, and a rejection that comes later is shown when it comes.
func TestExtensionShortcutHandlerDoesNotBlockInput(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := newExtensionErrorProbe(t)
		m.runCtx = t.Context()
		release := make(chan struct{})
		var started, finished atomic.Int32
		ext := extension.Extension{Name: "probe", Path: "/ext/probe.ts"}
		ext.SetRegisteredShortcut("ctrl+shift+x", extension.ExtensionShortcut{ExtensionPath: "/ext/probe.ts", Handler: func(context.Context) error {
			started.Add(1)
			<-release
			finished.Add(1)
			return errors.New("late failure")
		}})
		ext.SetRegisteredShortcut("ctrl+shift+y", extension.ExtensionShortcut{ExtensionPath: "/ext/probe.ts", Handler: func(context.Context) error {
			started.Add(1)
			finished.Add(1)
			return nil
		}})
		m.newRunner = inproc.NewRunner([]extension.Extension{ext}, "")
		m.setupExtensionShortcutListener(t.Context())

		if !m.extensionShortcutListener("\x1b[120;6u") {
			t.Fatal("the first shortcut was not consumed")
		}
		synctest.Wait()
		if started.Load() != 1 || finished.Load() != 0 {
			t.Fatalf("after the first key: started %d, finished %d; want the handler running (1, 0)", started.Load(), finished.Load())
		}
		if !m.extensionShortcutListener("\x1b[121;6u") {
			t.Fatal("a second shortcut was not consumed while the first handler ran")
		}
		synctest.Wait()
		if started.Load() != 2 || finished.Load() != 1 {
			t.Fatalf("after the second key: started %d, finished %d; want (2, 1)", started.Load(), finished.Load())
		}
		if rows := chatErrorRows(m); len(rows) != 0 {
			t.Fatalf("an error is shown before the handler failed: %q", rows)
		}
		close(release)
		synctest.Wait()
		for len(m.uiTaskCh) > 0 {
			(<-m.uiTaskCh)()
		}
		if got, want := chatErrorRows(m), []string{"Error: Shortcut handler error: late failure"}; !slices.Equal(got, want) {
			t.Fatalf("errors after the handler failed = %q, want %q", got, want)
		}
	})
}

// getShortcuts takes the effective keybinding configuration: a key the user binds to a reserved action stops being an extension shortcut, and a reserved default key the
// user rebinds elsewhere is free for extensions (interactive-mode.ts setupExtensionShortcuts passes this.keybindings.getEffectiveConfig()).
func TestExtensionShortcutsFollowRemappedKeybindings(t *testing.T) {
	restoreTUIKeybindings(t)
	const shiftCtrlX, ctrlL = "\x1b[120;6u", "\f"
	ok := shortcutOracleBehavior{Kind: "ok"}
	setup := func(t *testing.T, user map[string][]KeyID) (*InteractiveMode, *shortcutRun) {
		run := &shortcutRun{}
		m, _ := newCustomEditorDispatchMode(t)
		m.runCtx = t.Context()
		m.newRunner = shortcutRunnerFor(run, []shortcutOracleShortcut{{Key: "ctrl+shift+x", Behavior: ok}, {Key: "ctrl+l", Behavior: ok}})
		if user != nil {
			m.keybindings.SetUserBindings(user)
		}
		m.setupExtensionShortcutListener(t.Context())
		return m, run
	}
	consume := func(m *InteractiveMode, data string) bool {
		return m.extensionShortcutListener != nil && m.extensionShortcutListener(data)
	}
	skipped := func(m *InteractiveMode) []string {
		var messages []string
		for _, d := range m.newRunner.ShortcutDiagnostics() {
			messages = append(messages, d.Message)
		}
		return messages
	}
	t.Run("defaults", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			m, run := setup(t, nil)
			if !consume(m, shiftCtrlX) || consume(m, "a") {
				t.Fatal("ctrl+shift+x must be consumed and a must not")
			}
			// ctrl+l is app.model.select's default key, a reserved action.
			if consume(m, ctrlL) {
				t.Fatal("ctrl+l is bound to a reserved action by default and ran an extension handler")
			}
			synctest.Wait()
			if got := run.take(); !slices.Equal(got, []string{"ctrl+shift+x"}) {
				t.Fatalf("handlers run = %q, want only ctrl+shift+x", got)
			}
			if got, want := skipped(m), []string{"Extension shortcut 'ctrl+l' from /ext/probe.ts conflicts with built-in shortcut. Skipping."}; !slices.Equal(got, want) {
				t.Fatalf("diagnostics = %q, want %q", got, want)
			}
		})
	})
	t.Run("a reserved action remapped onto an extension key", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			m, run := setup(t, map[string][]KeyID{"app.clear": {"ctrl+shift+x"}})
			if consume(m, shiftCtrlX) || consume(m, ctrlL) {
				t.Fatal("both keys are bound to reserved actions and ran an extension handler")
			}
			synctest.Wait()
			if got := run.take(); len(got) != 0 {
				t.Fatalf("handlers run = %q, want none", got)
			}
			want := []string{
				"Extension shortcut 'ctrl+shift+x' from /ext/probe.ts conflicts with built-in shortcut. Skipping.",
				"Extension shortcut 'ctrl+l' from /ext/probe.ts conflicts with built-in shortcut. Skipping.",
			}
			if got := skipped(m); !slices.Equal(got, want) {
				t.Fatalf("diagnostics = %q, want %q", got, want)
			}
			if m.extensionShortcutListener != nil {
				t.Fatal("every shortcut is reserved, so no listener may be installed")
			}
		})
	})
	t.Run("a reserved default key rebound elsewhere is free", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			m, run := setup(t, map[string][]KeyID{"app.model.select": {"ctrl+shift+m"}})
			if !consume(m, ctrlL) || !consume(m, shiftCtrlX) {
				t.Fatal("ctrl+l is no longer bound to a reserved action and must run its extension handler")
			}
			synctest.Wait()
			got := run.take()
			slices.Sort(got)
			if !slices.Equal(got, []string{"ctrl+l", "ctrl+shift+x"}) {
				t.Fatalf("handlers run = %q, want ctrl+l and ctrl+shift+x", got)
			}
		})
	})
}

// Setting the shortcuts up again, as a reload or a Session replacement does, replaces the listener (interactive-mode.ts resetExtensionUI clears onExtensionShortcut, then
// setupExtensionShortcuts installs a handler only when the new runner has shortcuts): the earlier handlers stop running when the new runner has none, or none at all.
func TestExtensionShortcutListenerIsReplacedBySetup(t *testing.T) {
	ok := shortcutOracleBehavior{Kind: "ok"}
	for name, replacement := range map[string]func(*shortcutRun) *inproc.Runner{
		"a runner without shortcuts": func(*shortcutRun) *inproc.Runner { return inproc.NewRunner(nil, "") },
		"no runner":                  func(*shortcutRun) *inproc.Runner { return nil },
		"other shortcuts": func(run *shortcutRun) *inproc.Runner {
			return shortcutRunnerFor(run, []shortcutOracleShortcut{{Key: "alt+j", Behavior: ok}})
		},
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				run := &shortcutRun{}
				m := newExtensionErrorProbe(t)
				m.runCtx = t.Context()
				m.newRunner = shortcutRunnerFor(run, []shortcutOracleShortcut{{Key: "ctrl+shift+x", Behavior: ok}})
				m.setupExtensionShortcutListener(t.Context())
				if m.extensionShortcutListener == nil || !m.extensionShortcutListener("\x1b[120;6u") {
					t.Fatal("the first runner's shortcut was not bound")
				}
				synctest.Wait()
				if got := run.take(); !slices.Equal(got, []string{"ctrl+shift+x"}) {
					t.Fatalf("first runner handlers = %q, want ctrl+shift+x", got)
				}
				m.newRunner = replacement(run)
				m.setupExtensionShortcutListener(t.Context())
				if m.extensionShortcutListener != nil && m.extensionShortcutListener("\x1b[120;6u") {
					t.Fatal("the replaced runner's shortcut is still bound")
				}
				synctest.Wait()
				if got := run.take(); len(got) != 0 {
					t.Fatalf("handlers run after the replacement = %q, want none", got)
				}
			})
		})
	}
}
