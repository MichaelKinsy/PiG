package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/internal/parking"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/next/surfaces"
	"github.com/MichaelKinsy/PiG/chord/next/telemetryingest"
	chordhttp "github.com/MichaelKinsy/PiG/chord/nextnode/http"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/build"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
	nodesurfaces "github.com/MichaelKinsy/PiG/chord/nextnode/surfaces"
	"github.com/MichaelKinsy/PiG/chord/nextnode/workers"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/src/node/app/app.ts
//
// `StartServer` (slice 13 §1, §5; decisions "The app kit, point 1" to "point 5"): the server of an app built with the kit. It lays out
// the data directory, opens the server's store, creates the server host, the HTTP component and one workers component per worker
// kind, the plugins component over the app's required root and the user's root, loads the server's plugins with the kit's own facets
// (the start-data object, sign-ins, the owner link, pairing, files), and gives the app the controls it needs: the owner link,
// `ApplyChanges`, export and import per address.
//
// Forced Go differences (D460, D462). The plugins run in a Node subprocess (docs/plan/chord-plugins-node-evaluator.md): the kit's
// facets are Go core facets on the server's Go host, the server's plugins' host is the NodeHost the server host delegates to (D605: the remote types placed on the server that no Go facet serves go over its link),
// and the local types plugins use (AppInfo, SignIns) reach the Go facets through plugins.BridgedFacet (D460); so do `Files` and `FilesFolder`, `Files`'
// keyed member `summary` as a copy the Node host keeps of the Go object's documents. The server's host and the plugins' NodeHost are two hosts with a store each
// (D462). The server's own code is its executable, recorded after the listen (`BaseWatched`) where Pi compares the static import graph
// of its start script; one changed since the process started is marked as a base change until a restart (`editedSinceStart`); one under the Go build cache (`go run`, `go test`) is never compared, as Pi skips an entry under `node_modules`;
// one that cannot be read is reported as `server_scan` and the server runs on. A start script's lazy optional imports have no Go form
// (language mechanics: an executable has no import graph).
// `startServer`'s generic principal parameter is designed out. `principal.worker` is a function that may be nil only without
// worker kinds.
//
// Not ported yet (pending, no divergence): the `principalAttributes` hook (T6 host spans: the Go host has none).

// A Go worker is the program's own executable, so Pi's esbuild bundle of the worker's Node entry (base.ts, child.ts) has no Go
// form: the worker base is a copy of the executable made by `Workers.Build` into `cache/workers/<kind>/`.

// Server is the server's host kind: the kit's server host is Server().
var Server = mustHostKind("server")

func mustHostKind(name string, props ...contracts.Property) *contracts.HostKind {
	kind, err := contracts.NewHostKind(name, props...)
	if err != nil {
		panic(err)
	}
	return kind
}

// AppPlugins are the plugins of a running app, for its "apply changes" control and its installs.
type AppPlugins struct {
	// State is the marks (`dirty`, `install`, `set`, `base`): what the app's button shows.
	State chord.Watchable[delta.JsonValue]
	// Install installs plugins from git or npm into the user's root (slice 12's `installPlugin`) and returns their names.
	Install func(ctx context.Context, source plugins.PluginSource) ([]string, error)
	// Component is the plugins component itself.
	Component *plugins.Plugins
}

// AppAPI is the app's API (slice 12): its package name, the versions it provides (newest first) and this process's modules of them by
// specifier (`@acme/app/v3`, `@acme/app/v3/server`).
type AppAPI struct {
	Name     string
	Versions []string
	Modules  plugins.HostPackages
}

// AppKind is a facet kind of the app: where its builds run, and the packages besides the API its plugins share (`react`).
// Its API modules are named by convention: `<api>/<version>` and `<api>/<version>/<kind>` for every version.
type AppKind struct {
	Platform model.Platform
	Packages plugins.HostPackages
}

// AppKindEntry is one entry of upstream's `kinds` record: the kind's name, the record's key, and its AppKind. A record keeps its keys
// in insertion order and a Go map does not, so the kinds are a slice of entries in the order the app gave them.
type AppKindEntry struct {
	Name string
	Kind AppKind
}

// AppWorkerKind is a worker kind: a host kind of the app and how its workers start (the workers component's options).
type AppWorkerKind struct {
	// Kind is the host kind (`hostKind("project", { id: str() })`).
	Kind *contracts.HostKind
	// Entry is the worker's executable (it calls StartWorker); ExecArgv and Env its arguments and environment.
	Entry    string
	ExecArgv []string
	Env      map[string]string
	// Facet is the facet kind its workers load: `worker` by default.
	Facet string
	// Start is the app's start data for the worker at address (JSON), before each start.
	Start func(address contracts.Address, ctx context.Context) (delta.JsonValue, error)
	// Authorize is whether principal may reach the worker at address.
	Authorize func(principal data.Principal, address contracts.Address) bool
	// IdleMs is how long a worker idles before it stops: 5 minutes by default.
	IdleMs func() time.Duration
}

// AppErrorKind tags an AppError.
type AppErrorKind string

const (
	AppErrorHost       AppErrorKind = "host"
	AppErrorHTTP       AppErrorKind = "http"
	AppErrorWorkers    AppErrorKind = "workers"
	AppErrorPlugins    AppErrorKind = "plugins"
	AppErrorServerScan AppErrorKind = "server_scan"
	// AppErrorBaseBuild is a base that did not build after the start (AppError.Base is `web`, `tui` or the worker kind's name): the
	// server runs on, the page answers 503, the kind's workers fail their start.
	AppErrorBaseBuild AppErrorKind = "base_build"
	// AppErrorInterruptedDataChange is the start's resolution of an import or a delete an earlier process left under way (a kill between
	// the two commits of its server part, or before it ended): Error says whether it was undone (an import) or completed (a delete), or
	// why it could not be resolved, in which case its address does not start and refuses its import, delete and export until it is. Pi has
	// no such report: its import and delete are one commit. Beyond Pi (D462).
	AppErrorInterruptedDataChange AppErrorKind = "interrupted_data_change"
)

// AppError is what the kit reports: from its host (host.HostError), its HTTP component (chordhttp.HttpError), its workers
// (workers.WorkersError) or its plugins (plugins.PluginsError), a base that did not build after the start (`base_build`: Base names
// it; the server runs on), or a failed scan of the server's own code (`server_scan`: the server runs on, that code is not compared).
type AppError struct {
	Kind  AppErrorKind
	Error any
	// Base is the base that did not build, for `base_build`.
	Base string
}

// AppPrincipals are the server's principal, each worker's (stamped on its link; only with worker kinds) and the owner's: whom the owner
// link, the owner token file and device pairing sign in, and who approves pairings and makes invites (AppInfo.owner). The owner must
// be strict JSON and not the server's principal (refused at start).
type AppPrincipals struct {
	Server data.Principal
	Worker func(address contracts.Address) data.Principal
	Owner  data.Principal
}

// AppLimits are the bounds of what public endpoints, clients and imports may cost (0 is the default): live ephemeral sign-ins
// (1,000; past it the oldest ends, signed out), pairings under way that anonymous clients started (100 in all, 10 per client address;
// past them 429; the owner's own are not counted), uploads under way per `Files` object (64; past it `unavailable`, `busy`), an
// archive's unpacked bytes (4 GiB) and entries (100,000).
type AppLimits struct {
	Uploads            int
	EphemeralSignIns   int
	PendingPairings    int
	PairingsPerAddress int
	ImportBytes        int64
	ImportEntries      int
}

// AppTimeouts are the hosts' timeouts (`HostOptions.closeTimeout`, `HostOptions.readyTimeout`, `HostOptions.reloadDrainTimeout`):
// zero is the host's default.
type AppTimeouts struct {
	CloseTimeout time.Duration
	ReadyTimeout time.Duration
	// ReloadDrainTimeout bounds how long a reload's restart keeps an object's old attempt open for old work.
	ReloadDrainTimeout time.Duration
}

func (t AppTimeouts) close() *time.Duration       { return optionalDuration(t.CloseTimeout) }
func (t AppTimeouts) ready() *time.Duration       { return optionalDuration(t.ReadyTimeout) }
func (t AppTimeouts) reloadDrain() *time.Duration { return optionalDuration(t.ReloadDrainTimeout) }

func optionalDuration(d time.Duration) *time.Duration {
	if d == 0 {
		return nil
	}
	return &d
}

// AppTelemetry is the app's telemetry store (§29; decision "The server is the one place telemetry is stored"): the server's host and the
// kit's ingest record to it. With one, every worker records in memory and sends its spans and metrics to the server's
// `WorkerTelemetry`.
type AppTelemetry interface {
	telemetry.TelemetryContext
	telemetryingest.IngestTarget
}

// AppOptions is what StartServer takes.
type AppOptions struct {
	// Name is the app's name: its default data directory is `~/.<name>`, its archives carry it.
	Name string
	// DataDir is the data directory (the start script's to decide; no environment variables): `~/.<name>` by default.
	DataDir string
	// Root is the app's built-in plugins root (required): `plugins/` of the working directory by default.
	Root string
	// Listen is where the server listens: `127.0.0.1`, a free port by default.
	Listen *chordhttp.ListenAddress
	API    AppAPI
	// Kinds are the app's facet kinds, in order: `server` and each worker kind's facet kind among them.
	Kinds     []AppKindEntry
	Principal AppPrincipals
	// Workers are the worker kinds; none: a server with clients only. They need Principal.Worker.
	Workers []AppWorkerKind
	// Files are the caps of each host's files (the server's and every worker's).
	Files  FilesCaps
	Limits AppLimits
	// Install is how installs run npm.
	Install plugins.InstallOptions
	// Facets are facets the server host loads beside the kit's own (language mechanics): the Go form of what Pi's app does with a facet that has
	// settings (`ephemeralLoginFacet()`, `inviteFacet()`, `defineUsers(...)`), which it re-exports through its API's server module
	// and loads with a one-line plugin. A plugin directory is TypeScript the Node plugin host evaluates and cannot import a Go
	// facet. Each is named; its name must not be one of the kit's (`chord.…`).
	Facets []host.HostFacet
	// Telemetry is the app's telemetry store; nil: none.
	Telemetry AppTelemetry
	// PrincipalAttributes is who a span was for (`createHost`'s `principalAttributes`): the server's hook, also stamped on the spans
	// pages send (the principal of their link). Nil: none.
	PrincipalAttributes func(principal data.Principal) telemetry.SpanAttributes
	// Web is the web surface (slice 11): the app's web entry, its page template, the paths serving the page; the `web` facet kind.
	// Nil: no web surface.
	Web *AppWeb
	// Tui is the terminal surface (slice 11): the app's terminal entry; the `tui` facet kind. Nil: no terminal clients.
	Tui *AppTui
	// Base is the app's base code beside what the kit knows (files or directories: its API modules, its start script), hashed at start.
	Base []string
	// Timeouts are the `closeTimeout`, `readyTimeout` and `reloadDrainTimeout` of every host (the server's and each worker's).
	Timeouts AppTimeouts
	// Node is how the Node subprocesses that host the plugins are started.
	Node    plugins.NodeOptions
	OnError func(AppError)

	// executable is the server's own code: os.Executable unless a test names another.
	executable func() (string, error)
	// processStart is when the process started, for the executable's edit since then: the program's start unless a test names another.
	processStart func() time.Time
}

// KitStart is what StartWorker reads from its start data besides the app's: the kit's part.
type KitStart struct {
	Dir       string
	Facet     string
	Principal data.Principal
	Server    data.Principal
	Files     FilesCaps
	// Telemetry is whether the server has a telemetry store: the worker then sends what it records to `WorkerTelemetry`.
	Telemetry bool
	// Uploads is the cap of uploads under way per `Files` object (`limits.uploads`).
	Uploads  int
	Timeouts AppTimeouts
}

// json is the start data's kit part as JSON.
func (k KitStart) json() *delta.JsonObject {
	files := delta.NewJsonObject(0)
	if k.Files.MaxFile != 0 {
		files.Set("maxFile", float64(k.Files.MaxFile))
	}
	if k.Files.MaxTotal != 0 {
		files.Set("maxTotal", float64(k.Files.MaxTotal))
	}
	timeouts := delta.NewJsonObject(0)
	if k.Timeouts.CloseTimeout != 0 {
		timeouts.Set("closeTimeout", float64(k.Timeouts.CloseTimeout/time.Millisecond)) //portlint:allow numbers Go-to-Go start data: whole milliseconds the kit wrote itself
	}
	if k.Timeouts.ReadyTimeout != 0 {
		timeouts.Set("readyTimeout", float64(k.Timeouts.ReadyTimeout/time.Millisecond)) //portlint:allow numbers Go-to-Go start data: whole milliseconds the kit wrote itself
	}
	if k.Timeouts.ReloadDrainTimeout != 0 {
		timeouts.Set("reloadDrainTimeout", float64(k.Timeouts.ReloadDrainTimeout/time.Millisecond)) //portlint:allow numbers Go-to-Go start data: whole milliseconds the kit wrote itself
	}
	object := delta.JsonObjectOf("dir", k.Dir, "facet", k.Facet, "principal", k.Principal, "server", k.Server, "files", files, "telemetry", k.Telemetry)
	if k.Uploads != 0 {
		object.Set("uploads", float64(k.Uploads))
	}
	object.Set("timeouts", timeouts)
	return object
}

// decodeKitStart reads the kit's part of a worker's start data.
func decodeKitStart(value delta.JsonValue) (KitStart, error) {
	object, ok := value.(*delta.JsonObject)
	if !ok {
		return KitStart{}, errors.New("startWorker: the start data has no kit part")
	}
	var kit KitStart
	kit.Dir, _ = object.Value("dir").(string)
	kit.Facet, _ = object.Value("facet").(string)
	kit.Principal, _ = object.Value("principal").(*delta.JsonObject)
	kit.Server, _ = object.Value("server").(*delta.JsonObject)
	kit.Telemetry, _ = object.Value("telemetry").(bool)
	if files, ok := object.Value("files").(*delta.JsonObject); ok {
		if n, ok := files.Value("maxFile").(float64); ok {
			kit.Files.MaxFile = int64(n) //portlint:allow numbers Go-to-Go start data: a whole number KitStart.json wrote from an int
		}
		if n, ok := files.Value("maxTotal").(float64); ok {
			kit.Files.MaxTotal = int64(n) //portlint:allow numbers Go-to-Go start data: a whole number KitStart.json wrote from an int
		}
	}
	if n, ok := object.Value("uploads").(float64); ok {
		kit.Uploads = int(n) //portlint:allow numbers Go-to-Go start data: a whole number KitStart.json wrote from an int
	}
	if timeouts, ok := object.Value("timeouts").(*delta.JsonObject); ok {
		if n, ok := timeouts.Value("closeTimeout").(float64); ok {
			kit.Timeouts.CloseTimeout = time.Duration(n) * time.Millisecond //portlint:allow numbers Go-to-Go start data: whole milliseconds KitStart.json wrote
		}
		if n, ok := timeouts.Value("readyTimeout").(float64); ok {
			kit.Timeouts.ReadyTimeout = time.Duration(n) * time.Millisecond //portlint:allow numbers Go-to-Go start data: whole milliseconds KitStart.json wrote
		}
		if n, ok := timeouts.Value("reloadDrainTimeout").(float64); ok {
			kit.Timeouts.ReloadDrainTimeout = time.Duration(n) * time.Millisecond //portlint:allow numbers Go-to-Go start data: whole milliseconds KitStart.json wrote
		}
	}
	if kit.Dir == "" || kit.Facet == "" {
		return KitStart{}, errors.New("startWorker: the start data's kit part has no dir or facet")
	}
	return kit, nil
}

// serverLock is how long a second server waits for the data directory's lock before it is refused.
const serverLock = time.Second

// addressLock is how long an export or import waits for a worker's lock: past it a worker holds it (it runs or starts).
const addressLock = 2 * time.Second

// App is a running app.
type App struct {
	// Host is the server's host.
	Host host.Host
	// HTTP is the HTTP component.
	HTTP *chordhttp.HttpComponent
	// Workers are the workers components, by host kind name.
	Workers map[string]*workers.Workers
	// Plugins is the plugins of the running app, for its "apply changes" control and its installs.
	Plugins AppPlugins
	// Node is the server's plugins' host (the NodeHost the server host accepted the link of).
	Node *plugins.NodeHost
	// nodeData is Node's host data operations (the seam of the tests).
	nodeData nodeHostData
	// DataDir is the data directory.
	DataDir string
	// Web is the web surface, when the app has one: its base bundle's hash once built (BasesBuilt).
	Web *AppSurface
	// Tui is the terminal surface, when the app has one: its base bundle's hash once built.
	Tui *AppSurface
	// OwnerTokenFile is the owner token file's path (a device sign-in of the owner, mode 0600).
	OwnerTokenFile string

	// blocked are the addresses whose interrupted import or delete the start could not resolve (D462).
	blocked blockedAddresses
	store   store.Store
	lock    *workers.ProcessLockHandle
	options AppOptions
	link    string
	// surfaces are the client surfaces, filled before the server's load.
	surfaces []surface

	// build and rollout are the plugins component's Build and Rollout, the two steps ApplyChanges runs (Pi's tests replace the
	// component's `build` and `rollout` to hold one: a test here replaces these two, before it applies anything).
	build   func(names []string, ctx context.Context) (*plugins.Built, error)
	rollout func(steps []plugins.RolloutStep, built *plugins.Built, ctx context.Context) (plugins.RolloutResult, error)

	close  sync.Once
	closed error
	// closing is set when the close starts: what runs after the start (the bases' builds, the scan) stops reporting there.
	closing atomic.Bool
	// basesStarted is set once startBases started the goroutine basesBuilt and baseWatched are closed by: Wait joins it.
	basesStarted atomic.Bool
	// basesBuilt and baseWatched are BasesBuilt's and BaseWatched's channels, closed by the build after the listen (startBases).
	basesBuilt, baseWatched chan struct{}
	// ended is closed when the close starts.
	ended chan struct{}
}

// OwnerLink is the owner link of this start (single use, 10 minutes): `http://127.0.0.1:<port>/_chord/owner#<token>`.
func (a *App) OwnerLink() string { return a.link }

// StartServer starts the app's server (§1): returns once its plugins are loaded and it listens. A failing plugin of the app's root
// (or a problem discovery reports there) fails the start with everything closed.
func StartServer(options AppOptions, ctx context.Context) (*App, error) {
	if err := checkOwner(options.Principal.Owner, options.Principal.Server, ctx); err != nil {
		return nil, err
	}
	if len(options.Workers) > 0 && options.Principal.Worker == nil {
		return nil, errors.New("startServer: worker kinds need principal.worker")
	}
	for _, kind := range options.Workers {
		if kind.Authorize == nil || kind.Start == nil {
			return nil, fmt.Errorf("startServer: worker kind %q needs Authorize and Start", workerKindName(kind))
		}
	}
	dataDir := options.DataDir
	if dataDir == "" {
		dataDir = DefaultDataDir(options.Name)
	}
	absolute, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	opened, err := openServer(absolute, ctx)
	if err != nil {
		return nil, err
	}
	app := &App{basesBuilt: make(chan struct{}), baseWatched: make(chan struct{}), ended: make(chan struct{}), Workers: map[string]*workers.Workers{}, DataDir: opened.layout.DataDir, OwnerTokenFile: opened.layout.OwnerToken, store: opened.store, lock: opened.lock, options: options}
	if err := app.start(opened, ctx); err != nil {
		return nil, errors.Join(err, app.Close(ctx), app.Wait(ctx))
	}
	return app, nil
}

func (a *App) report(kind AppErrorKind, err any) {
	if a.options.OnError != nil {
		a.options.OnError(AppError{Kind: kind, Error: err})
	}
}

// serverExecutable is the path of the server's own executable, to compare like a base file (Pi's `nodeBaseInputs(process.argv[1])`, workers.NodeBaseInputs):
// not one under the Go build cache (the analog of an entry under `node_modules`: a test runner, `go run`), and one that cannot be
// read is reported as `server_scan` and left out.
func (a *App) serverExecutable() (model.BaseInputs, bool) {
	lookup := a.options.executable
	if lookup == nil {
		lookup = os.Executable
	}
	exe, err := lookup()
	if err == nil {
		exe, err = filepath.Abs(exe)
	}
	// A program of the Go build cache is not compared (and not read): its bytes are no one's code.
	if err == nil && inGoBuildCache(exe) {
		if _, statErr := os.Stat(exe); statErr == nil {
			return nil, false
		}
	}
	var inputs model.BaseInputs
	if err == nil {
		// Readable now; the record is handed on, the watcher does not hash it again.
		inputs, err = workers.NodeBaseInputs(exe, nil)
	}
	if err != nil {
		a.reportLate(AppErrorServerScan, fmt.Errorf("the server's executable cannot be read: %w", err), "")
		return nil, false
	}
	if inGoBuildCache(exe) {
		return nil, false
	}
	return inputs, true
}

// inGoBuildCache is whether path lies under a work directory `go run` and `go test` build their programs in: `go-build` and the
// digits the go command appends to it.
func inGoBuildCache(path string) bool {
	for element := range strings.SplitSeq(filepath.ToSlash(path), "/") {
		if digits, ok := strings.CutPrefix(element, "go-build"); ok && strings.Trim(digits, "0123456789") == "" {
			return true
		}
	}
	return false
}

// loadingHost is the server host as its HTTP component sees it. Pi's one server host loads the kit's facets and the plugins in one
// `load`, and until that ended Pi's HTTP component refuses every request and upgrade with 503 (http.ts `#resolve`: the registries
// are `not_ready`). Here the plugins load in the NodeHost after the server host's own facets (D605), so the host's registries
// would resolve while the plugins are still loading: a link connected then and held the scripts object's empty set, whose base is
// none a page runs (a page reconnecting to a restarted server reloaded). The registries are `not_ready` to the component until
// loaded is closed (the NodeHost's load ended), as before the host's `Load`.
type loadingHost struct {
	host.Host
	loaded <-chan struct{}
}

// Registry is the host's registry once the server's plugins are loaded; `not_ready` before.
func (h loadingHost) Registry(ctx context.Context, r registries.Registry[any, any], options host.GetOptions) (*registries.RegistryValueSource, error) {
	select {
	case <-h.loaded:
		return h.Host.Registry(ctx, r, options)
	default:
		return nil, data.NewChordError(data.ChordErrorInit{Code: data.CodeNotReady, Message: "app: the server's plugins are not loaded yet"}, nil)
	}
}

func (a *App) start(opened openedServer, ctx context.Context) error {
	options := a.options
	limits := options.Limits
	signIns := CreateSignIns(opened.cookieSecret, SignInLimits{MaxEphemeral: limits.EphemeralSignIns})
	hostOptions := host.HostOptions{
		Address: nil, Principal: options.Principal.Server, Store: opened.store,
		CloseTimeout: options.Timeouts.close(), ReadyTimeout: options.Timeouts.ready(), ReloadDrainTimeout: options.Timeouts.reloadDrain(),
		OnError:             func(err host.HostError) { a.report(AppErrorHost, err) },
		PrincipalAttributes: options.PrincipalAttributes,
	}
	server := Server.Address(nil)
	hostOptions.Address = &server
	if options.Telemetry != nil {
		hostOptions.Telemetry = options.Telemetry
	}
	h, err := host.CreateHost(hostOptions)
	if err != nil {
		return err
	}
	a.Host = h
	listen := options.Listen
	if listen == nil {
		listen = &chordhttp.ListenAddress{Host: "127.0.0.1", Port: 0}
	}
	httpOptions := chordhttp.HttpOptions{
		Listen: listen,
		Links: &chordhttp.LinksOptions{Path: "/chord", Opened: func(request chordhttp.HttpRequest, _ data.Principal, _ func()) (func(), error) {
			return signIns.Opened(request), nil
		}},
		OnError: func(err chordhttp.HttpError) { a.report(AppErrorHTTP, err) },
	}
	// The HTTP component records with the server host's adapter, as Pi's `createHttp` gets the app's `telemetry`: each request is a
	// `chord.http` span. A nil store stays a nil adapter (a nil AppTelemetry is not a nil interface of the option's type).
	if options.Telemetry != nil {
		httpOptions.Telemetry = options.Telemetry
	}
	// Closed once the NodeHost is loaded: the server's load, as its HTTP component and its delegation see it.
	nodeReady := make(chan struct{})
	a.HTTP = chordhttp.CreateHttp(loadingHost{Host: h, loaded: nodeReady}, httpOptions, ctx)
	root := options.Root
	if root == "" {
		root = "plugins"
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	kinds := make([]plugins.KindEntry, len(options.Kinds))
	for i, kind := range options.Kinds {
		kinds[i] = plugins.KindEntry{Name: kind.Name, Definition: plugins.KindDefinition{Platform: kind.Kind.Platform, HostPackages: hostPackagesOf(options.API, kind)}}
	}
	base := make([]string, len(options.Base))
	for i, path := range options.Base {
		if base[i], err = filepath.Abs(path); err != nil {
			return err
		}
	}
	// The web surface serves a browser build's assets under its prefix (Pi's `publicPath: ${WEB_PREFIX}assets/`).
	assetsPath := nodesurfaces.WebPrefix + "assets/"
	component := plugins.CreatePlugins(plugins.PluginsOptions{
		Build: build.BuildFacet, Roots: []model.PluginRoot{{Path: root, Required: true}, {Path: opened.layout.Plugins}}, Kinds: kinds, Install: options.Install, Base: base,
		Api:        &model.AppApi{Name: options.API.Name, Versions: options.API.Versions},
		PublicPath: &assetsPath,
		OnError:    func(err plugins.PluginsError) { a.report(AppErrorPlugins, err) }, Node: options.Node,
	}, ctx)
	a.Plugins = AppPlugins{
		State:     component.State(),
		Install:   a.installPlugin,
		Component: component,
	}
	a.build, a.rollout = component.Build, component.Rollout
	workerHosts := &workerHosts{keys: map[string]string{}, principals: map[string]string{}}
	link := &OwnerLink{}
	owner := options.Principal.Owner
	themeChoice, err := surfaces.ThemeChoiceFacet()
	if err != nil {
		return err
	}
	localeChoice, err := surfaces.LocaleChoiceFacet()
	if err != nil {
		return err
	}
	version := ""
	if len(options.API.Versions) > 0 {
		version = options.API.Versions[0]
	}
	// pig divergence (D460): the kit's facets are Go core facets on the server's Go host, beside the plugins' Node host; Pi loads them into the one host that loads the plugins.
	core := []host.HostFacet{
		{Name: "chord.http", Facet: a.HTTP.Facet()},
		{Name: "chord.app.info", Facet: AppInfoFacet(AppInfoValues{DataDir: opened.layout.DataDir, Name: options.Name, Version: version, Owner: owner})},
		{Name: "chord.app.sign-ins", Facet: signIns.Facet},
		{Name: "chord.app.owner", Facet: OwnerFacet(link, owner)},
		{Name: "chord.app.pairing", Facet: PairingFacet(NewPairings(PairingLimits{MaxPending: limits.PendingPairings, PerAddress: limits.PairingsPerAddress}), owner)},
		{Name: "chord.app.files", Facet: FilesFacet(filepath.Join(opened.layout.Server, "files"), options.Files, limits.Uploads)},
		{Name: "chord.themes", Facet: surfaces.ThemesFacet()},
		{Name: "chord.themes.choice", Facet: themeChoice},
		{Name: "chord.translations", Facet: surfaces.TranslationsFacet()},
		{Name: "chord.locale.choice", Facet: localeChoice},
	}
	if options.Telemetry != nil {
		core = append(core, host.HostFacet{Name: "chord.telemetry", Facet: telemetryingest.IngestFacet(telemetryingest.IngestOptions{
			Target: options.Telemetry,
			// Every link of the kit's server is signed in: its pages are the app's own people's.
			Admit: func(data.Principal) bool { return true },
			WorkerHost: func(principal data.Principal) (string, bool) {
				return workerHosts.get(data.PrincipalKey(principal, chordctx.Background(), nil))
			},
			PrincipalAttributes: func(principal data.Principal) telemetry.SpanAttributes {
				return a.Host.PrincipalAttributes(principal, chordctx.Background())
			},
		})})
	}
	core = append(core, options.Facets...)
	for _, kind := range options.Workers {
		created, err := a.workersOf(opened.store, kind, opened.layout, workerHosts, ctx)
		if err != nil {
			return err
		}
		a.Workers[kind.Kind.Kind] = created
	}
	// The server's plugins run in a Node subprocess behind this host (D462): the remote types placed on the server that the Go facets
	// do not serve are the NodeHost's, so the server host delegates them to its link (D605), and serves the union as Pi's one host does.
	// The link is attached by the first hold that goes over it, as a worker's is; the NodeHost loads after this host.
	var nodeHost atomic.Pointer[plugins.NodeHost]
	if _, err := h.Delegate(Server, host.RouteOptions{
		Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
		Resolve: func(_ contracts.Address, resolving context.Context) (host.ResolvedLink, error) {
			// The host resolves in a section it lends to this function: waiting for the NodeHost parks first (as a worker's route does).
			parking.Park(resolving)
			select {
			case <-nodeReady:
			case <-resolving.Done():
				return host.ResolvedLink{}, context.Cause(resolving)
			}
			return host.ResolvedLink{Link: nodeHost.Load().Link(), Principal: options.Principal.Server, Trusted: true}, nil
		},
	}, ctx); err != nil {
		return err
	}
	for _, each := range surfacesOf(h, options, a.Plugins.Component) {
		a.surfaces = append(a.surfaces, each)
		core = append(core, each.facets...)
		switch each.name {
		case "web":
			a.Web = &AppSurface{built: each.built}
		case "tui":
			a.Tui = &AppSurface{built: each.built}
		}
	}
	report, err := h.Load(core, ctx)
	if err != nil {
		return err
	}
	if err := failedPlugins(report); err != nil {
		return err
	}
	var serverKind *AppKindEntry
	for i := range options.Kinds {
		if options.Kinds[i].Name == "server" {
			serverKind = &options.Kinds[i]
		}
	}
	modules := plugins.HostPackages{}
	maps.Copy(modules, options.API.Modules)
	if serverKind != nil {
		maps.Copy(modules, serverKind.Kind.Packages)
	}
	// The server host's own store is the kit's; the plugins' is another file (two chord stores cannot share one).
	pluginsStore := filepath.Join(opened.layout.Server, "plugins.sqlite")
	if err := PrivateStore(pluginsStore); err != nil {
		return err
	}
	// pig divergence (D460): Pi's `themesFacet()` and `translationsFacet()` are core facets of the plugins' host (`app.ts:384`, `:386`): here the Node host's own.
	node, _, err := a.Plugins.Component.Load(plugins.PluginsLoad{
		Kind: "server", Core: []plugins.CoreFacet{piThemes, piTranslations}, Modules: modules, Address: &server, Principal: options.Principal.Server, Peer: options.Principal.Server,
		CloseTimeout: options.Timeouts.close(), ReadyTimeout: options.Timeouts.ready(), ReloadDrainTimeout: options.Timeouts.reloadDrain(), Telemetry: options.Telemetry,
		PrincipalAttributes: options.PrincipalAttributes,
		Store:               &plugins.StoreOptions{Path: pluginsStore},
		// The delegation the other way: what the Go server host serves (the choices, the telemetry ingest, the app's Go facets) is
		// bound there when a plugin holds it, as Pi's one server host binds it (D605).
		ParentServes: h.Serves,
		Bridged: []plugins.BridgedFacet{
			{Name: "chord.app.info", Host: h, Contracts: []*contracts.Contract{AppInfo}},
			{Name: "chord.app.sign-ins", Host: h, Contracts: []*contracts.Contract{SignIns}},
			{Name: "chord.app.files", Host: h, Contracts: []*contracts.Contract{Files, FilesFolder}},
		},
	}, ctx)
	// A required root's plugin failing returns the loaded host with the *PluginError: the app owns it and closes it.
	a.Node = node
	a.nodeData = node
	if err != nil {
		return err
	}
	// pig divergence (D462): an import or a delete a killed process left under way is resolved before anything is served: the NodeHost's
	// store is reachable only through the loaded NodeHost, and the server's requests wait for it (nodeReady).
	if err := a.resolveIntents(ctx); err != nil {
		return err
	}
	nodeHost.Store(node)
	close(nodeReady)
	if _, err := OwnerTokenFile(opened.layout.OwnerToken, owner, h, ctx); err != nil {
		return err
	}
	listening, err := a.HTTP.Listening(ctx)
	if err != nil {
		return err
	}
	a.link = "http://" + linkHost(listening.Host, listening.Port) + OwnerPath + "#" + link.Renew()
	a.startBases(a.baseBuilds())
	return nil
}

// failedPlugins is the first plugin of a report that failed to load, as an error: the start rejects with it.
func failedPlugins(report host.LoadReport) error {
	if len(report.Failed) == 0 {
		return nil
	}
	first := report.Failed[0]
	return fmt.Errorf("plugin %q failed to load: %w", first.Plugin, first.Error)
}

// workerKindName is the name of a worker kind for a message (its host kind may be missing in a bad option).
func workerKindName(kind AppWorkerKind) string {
	if kind.Kind == nil {
		return ""
	}
	return kind.Kind.Kind
}

// workerHosts is the address key of each worker by its principal's key: the principal its link stamps on its telemetry batches. An
// entry ends with its address (forget): Pi's map keeps them for the process's life, which grows with every address ever started.
type workerHosts struct {
	mu sync.Mutex
	// keys is the address key by principal key; principals is the principal key each address key made, to find it again.
	keys       map[string]string
	principals map[string]string
}

func (w *workerHosts) set(principalKey, address string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if previous, ok := w.principals[address]; ok && previous != principalKey && w.keys[previous] == address {
		delete(w.keys, previous)
	}
	w.keys[principalKey] = address
	w.principals[address] = principalKey
}

// forget drops the entry the address made, unless another address took its principal key since.
func (w *workerHosts) forget(address string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	principalKey, ok := w.principals[address]
	if !ok {
		return
	}
	delete(w.principals, address)
	if w.keys[principalKey] == address {
		delete(w.keys, principalKey)
	}
}

func (w *workerHosts) get(principalKey string) (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	key, ok := w.keys[principalKey]
	return key, ok
}

// checkOwner refuses an owner the kit could not recognize or that would act as the server (a declaration policy the types cannot
// express): sign-ins keep the owner as JSON, so a value that is not strict JSON would never match it again; the server's principal
// reaches every worker's kit objects.
func checkOwner(owner, server data.Principal, ctx context.Context) error {
	if !chordjson.IsValue(owner) {
		return errors.New("startServer: principal.owner must be strict JSON (no undefined fields, functions or class instances)")
	}
	if data.SamePrincipal(owner, server, ctx) {
		return errors.New("startServer: principal.owner must not be the server's principal")
	}
	return nil
}

type openedServer struct {
	layout       AppLayout
	cookieSecret string
	store        store.Store
	lock         *workers.ProcessLockHandle
}

// openServer is the server's own part of the data directory path: its lock (one server per data directory, by its real path: a
// second one, also through a symbolic link, would share its stores and secrets), the layout made private, the cookie secret and the
// server's store; the lock released when the rest fails.
func openServer(path string, ctx context.Context) (opened openedServer, err error) {
	dataDir, err := OpenDataDir(path)
	if err != nil {
		return openedServer{}, err
	}
	waited, cancel := context.WithTimeout(ctx, serverLock)
	defer cancel()
	lock, err := workers.ProcessLock(serverLockName(dataDir), waited)
	if err != nil {
		if waited.Err() != nil && ctx.Err() == nil {
			return openedServer{}, fmt.Errorf("startServer: another server runs on %s", dataDir)
		}
		return openedServer{}, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, lock.Release())
		}
	}()
	layout, err := CreateLayout(dataDir)
	if err != nil {
		return openedServer{}, err
	}
	cookieSecret, err := SecretFile(layout.CookieSecret)
	if err != nil {
		return openedServer{}, err
	}
	if err := PrivateStore(layout.ServerStore); err != nil {
		return openedServer{}, err
	}
	database, err := node.OpenNodeSqliteDatabase(layout.ServerStore, node.NodeSqliteDatabaseOptions{})
	if err != nil {
		return openedServer{}, err
	}
	// pig divergence (D462): the server's Go host has a store of its own (store.sqlite); the plugins' NodeHost has another (plugins.sqlite); Pi's one host has one.
	chordStore, err := store.OpenStore(store.SqliteBackend(database), ctx, store.StoreOptions{})
	if err != nil {
		return openedServer{}, err
	}
	return openedServer{layout: layout, cookieSecret: cookieSecret, store: chordStore, lock: lock}, nil
}

// hostPackagesOf is the host packages of kind: the API's shared and per-kind module of every version, and its packages.
func hostPackagesOf(api AppAPI, kind AppKindEntry) []string {
	var packages []string
	for _, version := range api.Versions {
		packages = append(packages, api.Name+"/"+version, api.Name+"/"+version+"/"+kind.Name)
	}
	for name := range kind.Kind.Packages {
		packages = append(packages, name)
	}
	return packages
}

// workersOf is the workers component of one worker kind (its base copied after the listen, into the kind's directory under
// `cache/workers/`): started with the kit's part of the start data beside the app's. The server's store is the workers' (Pi's `store`):
// a worker's exit is published after the commits its calls queued there.
func (a *App) workersOf(serverStore store.Store, kind AppWorkerKind, layout AppLayout, hosts *workerHosts, ctx context.Context) (*workers.Workers, error) {
	dataDir := layout.DataDir
	options := a.options
	facet := kind.Facet
	if facet == "" {
		facet = "worker"
	}
	worker := options.Principal.Worker
	server := options.Principal.Server
	idle := kind.IdleMs
	return workers.CreateWorkers(a.Host, kind.Kind, workers.WorkersOptions{
		Kind: facet, Entry: kind.Entry, ExecArgv: kind.ExecArgv, Env: kind.Env,
		// Each kind's base copy, in its own directory of the data directory's cache, made again at every start.
		BaseDir: filepath.Join(layout.WorkerBases, kind.Kind.Kind),
		// The server reaches its workers' kit objects (`HostData`) whatever the app admits.
		Authorize: func(principal data.Principal, address contracts.Address) (bool, error) {
			return data.SamePrincipal(principal, server, chordctx.Background()) || kind.Authorize(principal, address), nil
		},
		Principal: func(address contracts.Address) (data.Principal, error) { return worker(address), nil },
		Start: func(address contracts.Address, start context.Context) (delta.JsonValue, error) {
			// pig divergence (D462): an address whose interrupted import or delete the start could not resolve fails its own start.
			if err := a.blocked.get(address); err != nil {
				return nil, err
			}
			principal := worker(address)
			hosts.set(data.PrincipalKey(principal, start, nil), address.Key)
			kit := KitStart{
				Dir: WorkerDirectory(dataDir, address), Facet: facet, Principal: principal, Server: server,
				Files: options.Files, Telemetry: options.Telemetry != nil, Uploads: options.Limits.Uploads, Timeouts: options.Timeouts,
			}
			app, err := kind.Start(address, start)
			if err != nil {
				return nil, err
			}
			return delta.JsonObjectOf("kit", kit.json(), "app", app), nil
		},
		Lock: func(address contracts.Address) (string, error) {
			return workerLock(options.Name, dataDir, address.Key), nil
		},
		IdleMs:    idle,
		Store:     serverStore,
		Plugins:   a.Plugins.Component,
		OnError:   func(err workers.WorkersError) { a.report(AppErrorWorkers, err) },
		Forgotten: func(address contracts.Address) { hosts.forget(address.Key) },
	}, ctx)
}

// unknownOutcome is whether a host data commit's rejection leaves its outcome unknown: the store stopped after or during `COMMIT`
// (`unavailable/storage_failed`), so the commit may stand. The NodeHost's rejection is a *plugins.NodeError holding the ChordError.
func unknownOutcome(err error) bool {
	chord, ok := data.IsChordError(err)
	if node, isNode := errors.AsType[*plugins.NodeError](err); !ok && isNode && node.Chord != nil {
		chord, ok = node.Chord, true
	}
	return ok && chord.Code == data.CodeUnavailable && chord.Reason == data.ReasonStorageFailed
}

// nodeHostData is the host data operations of the NodeHost, the host of the server's plugins (*plugins.NodeHost; D462): the seam where
// Pi's test replaces the host's `importHostData` and `deleteHostData`.
type nodeHostData interface {
	ExportHostData(address contracts.Address, path string, ctx context.Context) error
	ImportHostData(address contracts.Address, path, existing string, ctx context.Context) error
	DeleteHostData(address contracts.Address, ctx context.Context) error
}

// resolveSeam, when set, is called with the name of each step of resolving an interrupted import or delete, before the step: "restore-node",
// "restore-go" and "place" (the undo of an import), "delete-node", "delete-go" and "delete-directory" (the completion of a delete), and
// "intent" (the removal of the intent, the last step of both), and "resolved" (every intent found at the start is resolved: the last thing
// before the server serves). The kill tests' child process stops there, where it is killed by its pid; nil in the product.
var resolveSeam func(step string)

// serverLockName is the process lock of the server for dataDir.
func serverLockName(dataDir string) string {
	return "chord-app:" + dataDir
}

// workerLock is the process lock of the worker with key: its worker holds it while it runs, an export or import meanwhile.
func workerLock(name, dataDir, key string) string {
	return name + ":" + dataDir + ":" + key
}

// linkHost is the host of the owner link for the address listened on: loopback for a wildcard, an IPv6 address in brackets.
func linkHost(listened string, port int) string {
	name := listened
	switch {
	case listened == "0.0.0.0" || listened == "":
		name = "127.0.0.1"
	case listened == "::":
		name = "[::1]"
	case strings.Contains(listened, ":") && listened[0] != '[':
		name = "[" + listened + "]"
	}
	return name + ":" + strconv.Itoa(port)
}

// installPlugin is AppPlugins.Install: it installs plugins from git or npm into the user's root and returns their names (it binds
// the user's root where Plugins.InstallPlugin takes any root); applying changes loads them.
func (a *App) installPlugin(ctx context.Context, source plugins.PluginSource) ([]string, error) {
	return a.Plugins.Component.InstallPlugin(source, filepath.Join(a.DataDir, "plugins"), ctx)
}

// ApplyChanges builds the dirty plugins and rolls them out: the server, every worker kind, then the web and terminal surfaces;
// never by itself. Calls run one after another (a later one builds after the earlier one rolled out); one whose context aborts
// while it waits stops waiting and builds nothing (Plugins.Apply).
func (a *App) ApplyChanges(ctx context.Context) (plugins.RolloutResult, error) {
	server, err := a.Plugins.Component.Step("server", a.Node)
	if err != nil {
		return plugins.RolloutResult{}, err
	}
	steps := []plugins.RolloutStep{server}
	for _, kind := range a.options.Workers {
		steps = append(steps, a.Workers[kind.Kind.Kind].Step(kind.Kind.Kind))
	}
	for _, each := range a.surfaces {
		steps = append(steps, each.step(each.name))
	}
	return a.Plugins.Component.ApplyWith(steps, ctx, a.build, a.rollout)
}

func (a *App) workersFor(address contracts.Address) (*workers.Workers, error) {
	// pig divergence (D462): an address whose interrupted import or delete the start could not resolve refuses its import, delete and export.
	if err := a.blocked.get(address); err != nil {
		return nil, err
	}
	found, ok := a.Workers[address.Kind]
	if !ok {
		return nil, fmt.Errorf("app: no worker kind %q", address.Kind)
	}
	return found, nil
}

func (a *App) manifest(address contracts.Address) ArchiveManifest {
	version := ""
	if len(a.options.API.Versions) > 0 {
		version = a.options.API.Versions[0]
	}
	return ArchiveManifest{Format: ArchiveFormat, App: a.options.Name, API: version, Address: ArchiveAddress{Kind: address.Kind, Key: address.Key}}
}

// running is whether the worker at address runs.
func running(found *workers.Workers, address contracts.Address) bool {
	object, _ := found.State().Value().(*delta.JsonObject)
	entry, _ := object.Value(address.Key).(*delta.JsonObject)
	if entry == nil {
		return false
	}
	status, _ := entry.Value("status").(string)
	return status == string(workers.WorkerRunning)
}

// ExportData writes the data of the worker at address (its store and finished files, and the server's records and rows belonging to
// the address) as an archive at path, stopping nothing: a running worker packs its own store at one consistent point; without one the
// server packs it under the worker's lock (no worker starts meanwhile).
func (a *App) ExportData(address contracts.Address, path string, ctx context.Context) (err error) {
	found, err := a.workersFor(address)
	if err != nil {
		return err
	}
	manifest := a.manifest(address)
	target, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	// The server's parts first, staged beside the archive: the worker or PackData packs them with the rest.
	staging, err := os.MkdirTemp(filepath.Dir(target), ".chord-server-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(staging)) }()
	// pig divergence (D462): the server's data is the NodeHost's part and the Go host's, two files merged into the one `server.archive` Pi
	// writes (a PiG server has two hosts; the Go host's owners are the kit's own, none of them host-keyed in the stock kit).
	nodePart, goPart, server := filepath.Join(staging, "server-node.archive"), filepath.Join(staging, "server-go.archive"), filepath.Join(staging, "server.archive")
	if err := a.nodeData.ExportHostData(address, nodePart, ctx); err != nil {
		return err
	}
	export, err := a.Host.ExportHostData(address, ctx)
	if err != nil {
		return err
	}
	staged, err := stageParts(ctx, export, goPart, nil)
	if err != nil {
		return err
	}
	nodeParts, err := openParts(nodePart)
	if err != nil {
		return err
	}
	goParts, err := openParts(goPart)
	if err != nil {
		return errors.Join(err, nodeParts.Close())
	}
	if err := writeParts(ctx, mergeParts(partsOf(nodeParts), partsOf(goParts), staged.documents), server); err != nil {
		return err
	}
	return a.exportWorker(found, address, manifest, server, target, ctx)
}

// exportWorker packs the worker's data with the server's part server at target, by the running worker or under its lock.
func (a *App) exportWorker(found *workers.Workers, address contracts.Address, manifest ArchiveManifest, server, target string, ctx context.Context) error {
	// Running: the worker packs its own store. Else the lock, unless a worker starts first: then ask it.
	for attempt := 1; ; attempt++ {
		if running(found, address) {
			return a.exportRunning(address, manifest, server, target, ctx)
		}
		lock, err := a.workerLock(address, ctx)
		if err != nil {
			return err
		}
		if lock != nil {
			return errors.Join(PackData(ctx, WorkerDirectory(a.DataDir, address), manifest, server, target), lock.Release())
		}
		if attempt == 5 {
			return fmt.Errorf("exportData: the worker at %s keeps starting; try again", address.Key)
		}
	}
}

func (a *App) exportRunning(address contracts.Address, manifest ArchiveManifest, server, target string, ctx context.Context) error {
	reference, err := a.Host.Get(ctx, HostData, delta.JsonObjectOf("address", AddressParam(address)), host.GetOptions{})
	if err != nil {
		return err
	}
	defer reference.Release()
	text, err := chordjson.Marshal(delta.JsonObjectOf(
		"format", manifest.Format, "app", manifest.App, "api", manifest.API,
		"address", delta.JsonObjectOf("kind", manifest.Address.Kind, "key", manifest.Address.Key),
	))
	if err != nil {
		return err
	}
	_, err = reference.Call("exportTo", delta.JsonObjectOf("path", target, "manifest", string(text), "server", server), ctx)
	return err
}

// DataImportOptions is what ImportData takes besides the archive and the address.
type DataImportOptions struct {
	// Existing is what happens when the address has data: store.ExistingRefuse (the default, also ""), or store.ExistingReplace.
	Existing string
}

// ImportData makes the data of address from the archive at archive (an archive of an address of its kind; another address is a copy:
// the server's host cells and keys are rewritten), under the worker's lock; its next hold starts it. An address with data (a directory,
// server records or rows) is refused, unless options.Existing is "replace": then its data is replaced. A refused or definitely failed
// import is undone (the old directory back, nothing new left, the NodeHost's commit undone if the Go host's failed after it: its import
// deleted, or with replace the old part put back); one whose server commit has an unknown outcome (`storage_failed`), or whose NodeHost
// rollback failed, undoes nothing: the new directory stays in place and the old one aside, and the error says so. A process killed in
// the middle leaves an intent that the next start resolves (resolveIntents): the import is undone, the address is as it was.
func (a *App) ImportData(archive string, address contracts.Address, ctx context.Context, options ...DataImportOptions) error {
	if _, err := a.workersFor(address); err != nil {
		return err
	}
	name, limits := a.options.Name, a.options.Limits
	existing := ""
	if len(options) > 0 {
		existing = options[0].Existing
	}
	if existing == "" {
		existing = store.ExistingRefuse
	}
	accept := func(manifest ArchiveManifest) error {
		// A trust boundary: an archive from anywhere.
		if manifest.Format != ArchiveFormat {
			return fmt.Errorf("importData: not a format %d archive", ArchiveFormat)
		}
		if manifest.App != name {
			return fmt.Errorf("importData: an archive of %q, not %q", manifest.App, name)
		}
		if manifest.Address.Kind != address.Kind {
			return fmt.Errorf("importData: an archive of a %s, not a %s", manifest.Address.Kind, address.Kind)
		}
		return nil
	}
	dir := WorkerDirectory(a.DataDir, address)
	source, err := filepath.Abs(archive)
	if err != nil {
		return err
	}
	return a.underLock(address, "importData", "has data (its worker runs)", ctx, func() error {
		if existing == store.ExistingRefuse && Exists(dir) {
			return fmt.Errorf("importData: %s exists already (an address is imported once)", dir)
		}
		// (1) Unpacked whole into staging: nothing changed yet.
		staging, server, err := UnpackStaging(ctx, source, filepath.Dir(dir), accept, ImportLimits{Bytes: limits.ImportBytes, Entries: limits.ImportEntries})
		if err != nil {
			return err
		}
		// pig divergence (D462): the server's part is Pi's one `server.archive`; it is split by owner into the NodeHost's part and the Go host's
		// (the owners the Go host serves), which are the two commits.
		err = splitHostData(ctx, server, filepath.Join(staging, goServerPart), filepath.Join(staging, nodeServerPart), a.Host.Serves)
		if err == nil {
			err = os.Remove(server)
		}
		if err != nil {
			return errors.Join(err, os.RemoveAll(staging))
		}
		where := placement{dir: dir, staging: staging}
		intent := dataIntent{Kind: intentImport, Staging: filepath.Base(staging)}
		if existing == store.ExistingReplace && Exists(dir) {
			where.aside = dir + ".aside-" + uuid.NewString()
			intent.Aside = filepath.Base(where.aside)
		}
		// pig divergence (D462): the import is two commits where Pi's is one. Its intent, with the old data of both hosts, is durable before
		// the first; a kill after it is undone at the next start, and the live rollbacks below are that undo.
		files, err := a.recordIntent(address, intent, ctx)
		if err != nil {
			return errors.Join(err, os.RemoveAll(staging))
		}
		// (2) The old directory aside, (3) the new one in place, (4) the server's parts, the commits, last.
		committed, err := a.placeAndCommit(where, address, existing, ctx)
		if err != nil {
			if unknownOutcome(err) {
				// It may have committed: undoing could pair the old directory with the new server data.
				return a.dropped(files, a.unknownImport(address, where.aside, err))
			}
			if committed {
				// The NodeHost's commit stood and the Go host's definitely failed: it is undone, so the import is refused as a whole. A
				// restore that fails leaves the intent: the next start restores again.
				if rolled := a.restoreStores(files, address, true, false, ctx); rolled != nil {
					return a.rollbackFailed(address, where.aside, err, rolled)
				}
			}
			if undone := where.undo(); undone != nil {
				return errors.Join(err, undone)
			}
			return a.dropped(files, err)
		}
		// The import is done when its intent is gone: removed right after the second commit, before the leftovers go.
		if err := a.finishIntent(files); err != nil {
			return fmt.Errorf("importData: %s: the import is done but its record %s cannot be removed: %w. If the record stays, the next start undoes this import (%s)", address.Key, files.record(), err, whereAfterImport(where.aside))
		}
		// Committed: what is left over is harmless, so these never fail the import.
		for _, left := range []string{nodeServerPart, goServerPart} {
			_ = os.Remove(filepath.Join(dir, left))
		}
		if where.aside != "" {
			_ = os.RemoveAll(where.aside)
		}
		return nil
	})
}

// nodeServerPart and goServerPart are the halves of an archive's `server.archive` in the staging directory (and then the worker's
// directory until the import is done): the records and rows of the owners the NodeHost holds and of the owners the Go host serves.
const (
	nodeServerPart = "server-node.archive"
	goServerPart   = "server-go.archive"
)

// placeAndCommit is steps 2 to 4 of ImportData: the old directory aside, the new one in place, then the server's parts (the NodeHost's
// first, then the Go host's: two commits, D462; the Go host's part may be empty, which a replace takes as the address's data removed and a
// refusal asks of it all the same). committed is whether the first commit succeeded before err.
//
// pig divergence (D462): the server's part is two commits (the NodeHost's store, then the Go host's), where Pi's `importHostData` is one.
func (a *App) placeAndCommit(where placement, address contracts.Address, existing string, ctx context.Context) (committed bool, err error) {
	if where.aside != "" {
		if err := os.Rename(where.dir, where.aside); err != nil {
			return false, err
		}
	}
	if err := os.Rename(where.staging, where.dir); err != nil {
		return false, err
	}
	if err := a.nodeData.ImportHostData(address, filepath.Join(where.dir, nodeServerPart), existing, ctx); err != nil {
		return false, err
	}
	file, err := os.Open(filepath.Join(where.dir, goServerPart))
	if err != nil {
		return true, err
	}
	defer func() { _ = file.Close() }()
	_, err = a.Host.ImportHostData(store.DecodeJsonl(&fileBytes{file: file}), ctx, store.HostImportOptions{Address: &address, Existing: existing})
	return true, err
}

// unknownImportError is the error of an import that undid nothing: the server's commit has an unknown outcome (or the rollback of the
// NodeHost's commit failed). Cause is the commit's rejection (Pi's `cause`); it is not part of the message.
type unknownImportError struct {
	message string
	cause   error
}

func (err *unknownImportError) Error() string { return err.message }
func (err *unknownImportError) Unwrap() error { return err.cause }

// rollbackFailed is the unknownImportError of an import whose Go host commit failed after the NodeHost's and whose NodeHost rollback
// failed too: both errors are its cause.
func (a *App) rollbackFailed(address contracts.Address, aside string, cause, rollback error) error {
	return &unknownImportError{fmt.Sprintf("importData: %s: the server host's commit failed after the plugins' store committed, and restoring the plugins' store failed; nothing was undone (%s). %s.", address.Key, whereAfterImport(aside), importAgain), errors.Join(cause, rollback)}
}

func whereAfterImport(aside string) string {
	where := "the new directory is in place"
	if aside != "" {
		where += ", the old one at " + aside
	}
	return where
}

const importAgain = `Run the import again with existing: "replace"`

// unknownImport is the unknownImportError of the import of address.
func (a *App) unknownImport(address contracts.Address, aside string, cause error) error {
	return &unknownImportError{fmt.Sprintf("importData: %s: the server's commit has an unknown outcome; nothing was undone (%s). %s once the server's store is back.", address.Key, whereAfterImport(aside), importAgain), cause}
}

// DeleteData deletes the data of address (its worker's directory and the server's records and rows belonging to it) under the worker's
// lock; refused while its worker runs. A process killed in the middle leaves an intent that the next start completes (resolveIntents).
func (a *App) DeleteData(address contracts.Address, ctx context.Context) error {
	if _, err := a.workersFor(address); err != nil {
		return err
	}
	dir := WorkerDirectory(a.DataDir, address)
	return a.underLock(address, "deleteData", "runs; stop its worker first", ctx, func() error {
		// pig divergence (D462): two commits (the NodeHost's store, then the Go host's), where Pi's `deleteHostData` is one; the intent makes a
		// kill between them, or before the directory goes, finish at the next start where Pi leaves the repetition to its caller.
		files, err := a.recordIntent(address, dataIntent{Kind: intentDelete}, ctx)
		if err != nil {
			return err
		}
		return a.dropped(files, a.completeDelete(address, dir, ctx))
	})
}

// underLock runs work under the worker's lock of address (refused while its worker runs: `<what>: <key> <held>`).
func (a *App) underLock(address contracts.Address, what, held string, ctx context.Context, work func() error) error {
	lock, err := a.workerLock(address, ctx)
	if err != nil {
		return err
	}
	if lock == nil {
		return fmt.Errorf("%s: %s %s", what, address.Key, held)
	}
	return errors.Join(work(), lock.Release())
}

// workerLock is the process lock of the worker at address (the one its worker takes at start), or nil while held.
func (a *App) workerLock(address contracts.Address, ctx context.Context) (*workers.ProcessLockHandle, error) {
	waited, cancel := context.WithTimeout(ctx, addressLock)
	defer cancel()
	lock, err := workers.ProcessLock(workerLock(a.options.Name, a.DataDir, address.Key), waited)
	if err != nil {
		if waited.Err() != nil && ctx.Err() == nil {
			return nil, nil
		}
		return nil, err
	}
	return lock, nil
}

// Close closes the HTTP component, the workers (in the options' order), the host, the store, the plugins' watcher and the data
// directory's lock: in that order (HTTP first: it drains requests the host's close would seal). As Pi's `#close`, the first step that
// fails ends the close there (the later steps do not run, the lock stays held) and is what Close returns, to every caller: the first
// caller runs the steps, the later ones wait for it and get its result (a later Close with a longer ctx does not resume what the
// first ended). As Pi's, it does not wait for the bases' builds, the surfaces' builds or the scan of the server's own code: what they
// finish after the close is dropped (a build that ends later finds the app closing and reports and publishes nothing), and the lock
// is released while they may still run (they write nothing under the data directory). A surface build not started by then never
// starts.
func (a *App) Close(ctx context.Context) error {
	if !a.closing.Swap(true) {
		close(a.ended)
	}
	a.close.Do(func() { a.closed = a.closeSteps(ctx) })
	return a.closed
}

// Wait returns once every build and scan the app started ended: each surface's build, the bases' builds and the scan of the server's
// own code (BaseWatched closed), or with ctx's error when ctx ends first (the tasks are left to finish). Go's lifetime ownership of
// what Pi leaves as unawaited promises: Pi has no counterpart, and Close does not call it. It returns nil at once when none started.
// What the tasks finish after the app closed is dropped, as it is by Close.
func (a *App) Wait(ctx context.Context) error {
	for _, each := range a.surfaces {
		if err := each.wait(ctx); err != nil {
			return err
		}
	}
	if !a.basesStarted.Load() {
		return nil
	}
	select {
	case <-a.baseWatched:
		return nil
	default:
	}
	select {
	case <-a.baseWatched:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// closeSteps is Pi's `#close`: each step awaited, the first failure returned, the lock released last.
func (a *App) closeSteps(ctx context.Context) error {
	for _, each := range a.surfaces {
		each.close()
	}
	if err := a.closeResources(ctx); err != nil {
		return err
	}
	return a.lock.Release()
}

// closeResources is the steps of Pi's `#close` but the lock's release: the first failure ends them there.
func (a *App) closeResources(ctx context.Context) error {
	if a.HTTP != nil {
		a.HTTP.Close(ctx)
	}
	for _, kind := range a.options.Workers {
		if each := a.Workers[kind.Kind.Kind]; each != nil {
			if err := each.Close(ctx); err != nil {
				return err
			}
		}
	}
	// The plugins' Node host is the Go form of Pi's one host (D462): it closes with the server's.
	if a.Node != nil {
		if err := a.Node.Close(ctx); err != nil {
			return err
		}
	}
	if a.Host != nil {
		if err := a.Host.Close(ctx); err != nil {
			return err
		}
	}
	if err := a.store.Close(ctx); err != nil {
		return err
	}
	if a.Plugins.Component != nil {
		a.Plugins.Component.Close()
	}
	return nil
}
