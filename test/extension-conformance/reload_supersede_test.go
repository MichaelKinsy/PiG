package extensionconformance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/fusepack"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/testfixture"
)

// Pi's reload invalidates the old runner once the session_shutdown handlers have returned and before the settings and the extensions load again
// (agent-session.ts:3825-3826 `await emitSessionShutdownEvent(oldRunner, ...); oldRunner.invalidate();`). From there a member of a captured pi or ctx
// throws the stale message where it is used (runner.ts:729-741 invalidate and assertActive), and a call that was pending keeps its own result: nothing
// settles a pending promise at invalidate. The old instance lives on with the handlers that were in flight.
//
// The host's counterpart of that point is in Host.Reload, once the config has loaded (supersedeGenerations); the generation then runs on until the
// reload's commit replaces or removes it. The row drives every realization of the conformance fixture through that stretch and reads from the wire
// what each call of the old generation was answered with: its result ("served"), the stale message ("stale"), or nothing ("no answer", which the
// packed Node realization gave before the fix: the host took the armed generation for an obsolete connection and dropped its calls).
//
// A generation a Node runtime hosts is Pi's old instance: stale from that point. A Go, Python or Rust generation is served until the commit, as it
// always was: Pi has no such extension, and a packed cell's process also carries the helper calls of the replacement's factories on the old
// generation's connection, which a stale answer would fail. Either way every call has exactly one answer.
//
// dialog-probe opens a select, an input, an editor and a confirm in turn. The select is open when the reload starts. The set the reload loads holds
// one more extension whose factory blocks until the test lets it go, so the reload stays between its supersede point and its commit while the test
// answers the select and the handler makes its next call. Every wait ends on an event of the run: the gate's marker, a frame on the wire, a dialog
// the host UI holds, the command's or the reload's return.

// reloadWindowCase is one realization: the configs a reload loads and, for the fused one, the factory the host serves in its own process.
type reloadWindowCase struct {
	name    string
	extName string
	// window is what a call made between the supersede point and the commit gets: "stale" or "served".
	window  string
	configs func(t *testing.T) []subprocess.ExtConfig
	fused   bool
}

func reloadWindowCases() []reloadWindowCase {
	nodeFixture := func(t *testing.T) string {
		requireConformanceTool(t, "node")
		return filepath.Join(findModuleRoot(t), "test", "extension-conformance", "testdata", "node-sdk-fixture", "main.mjs")
	}
	packed := func(language string) func(t *testing.T) []subprocess.ExtConfig {
		return func(t *testing.T) []subprocess.ExtConfig {
			configs, err := packedUIConfigs[language]()
			if err != nil {
				t.Fatal(err)
			}
			return slices.Clone(configs)
		}
	}
	return []reloadWindowCase{
		{name: "node-packed", extName: "node-sdk-fixture", window: "stale", configs: func(t *testing.T) []subprocess.ExtConfig {
			peer := filepath.Join(t.TempDir(), "node-cell-peer.mjs")
			if err := os.WriteFile(peer, []byte("export default function () {}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return []subprocess.ExtConfig{{Name: "node-sdk-fixture", Source: nodeFixture(t), Enabled: true}, {Name: "node-cell-peer", Source: peer, Enabled: true}}
		}},
		{name: "node-isolated", extName: "node-sdk-fixture", window: "stale", configs: func(t *testing.T) []subprocess.ExtConfig {
			return []subprocess.ExtConfig{{Name: "node-sdk-fixture", Source: nodeFixture(t), Enabled: true, Isolation: "strict"}}
		}},
		{name: "go-isolated", extName: "sdk-fixture", window: "served", configs: func(t *testing.T) []subprocess.ExtConfig {
			return []subprocess.ExtConfig{{Name: "sdk-fixture", Path: buildSDKFixture(t), Enabled: true}}
		}},
		{name: "go-packed", extName: "sdk-fixture", window: "served", configs: packed("go")},
		{name: "go-fused", extName: "sdk-fixture", window: "served", fused: true, configs: func(*testing.T) []subprocess.ExtConfig { return nil }},
		{name: "python-isolated", extName: "python-sdk-fixture", window: "served", configs: func(t *testing.T) []subprocess.ExtConfig {
			return []subprocess.ExtConfig{{Name: "python-sdk-fixture", Path: buildPythonSDKFixture(t), Enabled: true, RuntimeLanguage: "python"}}
		}},
		{name: "python-packed", extName: "python-sdk-fixture", window: "served", configs: packed("python")},
		{name: "rust-isolated", extName: "rust-sdk-fixture", window: "served", configs: func(t *testing.T) []subprocess.ExtConfig {
			return []subprocess.ExtConfig{{Name: "rust-sdk-fixture", Path: buildRustSDKFixture(t), Enabled: true}}
		}},
		{name: "rust-packed", extName: "rust-sdk-fixture", window: "served", configs: packed("rust")},
	}
}

// dialogAnswerTap reads from the wire what the host answered each dialog call of one extension with. A replacement generation numbers its calls from
// the start again, so an answer counts only when it is a dialog's: an error, or a result with a dialog's member.
type dialogAnswerTap struct {
	extName string
	mu      sync.Mutex
	calls   []string            // ids of the dialog calls, in the order the host read them
	answers map[string][]string // call id -> what the host wrote for it
}

func (w *dialogAnswerTap) inbound(extName string, env *subprocess.Envelope) {
	if extName != w.extName || env.Type != subprocess.MsgCall || env.Call == nil {
		return
	}
	switch env.Call.Method {
	case "ui.select", "ui.input", "ui.editor", "ui.confirm":
		w.mu.Lock()
		w.calls = append(w.calls, env.ID)
		w.mu.Unlock()
	}
}

func (w *dialogAnswerTap) outbound(extName string, frame []byte) {
	if extName != w.extName || !bytes.Contains(frame, []byte(`"call_result"`)) {
		return
	}
	var env subprocess.Envelope
	if json.Unmarshal(frame, &env) != nil || env.Type != subprocess.MsgCallResult || env.CallResult == nil {
		return
	}
	answer := ""
	if env.CallResult.Error != nil {
		answer = "error: " + env.CallResult.Error.Message
	} else {
		var result map[string]json.RawMessage
		if json.Unmarshal(env.CallResult.Result, &result) != nil {
			return
		}
		for _, member := range []string{"selected", "text", "confirmed"} {
			if value, ok := result[member]; ok {
				answer = member + "=" + string(value)
			}
		}
		if answer == "" {
			return
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !slices.Contains(w.calls, env.ID) {
		return
	}
	if w.answers == nil {
		w.answers = map[string][]string{}
	}
	w.answers[env.ID] = append(w.answers[env.ID], answer)
}

// answered returns, for each dialog call in the order the host read them, the answers the host wrote for it.
func (w *dialogAnswerTap) answered() [][]string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([][]string, len(w.calls))
	for i, id := range w.calls {
		out[i] = slices.Clone(w.answers[id])
	}
	return out
}

func TestConformance_CallsOfAGenerationAReloadReplacesAgreeAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()
	stale := "error: " + (&inproc.StaleError{}).Error()
	for _, tc := range reloadWindowCases() {
		t.Run(tc.name, func(t *testing.T) {
			requireConformanceTool(t, "node")
			configs := tc.configs(t)
			// The gate is the extension whose load keeps the reload between its supersede point and its commit: its factory marks that it runs and blocks its thread until the test opens it.
			gateDir := t.TempDir()
			gateSource := filepath.Join(gateDir, "reload-gate.mjs")
			gate := fmt.Sprintf("import { existsSync, writeFileSync } from \"node:fs\";\nconst cell = new Int32Array(new SharedArrayBuffer(4));\nexport default function () {\n  writeFileSync(%q, \"\");\n  while (!existsSync(%q)) Atomics.wait(cell, 0, 0, 2);\n}\n", filepath.Join(gateDir, "loading"), filepath.Join(gateDir, "open"))
			if err := os.WriteFile(gateSource, []byte(gate), 0o600); err != nil {
				t.Fatal(err)
			}
			exists := func(name string) bool { _, err := os.Stat(filepath.Join(gateDir, name)); return err == nil }
			openGate := sync.OnceFunc(func() { _ = os.WriteFile(filepath.Join(gateDir, "open"), nil, 0o600) })

			notify, status, actions := &[]string{}, &[]string{}, &[]string{}
			ui := newRecordingUI(notify, status)
			ui.dialogs = newDialogGate()
			bridge := subprocess.NewUIBridge(func() {})
			bridge.SetUIContext(ui)
			bridge.SetActions(conformanceActions(actions))
			host := subprocess.NewHost(t.TempDir())
			host.SetHostInfo(conformanceHostInfo)
			host.SetUIBridge(bridge)
			tap := &dialogAnswerTap{extName: tc.extName}
			host.SetInboundObserver(tap.inbound)
			host.SetOutboundObserver(tap.outbound)
			var loaderMu sync.Mutex
			var loaderErr error
			withGate := false
			host.SetConfigLoader(func() ([]subprocess.ExtConfig, error) {
				loaderMu.Lock()
				defer loaderMu.Unlock()
				if loaderErr != nil {
					return nil, loaderErr
				}
				loaded := slices.Clone(configs)
				if withGate {
					loaded = append(loaded, subprocess.ExtConfig{Name: "reload-gate", Source: gateSource, Enabled: true, Isolation: "strict"})
				}
				return loaded, nil
			})
			// The gate is opened before the host stops, so a failed row does not leave a reload waiting for it.
			t.Cleanup(func() { openGate(); host.Shutdown("test done") })

			var loaded []extension.Extension
			if tc.fused {
				// A Piglet Binary installs the host's parsers before any fused factory runs (fusepack.Register).
				sdk.SetHostHelpers(fusepack.HostHelpers())
				ext, err := host.LoadInProcess(context.Background(), subprocess.ExtConfig{Name: tc.extName, Enabled: true}, func(conn net.Conn) error { return testfixture.Extension().RunWithConn(conn) })
				if err != nil {
					t.Fatalf("fused load: %v", err)
				}
				loaded = []extension.Extension{*ext}
			} else {
				var err error
				if loaded, err = host.Reload(context.Background()); err != nil || len(loaded) != len(configs) {
					t.Fatalf("load: %d extensions, %v (%+v)", len(loaded), err, host.LastReloadReport())
				}
			}
			runner := inproc.NewRunner(loaded, t.TempDir())
			bridge.SetUIPromptScope(runner)
			command := func(name string) extension.RegisteredCommand {
				t.Helper()
				registered, ok := findCommand(runner, name)
				if !ok {
					t.Fatalf("%s is not registered", name)
				}
				return registered
			}
			ponged := func() bool { return slices.Contains(ui.Recorded(), "pong:info") }

			// A reload whose config does not load fails before the supersede point: the host marks nothing, and the generation, which stays in service, is served. (Pi's reload() throws after it has invalidated the old runner and leaves it stale; that is the reload's caller here, which invalidates the whole host: TestFailedExtensionReloadLeavesTheOldInstanceStale.)
			loaderMu.Lock()
			loaderErr = errors.New("the config does not load")
			loaderMu.Unlock()
			if _, err := host.Reload(context.Background()); err == nil {
				t.Fatal("a reload whose config loader fails returned no error")
			}
			loaderMu.Lock()
			loaderErr, withGate = nil, true
			loaderMu.Unlock()
			if err := command("ping").Handler(context.Background(), ""); err != nil || !ponged() {
				t.Fatalf("after a reload whose config did not load the generation's call was not served: %v, notifications %q", err, ui.Recorded())
			}
			ui.ClearRecorded()

			// The select is in flight when the reload passes its supersede point.
			probeCtx, cancelProbe := context.WithCancel(context.Background())
			defer cancelProbe()
			probed := make(chan error, 1)
			go func() { probed <- command("dialog-probe").Handler(probeCtx, "") }()
			if !awaitCondition(testbudget.Context(t), func() bool { return slices.Contains(ui.dialogs.openTitles(), "Pick") }) {
				t.Fatalf("the host UI never held the select (it holds %v)", ui.dialogs.openTitles())
			}
			reloaded := make(chan error, 1)
			var after []extension.Extension
			go func() {
				var err error
				after, err = host.Reload(context.Background())
				reloaded <- err
			}()
			if !awaitCondition(testbudget.Context(t), func() bool { return exists("loading") }) {
				t.Fatal("the reload never reached the gate extension's load")
			}
			ui.dialogs.answer(t, "Pick", "second")

			// The handler's next call is made between the supersede point and the commit. An SDK that knows the generation is stale may answer the call itself; the command then returns the stale message with no frame on the wire.
			verdict, returned := "no answer", false
			var probeErr error
			isStale := func(err error) bool {
				return err != nil && strings.Contains(err.Error(), (&inproc.StaleError{}).Error())
			}
			awaitCondition(testbudget.Context(t), func() bool {
				if !returned {
					select {
					case probeErr = <-probed:
						returned = true
					default:
					}
				}
				switch answers := tap.answered(); {
				case len(answers) > 1 && len(answers[1]) > 0 && answers[1][0] == stale, returned && isStale(probeErr):
					verdict = "stale"
				case returned:
					verdict = fmt.Sprintf("the command returned %v", probeErr)
				case slices.Contains(ui.dialogs.openTitles(), "Input"):
					verdict = "served"
				default:
					return false
				}
				return true
			})
			if verdict != tc.window {
				// The command is given up so that the row ends: a served dialog would wait for an answer, and an unanswered call for good.
				cancelProbe()
				t.Fatalf("a call made after the reload's supersede point and before its commit: %s, want %s (dialog calls answered %q)", verdict, tc.window, tap.answered())
			}
			awaitProbe := func() {
				t.Helper()
				if returned {
					return
				}
				select {
				case probeErr = <-probed:
				case <-testbudget.Context(t).Done():
					t.Fatal("the command never returned")
				}
			}
			// Every dialog call of the generation has exactly one answer, and the select in flight at the supersede point has its own.
			want := [][]string{{`selected="second"`}}
			if tc.window == "stale" {
				awaitProbe()
				if !isStale(probeErr) {
					t.Fatalf("the command whose call was answered stale returned %v, want Pi's stale message", probeErr)
				}
				// Each later call that reached the host has the stale message.
				for range tap.answered()[1:] {
					want = append(want, []string{stale})
				}
			} else {
				// The generation is served until the commit: its other dialogs are answered inside the window too, and the command returns.
				for _, dialog := range []struct{ title, value, answer string }{{"Input", "typed", `text="typed"`}, {"Editor", "edited", `text="edited"`}, {"Confirm", "true", "confirmed=true"}} {
					if !awaitCondition(testbudget.Context(t), func() bool { return slices.Contains(ui.dialogs.openTitles(), dialog.title) }) {
						t.Fatalf("the host UI never held %s (it holds %v; dialog calls answered %q)", dialog.title, ui.dialogs.openTitles(), tap.answered())
					}
					ui.dialogs.answer(t, dialog.title, dialog.value)
					want = append(want, []string{dialog.answer})
				}
				awaitProbe()
				if probeErr != nil {
					t.Fatalf("the command of a generation served until the commit returned %v", probeErr)
				}
			}
			if got := tap.answered(); !slices.EqualFunc(got, want, slices.Equal[[]string]) {
				t.Fatalf("the generation's dialog calls were answered %q, want %q", got, want)
			}
			if titles := ui.dialogs.openTitles(); len(titles) != 0 {
				t.Fatalf("the host UI still holds %v", titles)
			}

			openGate()
			select {
			case err := <-reloaded:
				if err != nil {
					t.Fatalf("reload: %v", err)
				}
			case <-testbudget.Context(t).Done():
				t.Fatal("the reload never returned")
			}
			if tc.fused {
				// The fused factory is not among the configs the reload loads: the reload removed it.
				return
			}
			if len(after) != len(configs)+1 {
				t.Fatalf("the reload loaded %d extensions, want %d (%+v)", len(after), len(configs)+1, host.LastReloadReport())
			}
			// The generation the reload loaded is in service.
			runner = inproc.NewRunner(after, t.TempDir())
			bridge.SetUIPromptScope(runner)
			if err := command("ping").Handler(context.Background(), ""); err != nil || !ponged() {
				t.Fatalf("the reloaded generation's call was not served: %v, notifications %q", err, ui.Recorded())
			}
		})
	}
}
