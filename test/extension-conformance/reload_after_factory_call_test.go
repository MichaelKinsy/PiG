package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"reflect"
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

// factoryCallReloadCase is one realization of the conformance fixture and the way its host loads it, the first time and again.
type factoryCallReloadCase struct {
	name string
	// loader returns the function that loads the fixture into host; a second call loads it again, as /reload does.
	loader func(t *testing.T, host *subprocess.Host) func() ([]extension.Extension, error)
}

func factoryCallReloadCases() []factoryCallReloadCase {
	var cases []factoryCallReloadCase
	for _, tc := range viewReloadCases() {
		cases = append(cases, factoryCallReloadCase{name: tc.name, loader: func(t *testing.T, host *subprocess.Host) func() ([]extension.Extension, error) {
			configs := tc.configs(t)
			host.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return slices.Clone(configs), nil })
			return func() ([]extension.Extension, error) {
				loaded, err := host.Reload(context.Background())
				if err == nil && len(loaded) != len(configs) {
					err = fmt.Errorf("%d of %d extensions loaded (%+v)", len(loaded), len(configs), host.LastReloadReport())
				}
				return loaded, err
			}
		}})
	}
	// A fused extension runs in the host's process over an in-memory connection: its factory's listener is registered on that connection before the register frame, and the wait for ready reads what the host sends in between.
	return append(cases, factoryCallReloadCase{name: "go-fused", loader: func(_ *testing.T, host *subprocess.Host) func() ([]extension.Extension, error) {
		sdk.SetHostHelpers(fusepack.HostHelpers())
		return func() ([]extension.Extension, error) {
			loaded, err := host.LoadInProcess(context.Background(), subprocess.ExtConfig{Name: "sdk-fixture", Enabled: true}, func(conn net.Conn) error { return testfixture.Extension().RunWithConn(conn) })
			if err != nil {
				return nil, err
			}
			return []extension.Extension{*loaded}, nil
		}
	}})
}

// stateBeforeReady records the state frames the Host writes to an extension before the first ready it writes to it.
type stateBeforeReady struct {
	mu    sync.Mutex
	ready map[string]bool
	early []string
}

func (s *stateBeforeReady) observe(extName string, frame []byte) {
	var env struct {
		Type   string `json:"type"`
		Notify *struct {
			Method string `json:"method"`
		} `json:"notify"`
	}
	if json.Unmarshal(frame, &env) != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case env.Type == subprocess.MsgReady:
		if s.ready == nil {
			s.ready = map[string]bool{}
		}
		s.ready[extName] = true
	case env.Type == subprocess.MsgNotify && env.Notify != nil && (env.Notify.Method == "state_update" || env.Notify.Method == "run_signal") && !s.ready[extName]:
		s.early = append(s.early, extName+": "+env.Notify.Method)
	}
}

func (s *stateBeforeReady) frames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.early)
}

// TestConformance_AFactoryHostCallThenAReload pins, in every SDK (Lisp packed) and in the isolated, packed and fused (Go) realizations, what an extension
// whose factory makes a host call sees when it loads, and loads again, after the host's state changed. Every fixture subscribes on the event bus in its
// factory: a native SDK sends that subscription to the Host before it registers (a Node realm keeps it in its heap while it is the only realm). The Host
// answered such a call with a flush of its state ahead of the result, on a connection that had no ready yet; the Go SDK took that frame while the factory
// loaded and never counted it, so the first host call of a handler waited for it for ever (pig stayed at "Reloading ..." after /reload), and a fused Go
// extension failed its load on it.
//
// The row requires: the Host writes no state to a connection before its ready; the load, session_start and a command's host call return; and the replica a
// handler reads right after the ready is the host's state at the ready, a model, session id, name and file no SDK defaults to.
func TestConformance_AFactoryHostCallThenAReload(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	for _, tc := range factoryCallReloadCases() {
		t.Run(tc.name, func(t *testing.T) {
			notify, status, actions := &[]string{}, &[]string{}, &[]string{}
			ui := newRecordingUI(notify, status)
			bridge := subprocess.NewUIBridge(func() {})
			bridge.SetUIContext(ui)
			bridge.SetActions(conformanceActions(actions))
			state := &replicaHostState{model: replicaModels["a"], session: "replica-session-one", tokens: 777, window: 100000}
			state.bind(bridge)
			host := subprocess.NewHost(t.TempDir())
			host.SetHostInfo(conformanceHostInfo)
			host.SetUIBridge(bridge)
			early := &stateBeforeReady{}
			host.SetOutboundObserver(early.observe)
			t.Cleanup(func() { host.Shutdown("test done") })
			load := tc.loader(t, host)
			ctx := context.Background()
			// returns runs step and fails the test when it has not returned within the wait budget: a handler whose host call never returns never ends.
			returns := func(what string, step func() error) {
				t.Helper()
				done := make(chan error, 1)
				go func() { done <- step() }()
				select {
				case err := <-done:
					if err != nil {
						t.Fatalf("%s: %v", what, err)
					}
				case <-testbudget.Context(t).Done():
					t.Fatalf("%s did not return within the test's wait budget (%s): a host call of the extension never returned", what, testbudget.Env)
				}
			}
			// loadAndRead loads the fixture, runs session_start as pig does after a load, and requires that the replica-probe command reads the host's state.
			loadAndRead := func(reason, model, session string) {
				t.Helper()
				var loaded []extension.Extension
				returns("the load ("+reason+")", func() (err error) {
					loaded, err = load()
					return err
				})
				runner := inproc.NewRunner(loaded, t.TempDir())
				bridge.SetUIPromptScope(runner)
				before := len(ui.Recorded())
				returns("session_start("+reason+")", func() error {
					_, err := runner.Emit(ctx, extension.SessionStartEvent{Type: "session_start", Reason: reason})
					return err
				})
				if notes := ui.Recorded()[before:]; !slices.Contains(notes, "session_start:"+reason+":info") {
					t.Fatalf("session_start(%s) notified %v, want the handler's session_start:%s", reason, notes, reason)
				}
				command, ok := findCommand(runner, "replica-probe")
				if !ok {
					t.Fatal("replica-probe command missing")
				}
				before = len(ui.Recorded())
				returns("replica-probe after the "+reason, func() error { return command.Handler(runner.DispatchContext(ctx), "") })
				notes := ui.Recorded()[before:]
				if len(notes) != 1 {
					t.Fatalf("replica-probe after the %s notified %v, want its one report", reason, notes)
				}
				var report replicaReport
				if err := json.Unmarshal([]byte(strings.TrimSuffix(notes[0], ":info")), &report); err != nil {
					t.Fatalf("report %q: %v", notes[0], err)
				}
				want := replicaReport{Model: replicaWant[model], SessionID: session, ManagerSessionID: session, Usage: state.usage(),
					SessionName: replicaOptional(replicaNameOf(session)), SessionFile: replicaOptional(replicaFileOf(session)), ManagerSessionName: replicaOptional(replicaNameOf(session)), ManagerSessionFile: replicaOptional(replicaFileOf(session))}
				if !reflect.DeepEqual(report, want) {
					t.Errorf("the replica after the %s\n got  %+v\n want %+v", reason, report, want)
				}
			}

			// A host state change before the first load leaves the new connection behind it, as one before a reload does. No instance of the fixture is
			// connected yet, so every state frame before a ready belongs to the connection the load opens.
			bridge.HostStateChanged()
			loadAndRead("startup", "a", "replica-session-one")
			if frames := early.frames(); len(frames) != 0 {
				t.Errorf("the Host wrote state to a connection before its ready: %v", frames)
			}

			// The model is switched and the Session replaced, and then the extensions are reloaded: the instance the reload loads reads the new state.
			state.set(replicaModels["b"], "replica-session-two", 4242)
			bridge.HostStateChanged()
			loadAndRead("reload", "b", "replica-session-two")
		})
	}
}
