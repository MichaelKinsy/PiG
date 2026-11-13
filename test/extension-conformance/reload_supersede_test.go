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
	"time"

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
		{name: "lisp-isolated", extName: lispFixtureName, window: "served", configs: func(t *testing.T) []subprocess.ExtConfig {
			requireSBCL(t)
			return []subprocess.ExtConfig{lispFixtureConfig(t, "lisp-sdk-fixture", lispFixtureName)}
		}},
		{name: "lisp-packed", extName: lispFixtureName, window: "served", configs: func(t *testing.T) []subprocess.ExtConfig {
			requireSBCL(t)
			return packed("lisp")(t)
		}},
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

// mutation-checked: without supersedeGenerations in Host.Reload node-packed gets no answer, for the select in flight either (the host drops the calls its prompt handlers make), and node-isolated is served; with every generation superseded, as in bf60a5312160, the packed Go, Python and Rust rows fail after the reload: the cell's replacement did not load.
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

// reloadHeldHost returns a host for the Node extension "held", whose "held" command marks that it runs, waits for the go file and then makes a call it does not await, whose "hang" command marks that it runs and blocks its thread for good, and whose "ping" command notifies. Not packed, the extension has its process to itself. Packed, it shares a Node process, and so that process's one thread, with the extension "neighbour": its "neighbour-wait" command marks that it runs and awaits a file that never comes, and its "neighbour-ping" command notifies. observe sees every frame the host writes to "held"; load reloads the host and returns the lookup of the commands of the generation it loaded; recorded returns the notifications the host UI showed.
func reloadHeldHost(t *testing.T, packed bool, observe func(frame []byte)) (host *subprocess.Host, recorded func() []string, dir string, load func() func(name string) extension.RegisteredCommand) {
	t.Helper()
	requireConformanceTool(t, "node")
	dir = t.TempDir()
	source := filepath.Join(dir, "held.mjs")
	code := fmt.Sprintf(`import { existsSync, writeFileSync } from "node:fs";
const dir = %q;
export default function (pi) {
  pi.registerCommand("held", { description: "held", handler: async (_args, ctx) => {
    writeFileSync(dir + "/running", "");
    while (!existsSync(dir + "/go")) await new Promise((resolve) => setTimeout(resolve, 2));
    ctx.ui.notify("held went on", "info");
  } });
  pi.registerCommand("hang", { description: "hang", handler: async () => {
    writeFileSync(dir + "/running", "");
    Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0);
  } });
  pi.registerCommand("ping", { description: "ping", handler: async (_args, ctx) => { ctx.ui.notify("pong", "info"); } });
}
`, dir)
	if err := os.WriteFile(source, []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	configs := []subprocess.ExtConfig{{Name: "held", Source: source, Enabled: true, Isolation: "strict"}}
	if packed {
		neighbour := filepath.Join(dir, "neighbour.mjs")
		code := fmt.Sprintf(`import { existsSync, writeFileSync } from "node:fs";
const dir = %q;
export default function (pi) {
  pi.registerCommand("neighbour-wait", { description: "wait", handler: async () => {
    writeFileSync(dir + "/neighbour-running", "");
    while (!existsSync(dir + "/never")) await new Promise((resolve) => setTimeout(resolve, 2));
  } });
  pi.registerCommand("neighbour-ping", { description: "ping", handler: async (_args, ctx) => { ctx.ui.notify("neighbour pong", "info"); } });
}
`, dir)
		if err := os.WriteFile(neighbour, []byte(code), 0o600); err != nil {
			t.Fatal(err)
		}
		configs = []subprocess.ExtConfig{{Name: "held", Source: source, Enabled: true}, {Name: "neighbour", Source: neighbour, Enabled: true}}
	}
	notify, status, actions := &[]string{}, &[]string{}, &[]string{}
	ui := newRecordingUI(notify, status)
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	bridge.SetActions(conformanceActions(actions))
	host = subprocess.NewHost(t.TempDir())
	host.SetHostInfo(conformanceHostInfo)
	host.SetUIBridge(bridge)
	if observe != nil {
		host.SetOutboundObserver(func(extName string, frame []byte) {
			if extName == "held" {
				observe(frame)
			}
		})
	}
	host.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return slices.Clone(configs), nil })
	load = func() func(name string) extension.RegisteredCommand {
		t.Helper()
		loaded, err := host.Reload(context.Background())
		if err != nil || len(loaded) != len(configs) {
			t.Fatalf("load: %d extensions, %v (%+v)", len(loaded), err, host.LastReloadReport())
		}
		runner := inproc.NewRunner(loaded, t.TempDir())
		bridge.SetUIPromptScope(runner)
		return func(name string) extension.RegisteredCommand {
			t.Helper()
			registered, ok := findCommand(runner, name)
			if !ok {
				t.Fatalf("%s is not registered", name)
			}
			return registered
		}
	}
	return host, ui.Recorded, dir, load
}

// Pi's oldRunner.invalidate() is synchronous (agent-session.ts:3825-3826): a handler of the old instance made its call before it, and the call is served, or makes it after, and its ctx throws. A Node runtime applies the invalidation when its loop reads the notification. The row keeps the notification on the host's write loop until the handler in flight has gone on and made its call, which is the runtime reading it late: the call, made with a ctx that was live, is served. The host used to mark the generation when it sent the notification: it refused the call, the runtime wrote a diagnostic where Pi throws, and the command ended as if its notification had been shown. In RPC mode that is a `response` with no `extension_error`, which TestRPCCommandInFlightAtAReloadIsAnswered/node wrote in 1 of 50 runs; that run was not instrumented, so the late read is inferred for it.
// mutation-checked: with the wait for the acknowledgement off (the awaitBy branch of Conn.invalidateAcknowledged), the call is refused: "was not served: notifications []".
func TestConformance_ACallMadeBeforeTheRuntimeReadsAReloadsInvalidationIsServed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (runs a Node extension)")
	}
	t.Parallel()
	held, release := make(chan struct{}), make(chan struct{})
	var holdOnce, releaseOnce sync.Once
	releaseFrame := func() { releaseOnce.Do(func() { close(release) }) }
	host, recorded, dir, load := reloadHeldHost(t, false, func(frame []byte) {
		var env subprocess.Envelope
		if !bytes.Contains(frame, []byte(subprocess.NotifyInvalidate)) || json.Unmarshal(frame, &env) != nil || env.Type != subprocess.MsgNotify || env.Notify == nil || env.Notify.Method != subprocess.NotifyInvalidate {
			return
		}
		holdOnce.Do(func() { close(held) })
		<-release
	})
	t.Cleanup(func() { releaseFrame(); host.Shutdown("test done") })
	handler := load()("held").Handler
	returned := make(chan error, 1)
	go func() { returned <- handler(context.Background(), "") }()
	exists := func(name string) bool { _, err := os.Stat(filepath.Join(dir, name)); return err == nil }
	if !awaitCondition(testbudget.Context(t), func() bool { return exists("running") }) {
		t.Fatal("the held command never ran")
	}
	reloaded := make(chan error, 1)
	go func() {
		_, err := host.Reload(context.Background())
		reloaded <- err
	}()
	select {
	case <-held:
	case <-testbudget.Context(t).Done():
		t.Fatal("the reload never sent the old generation its invalidation")
	}
	// The runtime has not read the invalidation: the handler goes on with a live ctx and makes its call.
	if err := os.WriteFile(filepath.Join(dir, "go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	served := awaitCondition(testbudget.Context(t), func() bool { return slices.Contains(recorded(), "held went on:info") })
	releaseFrame()
	if !served {
		t.Fatalf("the call a handler made before its runtime had read the reload's invalidation was not served: notifications %q", recorded())
	}
	for what, done := range map[string]chan error{"the held command": returned, "the reload": reloaded} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s: %v", what, err)
			}
		case <-testbudget.Context(t).Done():
			t.Fatalf("%s never returned", what)
		}
	}
}

// A hung extension must still be replaceable by a reload: its handler holds its thread, so its runtime never reads the invalidation, and it still answers its heartbeat. Pi is never in this state: its extensions run on the thread that runs reload(). The acknowledgement has the deadline of a pong. At the deadline:
//   - every request in flight on the extension has its one answer, an error that names the reload;
//   - the reload's report says in one line that the extension did not acknowledge the reload and was restarted, and the host reports no crash and disables nothing beside it;
//   - the reload loads the replacement, in a new process, and the replacement is served.
//
// The extensions of a packed Node cell share one thread, so while one of them holds it none acknowledges. Each is ended and loaded again the same way. One that lost no request has no line: the reload replaced it as it replaces every extension. One whose own handler was in flight lost it, has the same answer, and has its line.
// mutation-checked: with the ReloadDeadline branch of handleIncoming off, the host reports `extension "held" crashed, restart in 2s: extension_unresponsive: ...` for the isolated extension and `extension "held" disabled: ...` and `extension "neighbour" disabled: ...` for the packed ones; with the request's error built without the connection's ReloadDeadline (Conn.Request) the hang command is answered "did not answer heartbeat for request".
func TestConformance_AReloadReplacesAnExtensionWhoseRuntimeStoppedAnswering(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (runs a Node extension)")
	}
	t.Parallel()
	// The deadline in both texts is the host's pong deadline.
	const answer, line = "did not acknowledge a reload within 5s", "did not acknowledge the reload within 5s and was restarted"
	for _, tc := range []struct {
		name           string
		packed         bool
		neighbourWaits bool
		want           []string
	}{
		{name: "node-isolated", want: []string{`extension "held" ` + line}},
		{name: "node-packed, the neighbour idle", packed: true, want: []string{`extension "held" ` + line}},
		{name: "node-packed, the neighbour's handler in flight", packed: true, neighbourWaits: true, want: []string{`extension "held" ` + line, `extension "neighbour" ` + line}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			host, recorded, dir, load := reloadHeldHost(t, tc.packed, nil)
			var reportsMu sync.Mutex
			var reports []string
			host.SetCrashHandler(func(name string, delay time.Duration, disabled bool, reason string) {
				reportsMu.Lock()
				defer reportsMu.Unlock()
				reports = append(reports, subprocess.FormatCrashNotice(name, delay, disabled, reason))
			})
			t.Cleanup(func() { host.Shutdown("test done") })
			command := load()
			exists := func(name string) bool { _, err := os.Stat(filepath.Join(dir, name)); return err == nil }
			inFlight := map[string]chan error{}
			run := func(name, marker string) {
				t.Helper()
				answers := make(chan error, 2)
				inFlight[name] = answers
				handler := command(name).Handler
				go func() { answers <- handler(context.Background(), "") }()
				if !awaitCondition(testbudget.Context(t), func() bool { return exists(marker) }) {
					t.Fatalf("the %s command never ran", name)
				}
			}
			if tc.neighbourWaits {
				run("neighbour-wait", "neighbour-running")
			}
			run("hang", "running")
			// The reload: the hang command holds the thread of its process, and no runtime in that process acknowledges.
			command = load()
			for name, answers := range inFlight {
				select {
				case err := <-answers:
					if err == nil || !strings.Contains(err.Error(), answer) {
						t.Fatalf("the %s command in flight at the reload was answered %v, want an error that says the extension %s", name, err, answer)
					}
				case <-testbudget.Context(t).Done():
					t.Fatalf("the %s command in flight at the reload was never answered", name)
				}
			}
			if got := host.LastReloadReport().Unacknowledged; !slices.Equal(got, tc.want) {
				t.Fatalf("the reload's report has the lines %q, want %q", got, tc.want)
			}
			served := map[string]string{"ping": "pong:info"}
			if tc.packed {
				served["neighbour-ping"] = "neighbour pong:info"
			}
			for name, shown := range served {
				if err := command(name).Handler(context.Background(), ""); err != nil || !slices.Contains(recorded(), shown) {
					t.Fatalf("the reloaded generation's %s was not served: %v, notifications %q", name, err, recorded())
				}
			}
			for name, answers := range inFlight {
				select {
				case err := <-answers:
					t.Fatalf("the %s command has a second answer: %v", name, err)
				default:
				}
			}
			reportsMu.Lock()
			defer reportsMu.Unlock()
			if len(reports) != 0 {
				t.Fatalf("the host reported %q for extensions the reload replaced: the reload's line is the only report", reports)
			}
		})
	}
}
