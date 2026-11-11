package hosts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/app"
	"github.com/MichaelKinsy/PiG/chord/nextnode/workers"
)

// Ports apps/p2/src/hosts/worker.ts: the session worker host, one process per running session, started by the server's
// workers component. Its host sits at the session's address on the session's Chord store
// (`<data>/sessions/<id>/store.sqlite`), connected to the server over IPC, and loads the worker facets of the selected
// plugins beside the core facet, which owns the session's Durable Harness (its records in that store), and the kit's
// `Files` over the session's files.
//
// Forced Go differences. The worker is the p2 executable run as `p2 worker` (the server forks it, D473): plugins are
// compiled in, so the worker loads the selection's facets itself (P2_PLUGINS) and no Node host starts. The worker's host
// is the Go host at the session's address, not a relay.

// WorkerArg is the argument that makes the p2 executable a session worker.
const WorkerArg = "worker"

// RunWorker runs this process as a session worker. It does not return.
func RunWorker() {
	// Before any provider request: see ConfigureFetch (HTTP/2 sessions can wedge an origin).
	p2runtime.ConfigureFetch()
	workers.WorkerMain(runWorker, workers.WorkerMainOptions{
		Redact: func(err any) workers.CrossedError {
			return workers.CrossedError{Message: p2runtime.ProcessLogs.Redactor.Redact(fmt.Sprint(err))}
		},
		OnError: func(err any) { fmt.Fprintln(os.Stderr, "p2 worker:", err) },
		Reload:  reloadWorker,
	})
}

func runWorker(start workers.WorkerStart, ctx context.Context) (*workers.WorkerResult, error) {
	init, err := p2runtime.ParseWorkerInit(start.Start)
	if err != nil {
		return nil, err
	}
	session, err := init.Session()
	if err != nil {
		return nil, err
	}
	var requested []string
	if value, ok := os.LookupEnv(PluginsEnv); ok {
		requested = splitNames(value)
	}
	selection, err := SelectPlugins(requested)
	if err != nil {
		return nil, err
	}
	p2runtime.ProcessLogs.Redactor.AddDirectory(init.DataDir)
	// Keep a redacted diagnostics copy without changing the worker's actual stdout/stderr writes.
	stopLogs := p2runtime.CaptureProcessLogs(SessionPrefix+session.ID, false)
	start.OnClose(func(context.Context) error { stopLogs(); return nil })
	// The session's directory: its store (Durable's records, its `Files` index) and its files. The outbox is off until p2
	// syncs. Closed after the host (closers run in reverse).
	dir, err := p2runtime.SessionDirectory(init.DataDir, session.ID)
	if err != nil {
		return nil, err
	}
	store, err := p2runtime.OpenHostStore(ctx, dir, false)
	if err != nil {
		return nil, err
	}
	start.OnClose(func(context.Context) error { return store.Close(chordctx.Background()) })
	address := start.Address
	worker, err := p2runtime.NewHost(host.HostOptions{
		Address:   &address,
		Principal: p2runtime.ToChord(p2runtime.Host),
		Store:     store,
		OnError: func(failure host.HostError) {
			fmt.Fprintf(os.Stderr, "p2 worker: %v\n", failure)
		},
	})
	if err != nil {
		return nil, err
	}
	liveness := p2runtime.NewLiveness()
	start.OnClose(func(context.Context) error {
		liveness.Stop()
		return worker.Close(chordctx.Background())
	})
	// Connect first: the server sees the worker started; the server's own requests act as SERVER, those it relays as their
	// caller.
	if err := worker.Connect(start.Parent, host.LinkOptions{Principal: p2runtime.ToChord(p2runtime.Server), Trusted: true}, ctx); err != nil {
		return nil, err
	}
	if err := liveness.FollowActivity(worker); err != nil {
		return nil, err
	}
	// Where the session works: the spawn location, then each in-place relocation (the Harness's `Relocate`).
	location := p2runtime.NewSessionLocation(p2runtime.SpawnLocation(session))
	root := func(fn func(ctx host.Ctx) (any, error), rootCtx context.Context) (any, error) {
		return worker.Root(fn, rootCtx, host.RootOptions{})
	}
	facets := []host.HostFacet{
		{Name: "@p2/core", Facet: p2runtime.WorkerCore(init, session, liveness, location, store, root)},
		// The session's files (the kit's `Files`): `<data>/sessions/<id>/files/`.
		{Name: "chord.app.files", Facet: app.FilesFacet(p2runtime.HostFiles(dir), app.FilesCaps{}, 0)},
	}
	facets = append(facets, workerFacets(selection)...)
	report, err := worker.Load(facets, ctx)
	if err == nil && len(report.Failed) > 0 {
		err = report.Failed[0].Error
	}
	// Every plugin is contributed (codemode's tool among them): request-time users of the Harness, the server's `send` routed
	// at started, may now proceed (p2runtime.LoadGate); a failed Load answers them with its error.
	finishLoad(ctx, worker, err)
	if err != nil {
		return nil, err
	}
	setReloader(&workerReloader{host: worker, facets: facets, selection: selection, applied: map[string]string{}})
	// A Harness that cannot open fails the worker (its error, with Retry), as an unknown root model does: at its first
	// failed attempt (`get` would retry it until its ready timeout, the start meanwhile hanging).
	if err := openedHarness(ctx, worker); err != nil {
		return nil, err
	}
	// Every plugin is loaded (its extensions contributed): scheduling may start. An archived session started only to
	// rebuild its search index is scanned without resuming its work.
	source, err := worker.Registry(ctx, p2runtime.HarnessNative, host.GetOptions{})
	if err != nil {
		return nil, err
	}
	if parts, _ := source.Value().(*p2runtime.HarnessParts); parts != nil && (session.Resume == nil || *session.Resume) {
		durable, err := parts.Durable()
		if err != nil {
			return nil, err
		}
		durable.Resume()
	}
	return &workers.WorkerResult{Busy: liveness}, nil
}

// finishLoad opens the Harness's LoadGate with the outcome of the worker's Load (the gate exists once the Harness is opened;
// before that nothing can wait on it).
func finishLoad(ctx context.Context, worker host.Host, outcome error) {
	source, err := worker.Registry(ctx, p2runtime.HarnessNative, host.GetOptions{})
	if err != nil {
		return
	}
	if parts, _ := source.Value().(*p2runtime.HarnessParts); parts != nil {
		parts.Loaded.Finish(outcome)
	}
}

// openedHarness waits for the HarnessHost object to go live, failing with its first failure.
func openedHarness(ctx context.Context, worker host.Host) error {
	ref, err := worker.Hold(ctx, p2runtime.HarnessHost, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		return err
	}
	defer ref.Release()
	settled := make(chan error, 1)
	stop := p2runtime.Follow(ref.State(), func(state *host.RefState) {
		var outcome error
		switch state.Kind {
		case host.RefStateLive:
		case host.RefStateFailed:
			if state.Cause.Error != nil {
				outcome = state.Cause.Error
			} else {
				outcome = errors.New("the Harness failed to open")
			}
		case host.RefStateEnded:
			if state.Error != nil {
				outcome = state.Error
			} else {
				outcome = errors.New("the Harness ended")
			}
		default:
			return
		}
		select {
		case settled <- outcome:
		default:
		}
	})
	defer stop()
	select {
	case err := <-settled:
		return err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// splitNames is a comma-separated list; a variable set to the empty string is the empty selection.
func splitNames(value string) []string {
	if value == "" {
		return []string{}
	}
	return strings.Split(value, ",")
}
