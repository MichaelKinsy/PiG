package experimental

// Ports packages/coding-agent/src/experimental/client-runtime.ts.

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/experimental/client"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

// ClientRuntime owns the live Unix client and service namespaces for one presentation. Dispose joins sources before transport lifetimes.
type ClientRuntime struct {
	Servers   []*ClientRuntimeServer
	sources   []func(context.Context) error
	clients   []*client.Client
	disposeMu sync.Mutex
	disposed  bool
}

// Dispose marks the runtime disposed before starting cleanup. The first caller joins every cleanup phase; subsequent or reentrant callers return immediately, as upstream's disposed flag does.
func (runtime *ClientRuntime) Dispose() error {
	runtime.disposeMu.Lock()
	if runtime.disposed {
		runtime.disposeMu.Unlock()
		return nil
	}
	runtime.disposed = true
	runtime.disposeMu.Unlock()
	sources := make([]func() error, len(runtime.sources))
	for index, dispose := range runtime.sources {
		sources[index] = func() error { return dispose(context.Background()) }
	}
	clients := make([]func() error, len(runtime.clients))
	for index, peer := range runtime.clients {
		clients[index] = func() error { return disposeServerClient(context.Background(), peer) }
	}
	var failures []error
	for _, phase := range [][]func() error{sources, clients} {
		failures = append(failures, settleClientCleanup(phase)...)
	}
	return clientRuntimeError("Failed to dispose experimental client runtime", failures)
}

func settleClientCleanup(operations []func() error) []error {
	results := make([]error, len(operations))
	var tasks sync.WaitGroup
	for index, operation := range operations {
		tasks.Go(func() {
			defer recoverSourceError(&results[index])
			results[index] = operation()
		})
	}
	tasks.Wait()
	return results
}

func clientRuntimeError(message string, failures []error) error {
	var values []any
	for _, failure := range failures {
		if failure != nil {
			values = append(values, failure)
		}
	}
	switch len(values) {
	case 0:
		return nil
	case 1:
		return values[0].(error)
	default:
		return &services.AggregateError{Message: message, Errors: values}
	}
}

// OpenClientRuntime validates selection, discovers or activates local servers, and opens real server/Session sources. Startup failures close all previously acquired resources before returning.
func OpenClientRuntime(ctx context.Context, command ClientCommand, options OpenClientRuntimeOptions) (_ *ClientRuntime, err error) {
	// pig divergence (D64): experimental Radius is designed out; reject non-Unix routes before discovery or activation.
	if command.Connect != nil && command.Connect.Transport != "unix" {
		return nil, errors.New("Experimental clients support only Unix transport")
	}
	if command.Provider != nil && command.Model == nil {
		return nil, errors.New("Server model provider requires a model")
	}
	if command.Connect != nil && command.Model != nil {
		return nil, errors.New("Model selection is only valid when automatically activating a new server")
	}
	directory, err := ResolveServerDirectory(options.Directory)
	if err != nil {
		return nil, err
	}
	var routes []ClientRuntimeRoute
	var activatedClient *client.Client
	if command.Connect != nil {
		var route ClientRuntimeRoute
		route, err = routeFromExplicitPath(command.Connect.Path)
		if err != nil {
			return nil, err
		}
		routes = []ClientRuntimeRoute{route}
	} else {
		discovered, err := client.DiscoverUnixServers(ctx, client.DiscoverUnixServersOptions{Directory: directory})
		if err != nil {
			return nil, err
		}
		for _, route := range discovered {
			routes = append(routes, ClientRuntimeRoute{Transport: "unix", ServerId: route.ServerId, Path: new(route.Path)})
		}
		if len(routes) > 0 && command.Model != nil {
			return nil, errors.New("Model selection is only valid when automatically activating a new server")
		}
		if len(routes) == 0 {
			sessionDir, err := ResolveSessionDirectory(nil)
			if err != nil {
				return nil, err
			}
			activated, err := ActivateServer(ctx, ActivateServerOptions{Directory: directory, RequestedServerId: requestedServerId(nil), SessionDir: sessionDir, Provider: command.Provider, Model: command.Model})
			if err != nil {
				return nil, err
			}
			activatedClient = activated.Client
			routes = []ClientRuntimeRoute{{Transport: "unix", ServerId: activated.Route.ServerId, Path: new(activated.Route.Path)}}
		}
	}
	if command.PluginPackages != nil && len(routes) != 1 {
		return nil, errors.New("Plugin selection requires exactly one local server")
	}
	runtime := &ClientRuntime{Servers: []*ClientRuntimeServer{}}
	defer func() {
		if err != nil {
			if cleanup := runtime.Dispose(); cleanup != nil {
				err = &services.AggregateError{Message: "Experimental client startup and cleanup failed", Errors: []any{err, cleanup}}
			}
		}
	}()
	for _, route := range routes {
		peer := activatedClient
		if peer == nil {
			peer, err = connectRuntimeRoute(ctx, route)
			if err != nil {
				//nolint:errorlint // Upstream instanceof checks the outer ServerError; wrapped failures do not trigger automatic reactivation.
				failure, version := err.(*client.ServerError)
				if command.Connect != nil || route.Transport != "unix" || !version || failure.Code != "version" {
					return nil, err
				}
				sessionDir, resolveError := ResolveSessionDirectory(nil)
				if resolveError != nil {
					return nil, resolveError
				}
				activated, activationError := ActivateServer(ctx, ActivateServerOptions{Directory: directory, RequestedServerId: new(route.ServerId), SessionDir: sessionDir})
				if activationError != nil {
					return nil, activationError
				}
				peer, err = activated.Client, nil
			}
		}
		activatedClient = nil
		runtime.clients = append(runtime.clients, peer)
		server, err := NewClientServerServiceSource(peer, ClientServiceSourceOptions{})
		if err != nil {
			return nil, err
		}
		runtime.sources = append(runtime.sources, server.Dispose)
		session, err := NewClientSessionServiceSource(peer, ClientServiceSourceOptions{})
		if err != nil {
			return nil, err
		}
		runtime.sources = append(runtime.sources, session.Dispose)
		runtime.Servers = append(runtime.Servers, &ClientRuntimeServer{Route: route, Client: peer, Server: server, Session: session})
	}
	return runtime, nil
}

// The static constructor is separate from discovery/activation's NewClient plus instance Connect paths.
var connectRuntimeClient = client.Connect

func connectRuntimeRoute(ctx context.Context, route ClientRuntimeRoute) (*client.Client, error) {
	factory, err := client.CreateUnixTransportFactory(client.UnixTransportOptions{Path: *route.Path})
	if err != nil {
		return nil, err
	}
	return connectRuntimeClient(ctx, client.ClientOptions{ServerId: route.ServerId, TransportFactory: factory})
}

func routeFromExplicitPath(path string) (ClientRuntimeRoute, error) {
	name := filepath.Base(path)
	id, ok := strings.CutSuffix(name, ".sock")
	if !ok || !protocol.IsServerId(id) {
		return ClientRuntimeRoute{}, errors.New("--connect path must end with <uuidv4-server-id>.sock")
	}
	return ClientRuntimeRoute{Transport: "unix", ServerId: id, Path: new(path)}, nil
}
