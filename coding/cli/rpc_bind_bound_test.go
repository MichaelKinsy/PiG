package cli

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// rpcHost.bind calls bound once the runner is filled and before the session_start handlers run (rpcShutdown.bound). Called earlier, a dispose that the signal started runs session_shutdown against a runner BindCore is still filling (race.208653); called after the handlers, a handler that never returns holds the shutdown, which Pi's shutdown() does not wait for. The Session's runner starts with a sentinel getCommands, and a session_start handler that blocks until the test releases it marks where the handlers run; bound reports whether the sentinel is gone and whether the handler had started, so the test needs no sleep.
func TestRPCBindCallsBoundAfterBindCoreAndBeforeSessionStartHandlers(t *testing.T) {
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer services.Close()
	runtime, err := coding.NewRuntime(coding.RuntimeOptions{Services: services})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runtime.Close() }()
	provider := ai.NewFauxProvider(ai.FauxConfig{})
	session, err := runtime.New(coding.SessionStartOptions{
		Model:            &ai.Model{ID: "faux-1", Provider: provider, ProviderMeta: ai.ProviderMetadata{ProviderID: provider.ID()}},
		SkipBuiltinTools: true,
		SessionDir:       t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}

	handlerStarted := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseHandler := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseHandler()
	runner := inproc.NewRunner([]extension.Extension{{Path: "hung-start", Handlers: map[string][]extension.HandlerFn{
		codingagent.EventSessionStart: {func(...any) (any, error) {
			close(handlerStarted)
			<-release
			return nil, nil
		}},
	}}}, services.CWD())
	const sentinel = "bound-before-bindcore"
	runner.BindCore(extension.ExtensionActions{GetCommands: func() []extension.SlashCommandInfo {
		return []extension.SlashCommandInfo{{Name: sentinel}}
	}}, extension.ContextActions{}, nil)
	session.ReplaceRunner(runner)

	type state struct{ coreBound, handlerRan bool }
	var atBound atomic.Pointer[state]
	boundCalled := make(chan struct{})
	h := &rpcHost{
		ctx: t.Context(), cancel: func() {}, rt: runtime,
		ui: newRPCUIContext(func(any) {}), writeRPC: func(any) {}, responses: &rpcResponseTurn{write: func(any) {}},
		promptWG: &sync.WaitGroup{}, tasks: &rpcTaskGroup{}, requestShutdown: func() {},
		conversionErr: make(chan error, 1), stdoutWait: func(context.Context) {},
	}
	defer h.ui.Close()
	h.bound = func() {
		st := &state{coreBound: true}
		for _, command := range runner.Runtime().ExtensionActions.GetCommands() {
			if command.Name == sentinel {
				st.coreBound = false
			}
		}
		select {
		case <-handlerStarted:
			st.handlerRan = true
		default:
		}
		atBound.Store(st)
		close(boundCalled)
	}
	bindDone := make(chan error, 1)
	go func() { bindDone <- h.bind(t.Context(), session, session.StartEvent(), false) }()

	select {
	case <-boundCalled:
	case <-handlerStarted:
		// A handler may start right after bound returned; one that started with no bound call is the held shutdown.
		select {
		case <-boundCalled:
		default:
			t.Fatal("a session_start handler ran before bound was called: a handler that never returns holds the shutdown")
		}
	case err := <-bindDone:
		t.Fatalf("bind returned (%v) without calling bound", err)
	}
	st := atBound.Load()
	if !st.coreBound {
		t.Error("bound was called before BindCore filled the runner: the dispose would emit session_shutdown against a runner that is still being bound")
	}
	if st.handlerRan {
		t.Error("bound was called after a session_start handler started")
	}
	// bind itself still runs the handler to its end.
	select {
	case <-handlerStarted:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("bind did not dispatch session_start")
	}
	releaseHandler()
	if err := <-bindDone; err != nil {
		t.Fatalf("bind: %v", err)
	}
}
