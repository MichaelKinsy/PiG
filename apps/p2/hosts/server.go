package hosts

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/web"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/nextnode/app"
	chordhttp "github.com/MichaelKinsy/PiG/chord/nextnode/http"
)

// Ports apps/p2/src/hosts/server.ts: the server host: HTTP and WebSocket accept, routes `session:` hosts to session
// workers, loads the server facets.
//
// Forced Go differences. Node's `http.createServer` and `ws` are net/http and Chord's upgrades endpoint
// (chordhttp.Upgrades.Link, which carries socket-limits.ts: frames up to 1 MiB, 4 MiB of unsent output); a request
// handler that may throw returns an error. The dev supervisor and the plugin build are designed out (D473): plugins are
// compiled in, so P2Server.Reload rolls out a plugin whose build revision changed, as Pi's digest-changed rollout, and is a no-op otherwise (reload.go). The web UI is the bundle Pi's
// esbuild makes of the vendored sources, embedded (package web, web.go); the TUI bundle (bundles.ts) is lane E's.

// MaxClientFrame is the largest frame a client may send (`MAX_CLIENT_FRAME`). Uploads go over HTTP; a 1,000,000-character
// message fits.
const MaxClientFrame = 1024 * 1024

// ServerOptions is what StartServer takes.
type ServerOptions struct {
	// Plugins are exactly these plugins (nil: all registered). A missing provider fails at activation, naming the consumer.
	Plugins []string
	DataDir string
	// Port is the listening port (nil: PORT, else 8788); 0 picks a free one.
	Port *int
	// Fake runs under the fake provider (nil: `P2_FAKE=1`).
	Fake *bool
	// Web serves the web UI (nil: true).
	Web *bool
	// Sprite is where the PiG sprite the branded mark draws comes from (nil: none, the server is Pi's; ignored unless the web
	// bundle is branded). The server owns it and closes it.
	Sprite SpriteSource
	// Idle is the time before a session worker stops (default 5 minutes; `P2_IDLE_MS`).
	Idle time.Duration
	// MoveTimeout is how long a running worker may take to follow a relocation before it is restarted instead (default 60 s).
	MoveTimeout time.Duration
	// WorkerEnv is extra environment for workers.
	WorkerEnv map[string]string
	// AllowedHosts are HTTPS tunnel hosts besides loopback (nil: `P2_ALLOWED_HOSTS`, comma-separated).
	AllowedHosts []string
	// Resources overrides the server's configuration resources (tests: a fixture provider with login flows).
	Resources p2runtime.ServerResourceOptions
	// Facets are loaded with the core, never reloaded (disposable test probes: their routes and types).
	Facets []host.HostFacet
	// Entry is the worker executable and ExecArgv its arguments (default: this executable, `worker`). A test binary sets
	// these to start itself as a worker (p2test).
	Entry    string
	ExecArgv []string
	// BuildRevision reports the build revision of a plugin, read at each Reload (nil: always empty). A Go plugin's code is
	// compiled in, so this is the input Pi's build digest has: a Reload rolls a plugin out only when its revision differs
	// from the last one rolled out (the initial one is empty, which the workers start on). A test changes it to stand in
	// for a rebuilt plugin source.
	BuildRevision func(plugin string) string
}

// P2Server is a running server.
type P2Server struct {
	Host    host.Host
	Port    int
	DataDir string
	// Plugins is the server/worker plugin selection (alphabetical).
	Plugins []string

	httpServer *http.Server
	upgrades   *chordhttp.Upgrades
	workers    *SessionWorkers
	reloads    reloader
	closeOnce  sync.Once
	closeErr   error
	closeFn    func(ctx context.Context) error
}

// Workers are the session workers.
func (s *P2Server) Workers() *SessionWorkers { return s.workers }

// Close stops the server: sockets, workers, host, store, log capture, listener, lock.
func (s *P2Server) Close(ctx context.Context) error {
	s.closeOnce.Do(func() { s.closeErr = s.closeFn(ctx) })
	return s.closeErr
}

func envBool(name string) bool { return os.Getenv(name) == "1" }

// StartServer starts a p2 server.
func StartServer(options ServerOptions) (*P2Server, error) {
	ctx := chordctx.Background()
	// As Pi Mobile: a half-set default model from the environment refuses to start (before taking any lock).
	if _, err := p2runtime.EnvDefaultModel(os.LookupEnv); err != nil {
		return nil, err
	}
	selection, err := SelectPlugins(options.Plugins)
	if err != nil {
		return nil, err
	}
	dataDir := options.DataDir
	if dataDir == "" {
		dataDir = os.Getenv("P2_DATA_DIR")
	}
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dataDir = filepath.Join(home, ".pi", "p2")
	}
	fake := envBool("P2_FAKE")
	if options.Fake != nil {
		fake = *options.Fake
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "sessions"), 0o700); err != nil {
		return nil, err
	}
	// One server per data directory: a lock another server holds refuses the start after a short wait (a restart's old
	// server may still be exiting).
	unlock, err := p2runtime.TakeProcessLock("p2:server:"+dataDir, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("Another p2 server uses %s: %w", dataDir, err)
	}
	allowedHosts := options.AllowedHosts
	if allowedHosts == nil {
		allowedHosts = ParseAllowedHosts(os.Getenv("P2_ALLOWED_HOSTS"))
	}
	// The server's Chord store (`<data>/server/store.sqlite`): persisted documents and job runs of its host-lifetime objects
	// (session lifecycle operations, search rebuilds) and its `Files` index. Its outbox stays off: local-only data never
	// goes to a sync.
	if err := p2runtime.AssertMigrated(dataDir); err != nil {
		_ = unlock()
		return nil, err
	}
	store, err := p2runtime.OpenHostStore(ctx, p2runtime.ServerDirectory(dataDir), false)
	if err != nil {
		_ = unlock()
		return nil, err
	}
	// The server's own requests act as SERVER: a worker tells them from a person's (WorkerBoundary).
	serverAddress := p2runtime.ServerKind.Address(delta.NewJsonObject(0))
	h, err := p2runtime.NewHost(host.HostOptions{
		Address:   &serverAddress,
		Principal: p2runtime.ToChord(p2runtime.Server),
		Store:     store,
		OnError: func(failure host.HostError) {
			if isOwnerNotice(failure) {
				p2runtime.ProcessLogs.Notice("server", fmt.Sprint(failure))
			}
			fmt.Fprintf(os.Stderr, "p2 server: %v\n", failure)
		},
	})
	if err != nil {
		_ = store.Close(ctx)
		_ = unlock()
		return nil, err
	}
	// Listen first so plugins see the actual port, but capture requests and upgrades BEFORE listening: a reconnect can
	// arrive while plugins load, and the browser would wait forever for its handshake.
	loaded, failed := make(chan struct{}), make(chan struct{})
	state := &serverState{host: h, dataDir: dataDir, allowedHosts: allowedHosts, upgrades: chordhttp.NewUpgrades(), loaded: loaded, failed: failed}
	state.nonce = newNonce()
	if options.Web == nil || *options.Web {
		bundle, err := web.Embedded()
		if err != nil {
			_ = h.Close(ctx)
			_ = store.Close(ctx)
			_ = unlock()
			return nil, err
		}
		state.web = &Web{Nonce: state.nonce, Bundle: bundle}
		if options.Sprite != nil && bundle.UpstreamVersion != "" {
			state.sprite = newSpriteHub(options.Sprite)
			state.web.Sprite = state.sprite
		}
	}
	if options.Sprite != nil && state.sprite == nil {
		options.Sprite.Close() // nothing draws it: no web UI, or Pi's own bundle
	}
	httpServer := &http.Server{Handler: state}
	port := 8788
	if env := os.Getenv("PORT"); env != "" {
		if parsed, err := strconv.Atoi(env); err == nil {
			port = parsed
		}
	}
	if options.Port != nil {
		port = *options.Port
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		_ = h.Close(ctx)
		_ = store.Close(ctx)
		_ = unlock()
		return nil, err
	}
	actualPort := listener.Addr().(*net.TCPAddr).Port
	//portlint:allow golifetime Serve returns when Close shuts httpServer down
	go func() { _ = httpServer.Serve(listener) }()
	info := p2runtime.ServerInfo{App: appDirectory(), DataDir: dataDir, Fake: fake, AllowedHosts: allowedHosts, Port: actualPort}
	entry, execArgv := options.Entry, options.ExecArgv
	if entry == "" {
		if entry, err = os.Executable(); err != nil {
			_ = httpServer.Close()
			_ = h.Close(ctx)
			_ = store.Close(ctx)
			_ = unlock()
			return nil, err
		}
		execArgv = []string{WorkerArg}
	}
	env := map[string]string{PluginsEnv: pluginNames(selection)}
	for key, value := range options.WorkerEnv {
		env[key] = value
	}
	idle := options.Idle
	if idle == 0 {
		if ms, err := strconv.Atoi(os.Getenv("P2_IDLE_MS")); err == nil && ms > 0 {
			idle = time.Duration(ms) * time.Millisecond
		}
	}
	sessionWorkers, err := NewSessionWorkers(SessionWorkersOptions{
		Host: h, App: info.App, DataDir: dataDir, Fake: fake, Entry: entry, ExecArgv: execArgv, Env: env,
		Idle: idle, MoveTimeout: options.MoveTimeout,
		// Each plugin's part of the start data (the sessions plugin's `session`); an error refuses the start.
		Values: func(session string, valuesCtx context.Context) (*delta.JsonObject, error) {
			return workerValues(h, session, valuesCtx)
		},
	}, ctx)
	if err != nil {
		_ = httpServer.Close()
		_ = h.Close(ctx)
		_ = store.Close(ctx)
		_ = unlock()
		return nil, err
	}
	// Capture before plugin resources open (provider SDK startup), not only after the logs facet activates.
	stopLogs := p2runtime.CaptureProcessLogs("server", false)
	fail := func(cause error) (*P2Server, error) {
		p2runtime.ProcessLogs.Notice("server", "Server startup failed: "+cause.Error())
		close(failed)
		_ = sessionWorkers.Close(ctx)
		_ = h.Close(ctx)
		_ = store.Close(ctx)
		stopLogs()
		_ = httpServer.Close()
		_ = unlock()
		return nil, cause
	}
	parts := &p2runtime.ServerParts{
		ResourceOptions: options.Resources,
		Workers:         sessionWorkers,
		Root: func(fn func(host.Ctx) (any, error), rootCtx context.Context) (any, error) {
			return h.Root(fn, rootCtx, host.RootOptions{})
		},
	}
	facets := []host.HostFacet{
		{Name: "@p2/core", Facet: p2runtime.ServerCore(h, info, parts)},
		// The server's files (the kit's `Files`): `<data>/server/files/`.
		{Name: "chord.app.files", Facet: app.FilesFacet(p2runtime.HostFiles(p2runtime.ServerDirectory(dataDir)), app.FilesCaps{}, 0)},
	}
	facets = append(facets, serverFacets(selection)...)
	facets = append(facets, options.Facets...)
	report, err := h.Load(facets, ctx)
	if err == nil && len(report.Failed) > 0 {
		err = report.Failed[0].Error
	}
	if err != nil {
		return fail(err)
	}
	names := make([]string, len(selection))
	for i, plugin := range selection {
		names[i] = plugin.Name
	}
	server := &P2Server{Host: h, Port: actualPort, DataDir: dataDir, Plugins: names, httpServer: httpServer, upgrades: state.upgrades, workers: sessionWorkers}
	server.reloads = reloader{facets: facets, selection: selection, revision: map[string]string{}, build: options.BuildRevision}
	if server.reloads.build == nil {
		server.reloads.build = func(string) string { return "" }
	}
	server.closeFn = func(closeCtx context.Context) error {
		deadline := make(chan struct{})
		timer := time.AfterFunc(5*time.Second, func() { close(deadline) })
		state.upgrades.Close(deadline)
		timer.Stop()
		var first error
		note := func(err error) {
			if first == nil && err != nil {
				first = err
			}
		}
		if state.sprite != nil {
			state.sprite.close()
		}
		note(sessionWorkers.Close(closeCtx))
		note(h.Close(closeCtx))
		note(store.Close(closeCtx))
		stopLogs()
		_ = httpServer.Close()
		note(unlock())
		return first
	}
	close(loaded)
	return server, nil
}

func appDirectory() string {
	if dir := os.Getenv("P2_APP_DIR"); dir != "" {
		return dir
	}
	return ""
}

func newNonce() string {
	random := make([]byte, 18)
	_, _ = rand.Read(random)
	return base64.RawStdEncoding.EncodeToString(random)
}

// workerValues are the start data's values of session: each plugin's contribution (WorkerInitValues); one for `session` is
// required.
func workerValues(h host.Host, session string, ctx context.Context) (*delta.JsonObject, error) {
	result, err := h.Root(func(rootCtx host.Ctx) (any, error) {
		source, err := h.Registry(rootCtx, p2runtime.WorkerInitValues.Registry, host.GetOptions{})
		if err != nil {
			return nil, err
		}
		table, err := p2runtime.WorkerInitValues.Table(source)
		if err != nil {
			return nil, err
		}
		values := delta.NewJsonObject(table.Len())
		for _, key := range table.Keys() {
			contribute, _ := table.Get(key)
			value, err := contribute(session, rootCtx)
			if err != nil {
				return nil, err
			}
			values.Set(key, value)
		}
		if values.Value("session") == nil {
			return nil, errors.New("Session parameters unavailable; refusing worker start")
		}
		return values, nil
	}, ctx, host.RootOptions{})
	if err != nil {
		return nil, err
	}
	return result.(*delta.JsonObject), nil
}

// isOwnerNotice is whether a server host error is an owner notice: not an object that ended, a hold its factory refused
// (`forbidden`: the caller is told) or a worker's start (the session's own notice has it).
func isOwnerNotice(failure host.HostError) bool {
	text := fmt.Sprint(failure)
	for _, quiet := range []string{"released", "forbidden", "resolve_failed"} {
		if containsFold(text, quiet) {
			return false
		}
	}
	return true
}

type serverState struct {
	host         host.Host
	dataDir      string
	allowedHosts []string
	upgrades     *chordhttp.Upgrades
	loaded       chan struct{}
	// failed is closed when the start failed: a request that arrived meanwhile is let go.
	failed chan struct{}
	nonce  string
	web    *Web
	sprite *spriteHub
}

// authenticate is who the request is. Loopback is the device owner, except for a browser request another site made or
// frames (Fetch Metadata) other than a top-level navigation to an app page: no clickjacking or cross-site driving of the
// owner UI (resolveAccess also drops cookie principals of such requests). Tunnel requests are whoever an authenticator (a
// cookie) says. As in Pi Mobile, the device owner's browser carries a loopback owner cookie (set when it loads the app),
// which wins over a guest cookie; without it, a guest cookie wins over the cookieless loopback fallback, so a browser that
// joined a share on loopback is that guest everywhere.
func (s *serverState) authenticate(request *http.Request, origin p2runtime.RequestOrigin, ctx host.Ctx) (p2runtime.Principal, error) {
	source, err := s.host.Registry(ctx, p2runtime.Authenticators.Registry, host.GetOptions{})
	if err != nil {
		return nil, err
	}
	table, err := p2runtime.Authenticators.Table(source)
	if err != nil {
		return nil, err
	}
	var authenticated p2runtime.Principal
	for _, key := range table.Keys() {
		authenticator, _ := table.Get(key)
		principal, err := authenticator(request, origin, ctx)
		if err != nil {
			return nil, err
		}
		// Associated owners stay remote even on localhost; a guest cookie cannot downgrade them.
		if p2runtime.IsOwner(principal) {
			return principal, nil
		}
		if authenticated == nil {
			authenticated = principal
		}
	}
	// A top-level page navigation from another site may load the app too (a link from another local port): it can neither be
	// framed nor read back, and the page's own requests are same-origin.
	loopbackOwner := !origin.Remote && (!ForeignBrowserRequest(request) || PageNavigation(request))
	if loopbackOwner && authenticated != nil {
		has, err := p2runtime.HasLoopbackCookie(request, origin.Port, s.dataDir)
		if err != nil {
			return nil, err
		}
		if has {
			return p2runtime.OwnerPrincipal{Access: p2runtime.AccessLocal}, nil
		}
	}
	if authenticated != nil {
		return authenticated, nil
	}
	if loopbackOwner {
		return p2runtime.OwnerPrincipal{Access: p2runtime.AccessLocal}, nil
	}
	return nil, nil
}

func (s *serverState) credentialAccess(request *http.Request, ctx host.Ctx) (*p2runtime.RequestAccess, error) {
	source, err := s.host.Registry(ctx, p2runtime.RequestResolvers.Registry, host.GetOptions{})
	if err != nil {
		return nil, err
	}
	table, err := p2runtime.RequestResolvers.Table(source)
	if err != nil {
		return nil, err
	}
	for _, key := range table.Keys() {
		resolve, _ := table.Get(key)
		access := resolve(request, ctx)
		if access == nil {
			continue
		}
		if access.Principal == nil && access.Origin.RelayUser != nil {
			principal, err := s.authenticate(request, access.Origin, ctx)
			if err != nil {
				return nil, err
			}
			return &p2runtime.RequestAccess{Principal: principal, Origin: access.Origin}, nil
		}
		return access, nil
	}
	origin := RequestOrigin(request, s.allowedHosts)
	if origin == nil {
		return nil, nil
	}
	principal, err := s.authenticate(request, *origin, ctx)
	if err != nil {
		return nil, err
	}
	return &p2runtime.RequestAccess{Principal: principal, Origin: *origin}, nil
}

// resolveAccess: a browser request another site made (Fetch Metadata: cross-site, or same-site such as a page on another
// local port, whose cookies SameSite does not separate) gets no principal from any credential: no fetch, module, upload or
// WebSocket of a foreign page can read or drive p2 with the owner's or a guest's cookie. Only a top-level navigation to an
// app page authenticates, so links into p2 open it normally. Radius-relayed requests are authenticated per channel by
// Radius (and framed by its shell), not by browser cookies; requests without Fetch Metadata (the TUI, scripts) are
// unaffected.
func (s *serverState) resolveAccess(request *http.Request, ctx host.Ctx) (*p2runtime.RequestAccess, error) {
	access, err := s.credentialAccess(request, ctx)
	if err != nil || access == nil {
		return access, err
	}
	if access.Principal != nil && access.Origin.RelayShellOrigin == "" && ForeignBrowserRequest(request) && !PageNavigation(request) {
		return &p2runtime.RequestAccess{Origin: access.Origin}, nil
	}
	return access, nil
}

// ServeHTTP handles a request once the plugins loaded (an upgrade too: its handshake waits as well).
func (s *serverState) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	select {
	case <-s.loaded:
	case <-s.failed:
		http.Error(response, "Server startup failed", http.StatusServiceUnavailable)
		return
	}
	if isUpgrade(request) {
		s.upgrade(response, request)
		return
	}
	s.handle(response, request)
}

func isUpgrade(request *http.Request) bool {
	return headerHasToken(request.Header.Get("Connection"), "upgrade") && headerHasToken(request.Header.Get("Upgrade"), "websocket")
}

func (s *serverState) upgrade(response http.ResponseWriter, request *http.Request) {
	path := request.URL.EscapedPath()
	var principal p2runtime.Principal
	_, err := s.host.Root(func(ctx host.Ctx) (any, error) {
		access, err := s.resolveAccess(request, ctx)
		if err == nil && access != nil && SameOrigin(request, access.Origin) {
			principal = access.Principal
		}
		return nil, err
	}, chordctx.Background(), host.RootOptions{})
	if err != nil || principal == nil {
		http.Error(response, "Forbidden", http.StatusForbidden)
		return
	}
	if path == "/chord" {
		// Mobile's limits: frames up to 1 MiB (socket-limits.ts); Chord queues calls behind its window.
		part := float64(MaxClientFrame)
		s.upgrades.Link(response, request, chordhttp.UpgradeAdmission{Principal: p2runtime.ToChord(principal)},
			chordhttp.LinkOptions{MaxPayload: MaxClientFrame, Limits: link.PartLimits{Part: &part}}, s.host, chordctx.Background(), &chordhttp.Router{},
			// p2 keeps no links component (`links.opened`): nothing runs when a socket opens or closes.
			func() func() { return nil })
		return
	}
	_, err = s.host.Root(func(ctx host.Ctx) (any, error) {
		source, err := s.host.Registry(ctx, p2runtime.Upgrades.Registry, host.GetOptions{})
		if err != nil {
			return nil, err
		}
		table, err := p2runtime.Upgrades.Table(source)
		if err != nil {
			return nil, err
		}
		handler, ok := table.Get(path)
		if !ok {
			http.Error(response, "Forbidden", http.StatusForbidden)
			return nil, nil
		}
		return nil, handler(response, request, principal, ctx)
	}, chordctx.Background(), host.RootOptions{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "p2 server: upgrade failed", err)
	}
}

func (s *serverState) handle(response http.ResponseWriter, request *http.Request) {
	header := response.Header()
	header.Set("Cache-Control", "no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")
	// Nothing frames the app (a response with its own CSP, e.g. an artifact preview, sets its own frame-ancestors).
	header.Set("Content-Security-Policy", ContentSecurityPolicy(s.nonce, nil))
	header.Set("X-Frame-Options", "DENY")
	path := request.URL.EscapedPath()
	// A stream (the sprite events) outlives the host's root scope: dispatch hands it back to run once the scope has ended, so
	// closing the host never waits for it.
	var stream func()
	_, err := s.host.Root(func(ctx host.Ctx) (any, error) {
		return nil, s.dispatch(response, request, path, ctx, &stream)
	}, chordctx.Background(), host.RootOptions{})
	if err == nil && stream != nil {
		stream()
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "p2 server: request failed", err)
		http.Error(response, "", http.StatusInternalServerError)
	}
}

func (s *serverState) dispatch(response http.ResponseWriter, request *http.Request, path string, ctx host.Ctx, stream *func()) error {
	access, err := s.resolveAccess(request, ctx)
	if err != nil {
		return err
	}
	if access != nil {
		response.Header().Set("Content-Security-Policy", ContentSecurityPolicy(s.nonce, &access.Origin))
		// The Radius shell frames the relayed UI; X-Frame-Options cannot name another origin.
		if access.Origin.RelayShellOrigin != "" {
			response.Header().Del("X-Frame-Options")
		}
	}
	if access == nil || !SameOrigin(request, access.Origin) {
		response.Header().Set("Content-Type", "text/plain")
		response.WriteHeader(http.StatusForbidden)
		if access == nil {
			_, _ = response.Write([]byte("Localhost only"))
		} else {
			_, _ = response.Write([]byte("Invalid origin"))
		}
		return nil
	}
	principal, origin := access.Principal, access.Origin
	get := request.Method == http.MethodGet || request.Method == http.MethodHead
	// Unauthenticated readiness (Pi Mobile's /health, polled by launchers); known Hosts only.
	if path == "/health" && get {
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusOK)
		if request.Method != http.MethodHead {
			_, _ = response.Write([]byte(`{"ok":true,"name":"p2"}`))
		}
		return nil
	}
	// The installable app's public files (web.go `IsPublicFile`), for home screens that fetch them without cookies
	// (same-origin, or without browser site headers). Not for another site's page: an image there loading p2's icon would
	// tell it a p2 runs at that address (on localhost above all).
	if s.web != nil && IsPublicFile(path) && get && !ForeignBrowserRequest(request) {
		s.web.Handle(response, request)
		return nil
	}
	// The chosen PiG sprite, a public file like the icons (see SpriteAssetPath).
	if s.sprite != nil && (path == SpriteAssetPath || path == LoginAssetPath) && get && !ForeignBrowserRequest(request) {
		s.sprite.serveAsset(response, request)
		return nil
	}
	if path == "/__p2_dev" {
		// Dev mode is designed out (D473): the endpoint answers as a server without it (P2_DEV unset).
		if !p2runtime.IsOwner(principal) || (request.Method == http.MethodPost && !p2runtime.FromOrigin(request, origin.Origin)) {
			response.WriteHeader(http.StatusForbidden)
		} else if request.Method == http.MethodPost {
			if _, err := ReadDevCommand(request); err != nil {
				return p2runtime.SendJSON(response, http.StatusBadRequest, DevResult{Message: "Error: " + err.Error()}, nil)
			}
			return p2runtime.SendJSON(response, http.StatusConflict, DevResult{Message: "Dev mode is not enabled"}, nil)
		} else {
			return p2runtime.SendJSON(response, http.StatusOK, nil, nil)
		}
		return nil
	}
	source, err := s.host.Registry(ctx, p2runtime.Routes.Registry, host.GetOptions{})
	if err != nil {
		return err
	}
	routes, err := p2runtime.Routes.Table(source)
	if err != nil {
		return err
	}
	if route, ok := p2runtime.FindRoute(routes, request.Method, path); ok {
		return route(response, request, p2runtime.RequestAccess{Principal: principal, Origin: origin}, ctx)
	}
	// The local owner's browser loading the app gets the loopback owner cookie (Pi Mobile sets it on `/`).
	if owner, ok := principal.(p2runtime.OwnerPrincipal); ok && owner.Access == p2runtime.AccessLocal && !origin.Remote && get &&
		(path == "/" || len(path) > 3 && path[:3] == "/s/") {
		cookie, err := p2runtime.LoopbackCookie(origin.Port, s.dataDir)
		if err != nil {
			return err
		}
		response.Header().Set("Set-Cookie", cookie)
	}
	if s.sprite != nil && path == SpriteEventsPath && get && (p2runtime.IsOwner(principal) || p2runtime.IsGuest(principal)) {
		*stream = func() { s.sprite.serveEvents(response, request) }
		return nil
	}
	guest, isGuest := principal.(p2runtime.GuestPrincipal)
	if p2runtime.IsOwner(principal) || (isGuest && s.web != nil && s.web.GuestMayLoad(request, guest)) {
		if s.web != nil {
			s.web.Handle(response, request)
			return nil
		}
		response.Header().Set("Content-Type", "text/plain")
		response.WriteHeader(http.StatusNotFound)
		_, _ = response.Write([]byte("Not found"))
		return nil
	}
	if isGuest {
		response.Header().Set("Content-Type", "text/plain")
		response.WriteHeader(http.StatusForbidden)
		_, _ = response.Write([]byte("Forbidden"))
		return nil
	}
	_, _ = io.Copy(io.Discard, request.Body)
	// A Radius user is signed in already; owner sign-in is not for them.
	if origin.RelayUser != nil {
		response.Header().Set("Content-Type", "text/plain; charset=utf-8")
		response.WriteHeader(http.StatusForbidden)
		_, _ = response.Write([]byte("Nothing on this brain is shared with you. Ask its owner for a share link.\n"))
		return nil
	}
	p2runtime.SignIn(response, request, http.StatusUnauthorized)
	return nil
}

func containsFold(text, part string) bool {
	return strings.Contains(strings.ToLower(text), part)
}

// headerHasToken is whether the comma-separated header value lists token (case-insensitive).
func headerHasToken(value, token string) bool {
	for _, part := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}
