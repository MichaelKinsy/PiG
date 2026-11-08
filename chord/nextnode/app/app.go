package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/internal/parking"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/next/surfaces"
	"github.com/MichaelKinsy/PiG/chord/next/telemetryingest"
	chordhttp "github.com/MichaelKinsy/PiG/chord/nextnode/http"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
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
// (the start-data object, sessions, the owner link, pairing, files), and gives the app the controls it needs: the owner link,
// `ApplyChanges`, export and import per address.
//
// Forced Go differences (D460, D461, D462, D603). The plugins run in a Node subprocess (docs/plan/chord-plugins-node-evaluator.md): the kit's
// facets are Go core facets on the server's Go host, the server's plugins' host is the NodeHost the server host delegates to (D605: the remote types placed on the server that no Go facet serves go over its link),
// and the local types plugins use (AppInfo, Sessions) reach the Go facets through plugins.BridgedFacet (D460). `Files` and
// `FilesFolder` are not bridged: `Files` has a keyed member (`summary`) and the bridge refuses keyed members, so a TS plugin cannot
// get a host's files; Go code gets them directly (D461). The server's host and the plugins' NodeHost are two hosts with a store each
// (D462). The server's own code is its executable, recorded after the listen (`BaseWatched`) where Pi compares the static import graph
// of its start script; one changed since the process started is marked as a base change until a restart (`editedSinceStart`); one under the Go build cache (`go run`, `go test`) is never compared, as Pi skips an entry under `node_modules`;
// one that cannot be read is reported as `server_scan` and the server runs on. A start script's lazy optional imports have no Go form
// (D603).
// `startServer`'s generic principal parameter is designed out. `principal.worker` is a function that may be nil only without
// worker kinds.
//
// Not ported yet (pending, no divergence): the `principalAttributes` hook (T6 host spans: the Go host has none).

// pig divergence (D600): a Go worker is the program's own executable, so Pi's esbuild bundle of the worker's Node entry (base.ts, child.ts) has no Go
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

// AppAPI is the app's API (slice 12): its package name, the versions it provides (newest first) and this process's modules of them by
// specifier (`@acme/app/v3`, `@acme/app/v3/server`).
type AppAPI struct {
	Name     string
	Versions []string
	Modules  plugins.HostPackages
}

// AppKind is a facet kind of the app: its name, where its builds run, and the packages besides the API its plugins share (`react`).
// Its API modules are named by convention: `<api>/<version>` and `<api>/<version>/<kind>` for every version.
type AppKind struct {
	Name     string
	Platform plugins.Platform
	Packages plugins.HostPackages
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

// AppLimits are the bounds of what public endpoints, clients and imports may cost (0 is the default): live ephemeral sessions
// (1,000; past it the oldest ends, signed out), pairings under way that anonymous clients started (100 in all, 10 per client address;
// past them 429; the owner's own are not counted), uploads under way per `Files` object (64; past it `unavailable`, `busy`), an
// archive's unpacked bytes (4 GiB) and entries (100,000).
type AppLimits struct {
	Uploads            int
	EphemeralSessions  int
	PendingPairings    int
	PairingsPerAddress int
	ImportBytes        int64
	ImportEntries      int
}

// AppTimeouts are the hosts' timeouts (`HostOptions.closeTimeout`, `HostOptions.readyTimeout`, `HostOptions.reloadDrainTimeout`):
// zero is the host's default.
type AppTimeouts struct {
	Close time.Duration
	Ready time.Duration
	// ReloadDrain bounds how long a reload's restart keeps an object's old attempt open for old work.
	ReloadDrain time.Duration
}

func (t AppTimeouts) close() *time.Duration       { return optionalDuration(t.Close) }
func (t AppTimeouts) ready() *time.Duration       { return optionalDuration(t.Ready) }
func (t AppTimeouts) reloadDrain() *time.Duration { return optionalDuration(t.ReloadDrain) }

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
	Kinds     []AppKind
	Principal AppPrincipals
	// Workers are the worker kinds; none: a server with clients only. They need Principal.Worker.
	Workers []AppWorkerKind
	// Files are the caps of each host's files (the server's and every worker's).
	Files  FilesCaps
	Limits AppLimits
	// Install is how installs run npm.
	Install plugins.InstallOptions
	// Facets are facets the server host loads beside the kit's own (D456): the Go form of what Pi's app does with a facet that has
	// settings (`ephemeralLoginFacet()`, `inviteFacet()`, `defineUsers(...)`), which it re-exports through its API's server module
	// and loads with a one-line plugin. A plugin directory is TypeScript the Node plugin host evaluates and cannot import a Go
	// facet. Each is named; its name must not be one of the kit's (`chord.…`).
	Facets []host.HostFacet
	// Telemetry is the app's telemetry store; nil: none.
	Telemetry AppTelemetry
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
	if k.Timeouts.Close != 0 {
		timeouts.Set("closeTimeout", float64(k.Timeouts.Close/time.Millisecond)) //portlint:allow numbers Go-to-Go start data: whole milliseconds the kit wrote itself
	}
	if k.Timeouts.Ready != 0 {
		timeouts.Set("readyTimeout", float64(k.Timeouts.Ready/time.Millisecond)) //portlint:allow numbers Go-to-Go start data: whole milliseconds the kit wrote itself
	}
	if k.Timeouts.ReloadDrain != 0 {
		timeouts.Set("reloadDrainTimeout", float64(k.Timeouts.ReloadDrain/time.Millisecond)) //portlint:allow numbers Go-to-Go start data: whole milliseconds the kit wrote itself
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
			kit.Timeouts.Close = time.Duration(n) * time.Millisecond //portlint:allow numbers Go-to-Go start data: whole milliseconds KitStart.json wrote
		}
		if n, ok := timeouts.Value("readyTimeout").(float64); ok {
			kit.Timeouts.Ready = time.Duration(n) * time.Millisecond //portlint:allow numbers Go-to-Go start data: whole milliseconds KitStart.json wrote
		}
		if n, ok := timeouts.Value("reloadDrainTimeout").(float64); ok {
			kit.Timeouts.ReloadDrain = time.Duration(n) * time.Millisecond //portlint:allow numbers Go-to-Go start data: whole milliseconds KitStart.json wrote
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
	// Plugins is the plugins component, for the app's "apply changes" control: its marks and its rollouts.
	Plugins *plugins.Plugins
	// Node is the server's plugins' host (the NodeHost the server host accepted the link of).
	Node *plugins.NodeHost
	// DataDir is the data directory.
	DataDir string
	// Web is the web surface, when the app has one: its base bundle's hash once built (BasesBuilt).
	Web *AppSurface
	// Tui is the terminal surface, when the app has one: its base bundle's hash once built.
	Tui *AppSurface
	// OwnerTokenFile is the owner token file's path (a device session of the owner, mode 0600).
	OwnerTokenFile string

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

	applyMu sync.Mutex
	close   sync.Once
	closed  error
	// closing is set when the close starts: what runs after the start (the bases' builds, the scan) stops reporting there.
	closing atomic.Bool
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
		return nil, errors.Join(err, app.Close(ctx))
	}
	return app, nil
}

func (a *App) report(kind AppErrorKind, err any) {
	if a.options.OnError != nil {
		a.options.OnError(AppError{Kind: kind, Error: err})
	}
}

// serverExecutable is the path of the server's own executable, to compare like a base file (Pi's `nodeBaseInputs(process.argv[1])`):
// not one under the Go build cache (the analog of an entry under `node_modules`: a test runner, `go run`), and one that cannot be
// read is reported as `server_scan` and left out.
func (a *App) serverExecutable() (string, bool) {
	lookup := a.options.executable
	if lookup == nil {
		lookup = os.Executable
	}
	exe, err := lookup()
	if err == nil {
		exe, err = filepath.Abs(exe)
	}
	if err == nil {
		var info os.FileInfo
		if info, err = os.Stat(exe); err == nil && !info.Mode().IsRegular() {
			err = fmt.Errorf("%s is not a regular file", exe)
		}
	}
	if err == nil {
		// Readable now: the watcher hashes it, and its failure would come as a plugins error instead.
		var file *os.File
		if file, err = os.Open(exe); err == nil {
			err = file.Close()
		}
	}
	if err != nil {
		a.reportLate(AppErrorServerScan, fmt.Errorf("the server's executable cannot be read: %w", err), "")
		return "", false
	}
	if inGoBuildCache(exe) {
		return "", false
	}
	return exe, true
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

func (a *App) start(opened openedServer, ctx context.Context) error {
	options := a.options
	limits := options.Limits
	sessions := CreateSessions(opened.cookieSecret, SessionLimits{MaxEphemeral: limits.EphemeralSessions})
	hostOptions := host.HostOptions{
		Address: nil, Principal: options.Principal.Server, Store: opened.store,
		CloseTimeout: options.Timeouts.close(), ReadyTimeout: options.Timeouts.ready(), ReloadDrainTimeout: options.Timeouts.reloadDrain(),
		OnError: func(err host.HostError) { a.report(AppErrorHost, err) },
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
	a.HTTP = chordhttp.CreateHttp(h, chordhttp.HttpOptions{
		Listen: listen,
		Links: &chordhttp.LinksOptions{Path: "/chord", OptionsFunc: a.linksAfterTheBases, Opened: func(request chordhttp.HttpRequest, _ data.Principal) (func(), error) {
			return sessions.Opened(request), nil
		}},
		OnError: func(err chordhttp.HttpError) { a.report(AppErrorHTTP, err) },
	}, ctx)
	root := options.Root
	if root == "" {
		root = "plugins"
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	kinds := make([]plugins.KindDefinition, len(options.Kinds))
	for i, kind := range options.Kinds {
		kinds[i] = plugins.KindDefinition{Name: kind.Name, Platform: kind.Platform, HostPackages: hostPackagesOf(options.API, kind)}
	}
	base := make([]string, len(options.Base))
	for i, path := range options.Base {
		if base[i], err = filepath.Abs(path); err != nil {
			return err
		}
	}
	// The web surface serves a browser build's assets under its prefix (Pi's `publicPath: ${WEB_PREFIX}assets/`).
	assetsPath := nodesurfaces.WebPrefix + "assets/"
	a.Plugins = plugins.CreatePlugins(plugins.PluginsOptions{
		Roots: []plugins.PluginRoot{{Path: root, Required: true}, {Path: opened.layout.Plugins}}, Kinds: kinds, Install: options.Install, Base: base,
		Api:        &plugins.AppApi{Name: options.API.Name, Versions: options.API.Versions},
		PublicPath: &assetsPath,
		OnError:    func(err plugins.PluginsError) { a.report(AppErrorPlugins, err) }, Node: options.Node,
	}, ctx)
	a.build, a.rollout = a.Plugins.Build, a.Plugins.Rollout
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
		{Name: "chord.app.sessions", Facet: sessions.Facet},
		{Name: "chord.app.owner", Facet: OwnerFacet(link, owner)},
		{Name: "chord.app.pairing", Facet: PairingFacet(NewPairings(PairingLimits{MaxPending: limits.PendingPairings, PerAddress: limits.PairingsPerAddress}), owner)},
		// pig divergence (D461): Files is a Go facet the plugins' NodeHost cannot get (the bridge refuses its keyed member); Pi's plugins `self.get(Files, {})`.
		{Name: "chord.app.files", Facet: FilesFacet(filepath.Join(opened.layout.Server, "files"), options.Files, limits.Uploads)},
		{Name: "chord.themes", Facet: surfaces.ThemesFacet()},
		{Name: "chord.themes.choice", Facet: themeChoice},
		{Name: "chord.translations", Facet: surfaces.TranslationsFacet()},
		{Name: "chord.locale.choice", Facet: localeChoice},
	}
	if options.Telemetry != nil {
		core = append(core, host.HostFacet{Name: "chord.telemetry", Facet: telemetryingest.IngestFacet(telemetryingest.IngestOptions{
			Target: options.Telemetry,
			WorkerHost: func(principal data.Principal) (string, bool) {
				return workerHosts.get(data.PrincipalKey(principal, chordctx.Background(), nil))
			},
			PrincipalAttributes: func(data.Principal) telemetry.SpanAttributes { return nil },
		})})
	}
	core = append(core, options.Facets...)
	for _, kind := range options.Workers {
		created, err := a.workersOf(kind, opened.layout, workerHosts, ctx)
		if err != nil {
			return err
		}
		a.Workers[kind.Kind.Kind] = created
	}
	// The server's plugins run in a Node subprocess behind this host (D462): the remote types placed on the server that the Go facets
	// do not serve are the NodeHost's, so the server host delegates them to its link (D605), and serves the union as Pi's one host does.
	// The link is attached by the first hold that goes over it, as a worker's is; the NodeHost loads after this host.
	nodeReady := make(chan struct{})
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
	for _, each := range surfacesOf(h, options, a.Plugins) {
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
	var serverKind *AppKind
	for i := range options.Kinds {
		if options.Kinds[i].Name == "server" {
			serverKind = &options.Kinds[i]
		}
	}
	modules := plugins.HostPackages{}
	for name, path := range options.API.Modules {
		modules[name] = path
	}
	if serverKind != nil {
		for name, path := range serverKind.Packages {
			modules[name] = path
		}
	}
	// The server host's own store is the kit's; the plugins' is another file (two chord stores cannot share one).
	pluginsStore := filepath.Join(opened.layout.Server, "plugins.sqlite")
	if err := PrivateStore(pluginsStore); err != nil {
		return err
	}
	node, _, err := a.Plugins.Load(plugins.PluginsLoad{
		Kind: "server", Modules: modules, Address: &server, Principal: options.Principal.Server, Peer: options.Principal.Server,
		CloseTimeout: options.Timeouts.close(), ReadyTimeout: options.Timeouts.ready(), ReloadDrainTimeout: options.Timeouts.reloadDrain(), Telemetry: options.Telemetry,
		Store: &plugins.StoreOptions{Path: pluginsStore},
		// The delegation the other way: what the Go server host serves (the choices, the telemetry ingest, the app's Go facets) is
		// bound there when a plugin holds it, as Pi's one server host binds it (D605).
		ParentServes: h.Serves,
		Bridged: []plugins.BridgedFacet{
			{Name: "chord.app.info", Host: h, Contracts: []*contracts.Contract{AppInfo}},
			{Name: "chord.app.sessions", Host: h, Contracts: []*contracts.Contract{Sessions}},
		},
	}, ctx)
	// A required root's plugin failing returns the loaded host with the *PluginError: the app owns it and closes it.
	a.Node = node
	if err != nil {
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
// express): sessions keep the owner as JSON, so a value that is not strict JSON would never match it again; the server's principal
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
	lock, err := workers.ProcessLock("chord-app:"+dataDir, waited)
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
func hostPackagesOf(api AppAPI, kind AppKind) []string {
	var packages []string
	for _, version := range api.Versions {
		packages = append(packages, api.Name+"/"+version, api.Name+"/"+version+"/"+kind.Name)
	}
	for name := range kind.Packages {
		packages = append(packages, name)
	}
	return packages
}

// workersOf is the workers component of one worker kind (its base copied after the listen, into the kind's directory under
// `cache/workers/`): started with the kit's part of the start data beside the app's.
func (a *App) workersOf(kind AppWorkerKind, layout AppLayout, hosts *workerHosts, ctx context.Context) (*workers.Workers, error) {
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
			return workerLock(options.Name, dataDir, address), nil
		},
		IdleMs:    idle,
		Plugins:   a.Plugins,
		OnError:   func(err workers.WorkersError) { a.report(AppErrorWorkers, err) },
		Forgotten: func(address contracts.Address) { hosts.forget(address.Key) },
	}, ctx)
}

// workerLock is the process lock of the worker at address: its worker holds it while it runs, an export or import meanwhile.
func workerLock(name, dataDir string, address contracts.Address) string {
	return name + ":" + dataDir + ":" + address.Key
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

// InstallPlugin installs plugins from git or npm into the user's root and returns their names (Pi's `app.plugins.install`, which binds
// the user's root where Plugins.InstallPlugin takes any root); applying changes loads them.
func (a *App) InstallPlugin(source plugins.PluginSource, ctx context.Context) ([]string, error) {
	return a.Plugins.InstallPlugin(source, filepath.Join(a.DataDir, "plugins"), ctx)
}

// ApplyChanges builds the dirty plugins and rolls them out: the server, every worker kind, then the web and terminal surfaces;
// never by itself. Calls run one after another (a later one builds after the earlier one rolled out).
func (a *App) ApplyChanges(ctx context.Context) (plugins.RolloutResult, error) {
	a.applyMu.Lock()
	defer a.applyMu.Unlock()
	built, err := a.build(a.Plugins.Marks().Dirty, ctx)
	if err != nil {
		return plugins.RolloutResult{}, err
	}
	server, err := a.Plugins.Step("server", a.Node)
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
	return a.rollout(steps, built, ctx)
}

func (a *App) workersFor(address contracts.Address) (*workers.Workers, error) {
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
func running(found *workers.Workers, address contracts.Address) (bool, error) {
	value, err := found.State().Value()
	if err != nil {
		return false, err
	}
	object, _ := value.(*delta.JsonObject)
	entry, _ := object.Value(address.Key).(*delta.JsonObject)
	if entry == nil {
		return false, nil
	}
	status, _ := entry.Value("status").(string)
	return status == string(workers.WorkerRunning), nil
}

// ExportData writes the data the worker at address created (its store and finished files) as an archive at path, stopping nothing: a
// running worker packs its own store at one consistent point; without one the server packs it under the worker's lock (no worker starts
// meanwhile).
func (a *App) ExportData(address contracts.Address, path string, ctx context.Context) error {
	found, err := a.workersFor(address)
	if err != nil {
		return err
	}
	manifest := a.manifest(address)
	target, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	// Running: the worker packs its own store. Else the lock, unless a worker starts first: then ask it.
	for attempt := 1; ; attempt++ {
		isRunning, err := running(found, address)
		if err != nil {
			return err
		}
		if isRunning {
			return a.exportRunning(address, manifest, target, ctx)
		}
		lock, err := a.workerLock(address, ctx)
		if err != nil {
			return err
		}
		if lock != nil {
			return errors.Join(PackData(ctx, WorkerDirectory(a.DataDir, address), manifest, target), lock.Release())
		}
		if attempt == 5 {
			return fmt.Errorf("exportData: the worker at %s keeps starting; try again", address.Key)
		}
	}
}

func (a *App) exportRunning(address contracts.Address, manifest ArchiveManifest, target string, ctx context.Context) error {
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
	_, err = reference.Call("exportTo", delta.JsonObjectOf("path", target, "manifest", string(text)), ctx)
	return err
}

// ImportData makes the data of address (which must have none yet: nothing is overwritten) from the archive at archive, under the
// worker's lock; its next hold starts it.
func (a *App) ImportData(archive string, address contracts.Address, ctx context.Context) error {
	if _, err := a.workersFor(address); err != nil {
		return err
	}
	name, limits := a.options.Name, a.options.Limits
	accept := func(manifest ArchiveManifest) error {
		// A trust boundary: an archive from anywhere.
		if manifest.Format != ArchiveFormat {
			return fmt.Errorf("importData: not a format %d archive", ArchiveFormat)
		}
		if manifest.App != name {
			return fmt.Errorf("importData: an archive of %q, not %q", manifest.App, name)
		}
		return nil
	}
	lock, err := a.workerLock(address, ctx)
	if err != nil {
		return err
	}
	// A worker holds it: it runs, so the address has data, and nothing is overwritten.
	if lock == nil {
		return fmt.Errorf("importData: %s has data (its worker runs)", address.Key)
	}
	source, err := filepath.Abs(archive)
	if err != nil {
		return errors.Join(err, lock.Release())
	}
	unpacked := UnpackData(ctx, source, WorkerDirectory(a.DataDir, address), accept, ImportLimits{Bytes: limits.ImportBytes, Entries: limits.ImportEntries})
	return errors.Join(unpacked, lock.Release())
}

// workerLock is the process lock of the worker at address (the one its worker takes at start), or nil while held.
func (a *App) workerLock(address contracts.Address, ctx context.Context) (*workers.ProcessLockHandle, error) {
	waited, cancel := context.WithTimeout(ctx, addressLock)
	defer cancel()
	lock, err := workers.ProcessLock(workerLock(a.options.Name, a.DataDir, address), waited)
	if err != nil {
		if waited.Err() != nil && ctx.Err() == nil {
			return nil, nil
		}
		return nil, err
	}
	return lock, nil
}

// Close closes the HTTP component, the workers, the host, the store, the plugins' watcher and the data directory's lock: in that
// order (HTTP first: it drains requests the host's close would seal).
func (a *App) Close(ctx context.Context) error {
	if !a.closing.Swap(true) {
		close(a.ended)
	}
	a.close.Do(func() {
		var errs []error
		if a.HTTP != nil {
			a.HTTP.Close(ctx)
		}
		for _, each := range a.Workers {
			errs = append(errs, each.Close(ctx))
		}
		if a.Node != nil {
			errs = append(errs, a.Node.Close(ctx))
		}
		if a.Host != nil {
			errs = append(errs, a.Host.Close(ctx))
		}
		errs = append(errs, a.store.Close(ctx))
		if a.Plugins != nil {
			a.Plugins.Close()
		}
		errs = append(errs, a.lock.Release())
		a.closed = errors.Join(errs...)
	})
	return a.closed
}
