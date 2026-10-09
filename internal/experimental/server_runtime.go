package experimental

// Ports packages/coding-agent/src/experimental/server.ts

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/google/uuid"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

// StartServerOptions preserves omitted paths, identity, model selection, lifetime policy, and plugin selection separately from explicit empty values.
type StartServerOptions struct {
	Directory      *string
	ServerId       *string
	SessionDir     *string
	Provider       *string
	Model          *string
	KeepAlive      *bool
	PluginPackages []string
}

// RunningServer owns one replaceable Unix server generation. Closed observes backend/catalog closure; Close also joins worker shutdown and coordinator cleanup.
type RunningServer struct {
	ServerId   string
	SocketPath string
	SessionDir string
	Server     *routing.Server

	backend      *runningServerBackend
	workers      *SessionWorkerManager
	coordinator  *CoordinatorConnection
	lifetime     *ServerLifetime
	closed       chan struct{}
	replacedDone chan struct{}
	mu           sync.Mutex
	closedErr    error
	closeOnce    sync.Once
	closeErr     error
}

func (server *RunningServer) WorkerPids() map[string]int { return server.workers.WorkerPids() }
func (server *RunningServer) Closed() <-chan struct{}    { return server.closed }
func (server *RunningServer) ClosedError() error {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.closedErr
}

// Close stops lifetime timers and joins the generation's cleanup once. Replaced generations detach without stopping workers adopted by their replacement.
func (server *RunningServer) Close() error {
	server.lifetime.Stop()
	server.closeOnce.Do(func() {
		server.closeErr = server.backend.close()
		if server.coordinator.WasReplaced() {
			server.workers.Detach()
		} else if err := server.workers.Shutdown(); err != nil {
			// server.ts:667-679 awaits workers.shutdown() inside the finally of the backend.close() try; a throw from finally replaces the pending backend error.
			server.closeErr = err
		}
		server.coordinator.Close()
	})
	<-server.closed
	<-server.replacedDone
	return server.closeErr
}

func (server *RunningServer) observe() {
	go func() {
		defer close(server.replacedDone)
		select {
		case <-server.coordinator.Replaced():
			server.lifetime.Stop()
			server.workers.Detach()
			// Backend failure is retained by the closed observer.
			_ = server.backend.close()
			server.coordinator.Close()
		case <-server.closed:
		}
	}()
	go func() {
		<-server.backend.closed
		server.backend.mu.Lock()
		failure := server.backend.closedErr
		server.backend.mu.Unlock()
		server.mu.Lock()
		server.closedErr = failure
		server.mu.Unlock()
		close(server.closed)
	}()
}

// StartServer starts a replaceable Unix server behind its stable coordinator endpoint. Startup owns its profile lock until service discovery and lifetime observation finish. No hosted relay is opened (D64).
func StartServer(ctx context.Context, options StartServerOptions) (runtime *RunningServer, err error) {
	if options.Provider != nil && options.Model == nil {
		return nil, errors.New("Server model provider requires a model")
	}
	var model *SessionWorkerModel
	if options.Model != nil {
		model = &SessionWorkerModel{Model: *options.Model}
		if options.Provider != nil {
			model.Provider = new(*options.Provider)
		}
	}
	directory, err := ResolveServerDirectory(options.Directory)
	if err != nil {
		return nil, err
	}
	profile, err := AcquireServerProfile(ctx, directory, requestedServerId(options.ServerId))
	if err != nil {
		return nil, err
	}
	serverId := profile.ServerID
	keepAlive := options.KeepAlive == nil || *options.KeepAlive
	lifetime := NewServerLifetime(keepAlive)
	var backend *runningServerBackend
	var coordinator *CoordinatorConnection
	var startupLease *CoordinatorStartupLease
	var workers *SessionWorkerManager
	released := false
	defer func() {
		if err == nil {
			return
		}
		lifetime.Stop()
		if startupLease != nil {
			startupLease.Close()
		}
		replaced := coordinator != nil && coordinator.WasReplaced()
		if replaced && workers != nil {
			workers.Detach()
		}
		failure := err
		err = settleServerCleanup("Server runtime startup and cleanup failed",
			func() error { return failure },
			func() error {
				if backend != nil {
					return backend.close()
				}
				return nil
			},
			func() error {
				if workers != nil && !replaced {
					return workers.Shutdown()
				}
				return nil
			},
			func() error {
				if coordinator != nil {
					coordinator.Close()
				}
				return nil
			},
			func() error {
				if !released {
					return profile.Release()
				}
				return nil
			},
		)
		if runtime != nil {
			// Join any retirement close that began while startup still owned the profile.
			_ = runtime.Close()
		}
		runtime = nil
	}()
	if err := EnsurePrivateServerDirectory(directory); err != nil {
		return nil, err
	}
	packagePaths, err := RestoreServerPluginPackageProfile(directory, serverId, options.PluginPackages)
	if err != nil {
		return nil, err
	}
	var pluginMu sync.Mutex
	packages := make(map[string]*ConfiguredServerPluginPackage)
	getPluginPackage := func(path string) (*ConfiguredServerPluginPackage, error) {
		pluginMu.Lock()
		defer pluginMu.Unlock()
		if cached := packages[path]; cached != nil {
			return cached, nil
		}
		configured, err := CreateServerPluginPackage(directory, serverId, path)
		if err != nil {
			return nil, err
		}
		packages[path] = configured
		return configured, nil
	}
	buildPluginSelection := func(ctx context.Context, paths []string) (resolvedSessionPlugins, error) {
		normalized, err := NormalizePluginPackagePaths(paths)
		if err != nil {
			return resolvedSessionPlugins{}, err
		}
		plugins := make([]*ConfiguredServerPluginPackage, len(normalized))
		manifests := make([]string, len(normalized))
		for i, path := range normalized {
			plugins[i], err = getPluginPackage(path)
			if err != nil {
				return resolvedSessionPlugins{}, err
			}
			manifests[i] = plugins[i].ManifestPath
		}
		artifacts := make([][]FacetBundleArtifact, len(plugins))
		var work sync.WaitGroup
		var firstFailure sync.Once
		var buildError error
		for i, plugin := range plugins {
			work.Go(func() {
				built, failure := plugin.Build(ctx)
				artifacts[i] = built
				if failure != nil {
					firstFailure.Do(func() { buildError = failure })
				}
			})
		}
		work.Wait()
		if buildError != nil {
			return resolvedSessionPlugins{}, buildError
		}
		return resolvedSessionPlugins{packagePaths: normalized, manifestPaths: manifests, presentationArtifacts: slices.Concat(artifacts...)}, nil
	}
	defaultSelection, err := buildPluginSelection(ctx, packagePaths)
	if err != nil {
		return nil, err
	}
	var selectionsMu sync.Mutex
	selections := make(map[string]resolvedSessionPlugins)
	resolveSessionPlugins := func(ctx context.Context, metadata SessionCatalogMetadata, requested []string) (resolvedSessionPlugins, error) {
		if requested != nil {
			normalized, err := NormalizePluginPackagePaths(requested)
			if err != nil {
				return resolvedSessionPlugins{}, err
			}
			selectionsMu.Lock()
			current, exists := selections[metadata.Path]
			selectionsMu.Unlock()
			if exists && slices.Equal(current.packagePaths, normalized) {
				return current, nil
			}
			manifests := make([]string, len(normalized))
			for i, path := range normalized {
				plugin, err := getPluginPackage(path)
				if err != nil {
					return resolvedSessionPlugins{}, err
				}
				manifests[i] = plugin.ManifestPath
			}
			if err := workers.AssertSessionPluginManifestPaths(metadata, manifests); err != nil {
				return resolvedSessionPlugins{}, err
			}
			candidate, err := buildPluginSelection(ctx, normalized)
			if err != nil {
				return resolvedSessionPlugins{}, err
			}
			if err := workers.AssertSessionPluginManifestPaths(metadata, candidate.manifestPaths); err != nil {
				return resolvedSessionPlugins{}, err
			}
			if err := WriteSessionPluginPackageProfile(directory, serverId, metadata.Path, candidate.packagePaths); err != nil {
				return resolvedSessionPlugins{}, err
			}
			selectionsMu.Lock()
			selections[metadata.Path] = candidate
			selectionsMu.Unlock()
			return candidate, nil
		}
		selectionsMu.Lock()
		cached, exists := selections[metadata.Path]
		selected := defaultSelection
		selectionsMu.Unlock()
		if exists {
			return cached, nil
		}
		stored, err := ReadSessionPluginPackageProfile(directory, serverId, metadata.Path)
		if err != nil {
			return resolvedSessionPlugins{}, err
		}
		if stored != nil {
			selected, err = buildPluginSelection(ctx, stored)
			if err != nil {
				return resolvedSessionPlugins{}, err
			}
		} else if err := WriteSessionPluginPackageProfile(directory, serverId, metadata.Path, selected.packagePaths); err != nil {
			return resolvedSessionPlugins{}, err
		}
		selectionsMu.Lock()
		selections[metadata.Path] = selected
		selectionsMu.Unlock()
		return selected, nil
	}
	removeSessionPlugins := func(metadata SessionCatalogMetadata) error {
		selectionsMu.Lock()
		delete(selections, metadata.Path)
		selectionsMu.Unlock()
		return RemoveSessionPluginPackageProfile(directory, serverId, metadata.Path)
	}
	reloadPresentationFacetBundles := func(ctx context.Context, paths []string) ([]FacetBundleArtifact, error) {
		reloaded, err := buildPluginSelection(ctx, paths)
		if err != nil {
			return nil, err
		}
		selectionsMu.Lock()
		defer selectionsMu.Unlock()
		if slices.Equal(defaultSelection.packagePaths, reloaded.packagePaths) {
			defaultSelection = reloaded
		}
		for path, selected := range selections {
			if slices.Equal(selected.packagePaths, reloaded.packagePaths) {
				selections[path] = reloaded
			}
		}
		return reloaded.presentationArtifacts, nil
	}
	socketPath, err := routing.GetUnixSocketPath(serverId, directory)
	if err != nil {
		return nil, err
	}
	controlPath := filepath.Join(directory, "control-"+serverId+".sock")
	nonce := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	serverPath := filepath.Join(directory, "server-"+serverId+"-"+nonce+".sock")
	startupLease, err = EnsureCoordinator(ctx, socketPath, controlPath)
	if err != nil {
		return nil, err
	}
	coordinator = NewCoordinatorConnection(CoordinatorConnectionOptions{ControlPath: controlPath, Endpoint: serverPath})
	sessionDir, err := ResolveSessionDirectory(options.SessionDir)
	if err != nil {
		return nil, err
	}
	workers = newLifetimeSessionWorkerManager(coordinator, sessionDir, model, lifetime)
	backend, err = startServerBackend(startServerBackendOptions{
		path: serverPath, serverId: serverId, sessionDir: options.SessionDir,
		resolveSessionPlugins: resolveSessionPlugins, removeSessionPlugins: removeSessionPlugins,
		reloadPresentationFacetBundles: reloadPresentationFacetBundles,
	}, workers, lifetime.SetConnectionCount)
	if err != nil {
		return nil, err
	}
	if err := coordinator.Connect(ctx); err != nil {
		return nil, err
	}
	startupLease.Close()
	startupLease = nil
	if err := workers.Discover(coordinator.PeerIDs()); err != nil {
		return nil, err
	}
	if err := backend.services.Refresh(context.Background()); err != nil {
		return nil, err
	}
	// pig divergence (D64): the owner designed out experimental Radius relay composition on 2026-09-28; the native Unix backend is the complete transport boundary.
	runtime = &RunningServer{
		ServerId: serverId, SessionDir: backend.sessionDir, SocketPath: socketPath, Server: backend.server,
		backend: backend, workers: workers, coordinator: coordinator, lifetime: lifetime,
		closed: make(chan struct{}), replacedDone: make(chan struct{}),
	}
	runtime.observe()
	activeRuntime := runtime
	lifetime.Start(func() { _ = activeRuntime.Close() })
	released = true
	if err := profile.Release(); err != nil {
		return runtime, err
	}
	return runtime, nil
}

// StartForegroundServer holds an operator-owned generation while serializing with automatic cold activation.
func StartForegroundServer(ctx context.Context, options StartServerOptions) (runtime *RunningServer, err error) {
	directory, err := ResolveServerDirectory(options.Directory)
	if err != nil {
		return nil, err
	}
	if err := EnsurePrivateServerDirectory(directory); err != nil {
		return nil, err
	}
	profile, err := AcquireServerProfile(ctx, directory, requestedServerId(options.ServerId))
	if err != nil {
		return nil, err
	}
	serverId := profile.ServerID
	if err := profile.Release(); err != nil {
		return nil, err
	}
	release, err := AcquireServerActivation(ctx, directory, serverId)
	if err != nil {
		return nil, err
	}
	defer func() {
		if releaseErr := release(); releaseErr != nil {
			err = releaseErr
		}
	}()
	options.Directory, options.ServerId, options.KeepAlive = &directory, &serverId, new(true)
	return StartServer(ctx, options)
}

type resolvedSessionPlugins struct {
	packagePaths          []string
	manifestPaths         []string
	presentationArtifacts []FacetBundleArtifact
}

type startServerBackendOptions struct {
	path                           string
	serverId                       string
	sessionDir                     *string
	resolveSessionPlugins          func(context.Context, SessionCatalogMetadata, []string) (resolvedSessionPlugins, error)
	removeSessionPlugins           func(SessionCatalogMetadata) error
	reloadPresentationFacetBundles func(context.Context, []string) ([]FacetBundleArtifact, error)
}

type runningServerBackend struct {
	server     *routing.Server
	sessionDir string
	services   *services.ExperimentalServerServices
	closed     chan struct{}
	mu         sync.Mutex
	closedErr  error
}

func startServerBackend(options startServerBackendOptions, workers *SessionWorkerManager, onConnectionCountChanged func(int)) (*runningServerBackend, error) {
	sessionDir, err := ResolveSessionDirectory(options.sessionDir)
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	// server.ts:383-387: the catalog lists the Sessions on disk, and a tracked worker's Session replaces its listed entry.
	listSessions := func() ([]SessionCatalogMetadata, error) {
		metadata, err := ListSessions(sessionDir)
		if err != nil {
			return nil, err
		}
		byPath := make(map[string]int, len(metadata))
		for i, item := range metadata {
			byPath[item.Path] = i
		}
		for _, tracked := range workers.TrackedSessions() {
			if i, exists := byPath[tracked.Path]; exists {
				metadata[i] = tracked
			} else {
				byPath[tracked.Path] = len(metadata)
				metadata = append(metadata, tracked)
			}
		}
		return metadata, nil
	}
	// server.ts:388-393: a tracked worker's Session wins, then the catalog; the catalog holds one directory per ID, so there is no ambiguity.
	resolveSession := func(_ context.Context, id string) (SessionCatalogMetadata, error) {
		for _, tracked := range workers.TrackedSessions() {
			if tracked.ID == id {
				return tracked, nil
			}
		}
		if metadata := ReadSession(sessionDir, id); metadata != nil {
			return *metadata, nil
		}
		return SessionCatalogMetadata{}, routing.NewSessionNotFoundError(fmt.Sprintf("Unknown session: %s", id))
	}
	summarize := func(metadata SessionCatalogMetadata) services.SessionSummary {
		return services.SessionSummary{SessionAddress: services.SessionAddress{ServerId: options.serverId, SessionId: metadata.ID}, CreatedAt: metadata.CreatedAt}
	}
	serverServices, err := services.CreateExperimentalServerServices(services.ExperimentalServerServicesOptions{
		List: func(context.Context) ([]services.SessionSummary, error) {
			metadata, err := listSessions()
			if err != nil {
				return nil, err
			}
			result := make([]services.SessionSummary, len(metadata))
			for i, item := range metadata {
				result[i] = summarize(item)
			}
			collator := collate.New(language.Und)
			slices.SortStableFunc(result, func(left, right services.SessionSummary) int {
				if order := collator.CompareString(left.SessionId, right.SessionId); order != 0 {
					return order
				}
				return cmp.Compare(left.CreatedAt, right.CreatedAt)
			})
			return result, nil
		},
		Create: func(_ context.Context, createOptions services.SessionCreateOptions) (services.SessionSummary, error) {
			metadata, err := CreateSession(sessionDir, CreateSessionOptions{ID: createOptions.Id, Cwd: cwd})
			if err != nil {
				return services.SessionSummary{}, err
			}
			return summarize(metadata), nil
		},
		Remove: func(ctx context.Context, id string) error {
			metadata, err := resolveSession(ctx, id)
			if err != nil {
				return err
			}
			if err := workers.CloseSession(ctx, metadata); err != nil {
				return err
			}
			if err := DeleteSession(metadata); err != nil {
				return err
			}
			return options.removeSessionPlugins(metadata)
		},
		PrepareSessionPlugins: func(ctx context.Context, id string, paths []string) (services.PreparedSessionPlugins, error) {
			metadata, err := resolveSession(ctx, id)
			if err != nil {
				return services.PreparedSessionPlugins{}, err
			}
			selected, err := options.resolveSessionPlugins(ctx, metadata, paths)
			if err == nil {
				err = workers.AssertSessionPluginManifestPaths(metadata, selected.manifestPaths)
			}
			if err != nil {
				if conflict, ok := errors.AsType[*SessionPluginSelectionConflictError](err); ok {
					return services.PreparedSessionPlugins{}, routing.NewServerError("service_invalid_value", conflict.Message)
				}
				return services.PreparedSessionPlugins{}, err
			}
			return services.PreparedSessionPlugins{PackagePaths: selected.packagePaths, PresentationPlugins: CreatePresentationFacetData(selected.presentationArtifacts)}, nil
		},
		ReloadPresentationPlugins: func(ctx context.Context, paths []string) (chord.JsonValue, error) {
			artifacts, err := options.reloadPresentationFacetBundles(ctx, paths)
			if err != nil {
				return nil, err
			}
			return CreatePresentationFacetData(artifacts), nil
		},
	})
	if err != nil {
		return nil, err
	}
	closeCatalog := serverServices.Dispose
	host := routing.ServerHost{
		ServerServices: serverRuntimeServiceHost{host: serverServices.Host},
		ResolveSession: func(ctx context.Context, id string) (routing.SessionMetadata, error) {
			return resolveSession(ctx, id)
		},
		OpenSession: func(ctx context.Context, resolved routing.SessionMetadata) (routing.RoutedSessionHandle, error) {
			metadata := resolved.(SessionCatalogMetadata)
			selected, err := options.resolveSessionPlugins(ctx, metadata, nil)
			if err != nil {
				return nil, err
			}
			handle, err := workers.OpenSession(ctx, metadata, selected.manifestPaths)
			if err != nil {
				return nil, err
			}
			return serverRuntimeSessionHandle{RoutedSessionHandle: handle}, nil
		},
	}
	server, err := routing.CreateUnixServer(host, routing.UnixServerOptions{ServerId: options.serverId, Path: options.path, Mode: new(float64(0o600)), OnConnectionCountChanged: onConnectionCountChanged})
	if err != nil {
		return nil, settleServerCleanup("Experimental server startup and cleanup failed", func() error { return err }, closeCatalog)
	}
	if _, err := server.Start(); err != nil {
		return nil, settleServerCleanup("Experimental server startup and cleanup failed", func() error { return err }, server.Close, closeCatalog)
	}
	backend := &runningServerBackend{server: server, sessionDir: sessionDir, services: serverServices, closed: make(chan struct{})}
	go func() {
		<-server.Closed()
		failure := settleBackendClosure(server.ClosedError, closeCatalog)
		backend.mu.Lock()
		backend.closedErr = failure
		backend.mu.Unlock()
		close(backend.closed)
	}()
	return backend, nil
}

// settleBackendClosure reports the listener's closing failure and the catalog disposal failure; both together form server.ts:469's AggregateError("Server and repository shutdown failed"), whose message keeps upstream's name for the catalog.
func settleBackendClosure(serverError, closeCatalog func() error) error {
	return settleServerCleanup("Server and repository shutdown failed", serverError, closeCatalog)
}

func (backend *runningServerBackend) close() error {
	// The closed result includes listener and catalog failures for every caller.
	_ = backend.server.Close()
	<-backend.closed
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.closedErr
}

func settleServerCleanup(message string, operations ...func() error) error {
	failures := make([]error, len(operations))
	var work sync.WaitGroup
	for i, operation := range operations {
		work.Go(func() { failures[i] = operation() })
	}
	work.Wait()
	var causes []any
	for _, failure := range failures {
		if failure != nil {
			causes = append(causes, failure)
		}
	}
	if len(causes) == 0 {
		return nil
	}
	if len(causes) == 1 {
		return causes[0].(error)
	}
	return &services.AggregateError{Message: message, Errors: causes}
}

type serverRuntimeServiceHost struct {
	host *services.RoutedServerServiceHost
}

func (host serverRuntimeServiceHost) AttachClient(ctx context.Context, presentation routing.RoutedServerPresentation) (routing.RoutedServerServiceAttachment, error) {
	return host.host.AttachClient(ctx, presentation)
}

type serverRuntimeSessionHandle struct{ *RoutedSessionHandle }

func (handle serverRuntimeSessionHandle) AttachClient(ctx context.Context) (routing.RoutedSessionAttachment, error) {
	return handle.RoutedSessionHandle.AttachClient(ctx)
}
