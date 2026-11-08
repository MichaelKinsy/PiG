package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/next/telemetryingest"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
	"github.com/MichaelKinsy/PiG/chord/nextnode/workers"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
)

// Ports packages/chord/src/node/app/worker.ts
//
// `StartWorker` (slice 13 §1): a worker of an app built with the kit, in its entry. `WorkerMain` plus the kit's part: the worker's
// directory under its kind and address with its store, the host at the worker's address acting as the principal the server gave it,
// connected to the server first, its plugins loaded with the kit's worker facets (`WorkerInfo`, `Files`, `HostData`); when the server
// has a telemetry store, the host records in memory and sends its spans and metrics to the server every second (decision "The
// server is the one place telemetry is stored").
//
// Go forced differences (D460, D461, D462). `workerMain` returns at once in Node; StartWorker blocks (workers.WorkerMain does not
// return). The worker's plugins run in a Node subprocess (docs/plan/chord-plugins-node-evaluator.md), so the worker's own host is a
// relay without an address: the kit's Go facets load on it and the local types plugins use (WorkerInfo) reach them through
// plugins.BridgedFacet; the NodeHost serves the worker's address (D460). `Files` and `FilesFolder` are not bridged (the keyed member
// `summary` cannot be forwarded), so a TS plugin cannot get the worker's files (D461). The relay and the NodeHost have a store each
// (D462).
//
// Not ported yet (pending, no divergence): `principalAttributes` (T6 host spans: the Go host has no hook, so WorkerOptions has none);
// `reloadDrainTimeout` (not a Go host option); `chord.translations` is not loaded on a worker: the Node host the plugins run in
// carries no translations registry (Pi's `translationsFacet` is not in the vendored Pi next) and the Go registry is the worker host's
// own, which no plugin contributes to.

// WorkerOptions is what StartWorker takes: the app's API and kinds as the server's StartServer has them, this process's modules.
type WorkerOptions struct {
	API   AppAPI
	Kinds []AppKind
	// WorkerKinds are the app's worker host kinds: the one of the address the server starts places the worker's route.
	WorkerKinds []*contracts.HostKind
	// Busy is the app's extra keep-alive counts (a record of numbers, all zero when idle).
	Busy workers.BusyWatchable
	// OnError gets the worker host's errors (host.HostError) and a failed start's.
	OnError func(err any)
	// Node is how the worker's Node subprocess is started.
	Node plugins.NodeOptions
}

// workerQueue is how many finished spans a worker keeps while it cannot send (past it new ones are dropped).
const workerQueue = 10_000

// workerSend is how often a worker sends what it recorded.
const workerSend = time.Second

// StartWorker runs this process as a worker of the app: once the start message arrived, its host is up and its plugins loaded. It
// does not return.
func StartWorker(options WorkerOptions) {
	// The worker's facet kind comes with the start message: every kind's packages are offered, its own are imported.
	packages := plugins.HostPackages{}
	maps.Copy(packages, options.API.Modules)
	for _, kind := range options.Kinds {
		maps.Copy(packages, kind.Packages)
	}
	nodeOptions := options.Node
	workers.WorkerMain(func(start workers.WorkerStart, ctx context.Context) (*workers.WorkerResult, error) {
		return runWorker(options, start, ctx)
	}, workers.WorkerMainOptions{
		HostPackages: packages, Node: nodeOptions,
		// Worker errors are public (decision "worker errors in public"): a fixed message, never the error's own.
		Redact:  func(any) workers.CrossedError { return workers.CrossedError{Message: "worker failed"} },
		OnError: options.OnError,
	})
}

func runWorker(options WorkerOptions, start workers.WorkerStart, ctx context.Context) (*workers.WorkerResult, error) {
	// The server's StartServer sends `{ kit, app }`.
	message, _ := start.Start.(*delta.JsonObject)
	if message == nil {
		return nil, errors.New("startWorker: the start data is not {kit, app}")
	}
	kit, err := decodeKitStart(message.Value("kit"))
	if err != nil {
		return nil, err
	}
	app := message.Value("app")
	if err := PrivateDirectory(kit.Dir); err != nil {
		return nil, err
	}
	// Two stores, as two chord stores cannot share a file (each allocates document ids): `store.sqlite` is the plugins' (the NodeHost opens
	// it: StoreOptions), `kit.sqlite` the Go kit's (Files). An export packs both.
	storePath := filepath.Join(kit.Dir, archiveStore)
	if err := PrivateStore(storePath); err != nil {
		return nil, err
	}
	kitPath := filepath.Join(kit.Dir, kitStore)
	if err := PrivateStore(kitPath); err != nil {
		return nil, err
	}
	database, err := node.OpenNodeSqliteDatabase(kitPath, node.NodeSqliteDatabaseOptions{})
	if err != nil {
		return nil, err
	}
	opened, err := store.OpenStore(store.SqliteBackend(database), ctx, store.StoreOptions{})
	if err != nil {
		return nil, err
	}
	start.OnClose(func(closing context.Context) error { return opened.Close(closing) })
	var recorder *telemetryingest.Recorder
	hostOptions := host.HostOptions{
		Principal: kit.Principal, Store: opened, OnError: func(err host.HostError) { options.OnError(err) },
		CloseTimeout: kit.Timeouts.close(), ReadyTimeout: kit.Timeouts.ready(), ReloadDrainTimeout: kit.Timeouts.reloadDrain(),
	}
	if kit.Telemetry {
		scope := uuid.NewString()
		recorder = telemetryingest.NewRecorder(func() string { return scope }, workerQueue)
		hostOptions.Telemetry = recorder
	}
	// The worker's host is a relay: the NodeHost serves the address (WorkerPlugins.Load routes it), so it has none.
	relay, err := host.CreateHost(hostOptions)
	if err != nil {
		return nil, err
	}
	start.OnClose(func(closing context.Context) error { return relay.Close(closing) })
	// First: the server sees the worker started, then the load runs.
	if err := relay.Connect(start.Parent, host.LinkOptions{Principal: kit.Server, Trusted: true}, ctx); err != nil {
		return nil, err
	}
	var hostKind *contracts.HostKind
	for _, kind := range options.WorkerKinds {
		if kind.Kind == start.Address.Kind {
			hostKind = kind
		}
	}
	if hostKind == nil {
		return nil, fmt.Errorf("startWorker: the app has no worker kind %q", start.Address.Kind)
	}
	files := filepath.Join(kit.Dir, "files")
	// HostData is served by the NodeHost (the host of the worker's address): a core module written for this start.
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(secret)
	dataModule := filepath.Join(kit.Dir, ".host-data.mjs")
	if err := os.WriteFile(dataModule, []byte(hostDataModule(kit.Server, token)), 0o600); err != nil {
		return nil, err
	}
	goCore := []host.HostFacet{
		{Name: "chord.app.worker-info", Facet: WorkerInfoFacet(WorkerInfoValues{Kind: start.Address.Kind, Key: start.Address.Key, Params: start.Address.Params, Start: app})},
		{Name: "chord.app.files", Facet: FilesFacet(files, kit.Files, kit.Uploads)},
		{Name: "chord.app.data.export", Facet: HostDataFacet(kit.Dir, opened, token)},
	}
	if _, err := start.Plugins.Load(relay, workers.WorkerLoad{
		Kind: kit.Facet, HostKind: hostKind, GoCore: goCore,
		Core: []plugins.CoreFacet{{Name: "chord.app.data", Module: dataModule}},
		Bridged: []plugins.BridgedFacet{
			{Name: "chord.app.worker-info", Contracts: []*contracts.Contract{WorkerInfo}},
			{Name: "chord.app.data.export", Contracts: []*contracts.Contract{HostDataExport}},
		},
		Principal: kit.Principal, Peer: kit.Server,
		Store: &plugins.StoreOptions{Path: storePath}, CloseTimeout: kit.Timeouts.close(), ReadyTimeout: kit.Timeouts.ready(), ReloadDrainTimeout: kit.Timeouts.reloadDrain(),
	}, ctx); err != nil {
		return nil, err
	}
	if recorder != nil {
		sender, err := telemetryingest.SendWorkerTelemetry(relay, recorder, ctx)
		if err != nil {
			return nil, err
		}
		ticker := time.NewTicker(workerSend)
		stopped := make(chan struct{})
		done := make(chan struct{})
		//portlint:allow golifetime the sender's timer ends when the worker closes (the closer below stops it and joins)
		go func() {
			defer close(done)
			for {
				select {
				case <-ticker.C:
					sender.Send(false)
				case <-stopped:
					return
				}
			}
		}()
		// Held after the load (a host holds nothing before). Closers run in reverse: this one before the host closes, the last spans and
		// partial metrics sent while the link is up, for at most a second.
		start.OnClose(func(context.Context) error {
			ticker.Stop()
			close(stopped)
			<-done
			sent := make(chan struct{})
			//portlint:allow golifetime bounded by the select below; Send returns once the link ends
			go func() { sender.Send(true); close(sent) }()
			select {
			case <-sent:
			case <-time.After(workerSend):
			}
			sender.Stop()
			return nil
		})
	}
	result := &workers.WorkerResult{}
	if options.Busy != nil {
		result.Busy = options.Busy
	}
	return result, nil
}
