// Ports packages/coding-agent/src/experimental/services/worker.ts.
package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/chord"
)

// TaskGraphActivity reports whether the durable Harness has a live task. Subscribe delivers the current activity and then every change, and returns its unsubscribe function; Dispose detaches the observation.
type TaskGraphActivity interface {
	Subscribe(func(active bool)) func()
	Dispose()
}

// SessionWorkerConversation is the root conversation the worker services expose: the command, configuration and view capabilities their providers consume.
type SessionWorkerConversation interface {
	AgentConversation
	ModelsServiceConversation
	TranscriptConversation
}

// WorkerServiceScope binds one endpoint to an attachment on a server connection.
type WorkerServiceScope struct {
	ServerConnectionId string
	AttachmentId       string
}

// SessionWorkerServicesOptions selects a worker's built-in and plugin facets. Harness and Conversation are the Session's durable Harness and its root conversation. Publish sends a subscription update to its original attachment.
type SessionWorkerServicesOptions struct {
	Harness         AgentHarness
	Conversation    SessionWorkerConversation
	ModelRuntime    ModelsServiceModelRuntime
	SettingsManager ModelsServiceSettingsManager
	FacetLoader     chord.FacetLoader
	Publish         func(context.Context, WorkerServiceScope, string, chord.ServiceProviderUpdate) error
}

type scopedServiceEndpoint struct {
	scope    WorkerServiceScope
	endpoint chord.RemoteServiceEndpoint
}

// SessionWorkerServices owns the worker's facet generations and attachment-scoped endpoints. Reload calls wait in admission order; Dispose drains that work before releasing both facet sets.
type SessionWorkerServices struct {
	host          *chord.FacetHost
	builtins      chord.LoadedFacets
	loadedPlugins chord.LoadedFacets
	pluginLoader  chord.FacetLoader
	reloads       serviceMutationTail
	mu            sync.Mutex
	endpoints     []*scopedServiceEndpoint
	publish       func(context.Context, WorkerServiceScope, string, chord.ServiceProviderUpdate) error
}

type workerSessionPlugins struct {
	reload func(context.Context) error
	tail   func() *serviceMutationTail
}

func (plugins workerSessionPlugins) Reload(ctx context.Context) error { return plugins.reload(ctx) }

// AdmitServiceMember reserves the reload's position in the worker's reload tail during admission.
func (plugins workerSessionPlugins) AdmitServiceMember(ctx context.Context, member string) (context.Context, func()) {
	tail := plugins.tail()
	if member != "reload" || tail == nil {
		return ctx, func() {}
	}
	ticket := tail.reserve()
	return context.WithValue(ctx, mutationTicketKey{}, ticket), ticket.release
}

// CreateSessionWorkerServices activates controller, plugin-reload, Models, Transcript, and supplied plugin facets. It returns only after activation finishes; initialization and reload use the background context as upstream's context-free calls do.
func CreateSessionWorkerServices(options SessionWorkerServicesOptions) (*SessionWorkerServices, error) {
	ctx := context.Background()
	services := &SessionWorkerServices{publish: options.Publish}
	reloadPlugins := func(context.Context) error { return errors.New("Session plugins are not ready") }
	controllerFacet := chord.Facet{Id: "@pi/agent-controller-runtime", Setup: func(env *chord.FacetEnvironment) error {
		return chord.ProvideService[AgentController](env, AgentControllerDefinition, CreateAgentController(options.Harness, options.Conversation))
	}}
	pluginFacet := chord.Facet{Id: "@pi/session-plugins-runtime", Setup: func(env *chord.FacetEnvironment) error {
		return chord.ProvideService[SessionPlugins](env, SessionPluginsDefinition, workerSessionPlugins{reload: func(ctx context.Context) error { return reloadPlugins(ctx) }, tail: func() *serviceMutationTail { return &services.reloads }})
	}}
	modelsFacet, err := createWorkerModelsFacet(ctx, options)
	if err != nil {
		return nil, err
	}
	transcriptFacet, err := CreateTranscriptServiceFacet(ctx, options.Conversation)
	if err != nil {
		return nil, err
	}
	builtins, err := chord.CreateStaticFacetLoader([]chord.Facet{
		controllerFacet, pluginFacet,
		modelsFacet,
		transcriptFacet,
	}).Load(ctx)
	if err != nil {
		return nil, err
	}
	loader := options.FacetLoader
	if loader == nil {
		loader = chord.CreateStaticFacetLoader(nil)
	}
	loaded, err := loader.Load(ctx)
	if err != nil {
		return nil, err
	}
	facets := append(append([]chord.Facet{}, builtins.Facets...), loaded.Facets...)
	host, err := chord.CreateFacetHost(ctx, chord.FacetOptions{Facets: facets})
	if err != nil {
		cleanup := disposeWorkerFacetSets(ctx, loaded, builtins)
		if len(cleanup) > 0 {
			return nil, throwFailures(append([]error{err}, cleanup...), "Session facets failed to start and clean up")
		}
		return nil, err
	}
	services.host, services.builtins, services.loadedPlugins, services.pluginLoader = host, builtins, loaded, loader
	reloadPlugins = services.reload
	return services, nil
}

// createWorkerModelsFacet acquires the conversation's agent document, then builds the facet that owns it.
func createWorkerModelsFacet(ctx context.Context, options SessionWorkerServicesOptions) (chord.Facet, error) {
	agent, err := options.Harness.AgentDocument(ctx, options.Conversation.ID())
	if err != nil {
		return chord.Facet{}, err
	}
	if agent == nil {
		return chord.Facet{}, fmt.Errorf("Conversation %d has no agent document", options.Conversation.ID())
	}
	return CreateModelsServiceFacet(ModelsServiceFacetOptions{Conversation: options.Conversation, Agent: agent, ModelRuntime: options.ModelRuntime, SettingsManager: options.SettingsManager}), nil
}

func (services *SessionWorkerServices) reload(admitted context.Context) error {
	return services.reloads.run(admitted, func() error {
		ctx := context.Background()
		candidate, err := services.pluginLoader.Load(ctx)
		if err != nil {
			return err
		}
		if err := services.host.Reload(ctx, candidate.Facets); err != nil {
			if cleanupError := candidate.Dispose(ctx); cleanupError != nil {
				return throwFailures([]error{err, cleanupError}, "Session plugin reload and cleanup failed")
			}
			return err
		}
		retired := services.loadedPlugins
		services.loadedPlugins = candidate
		return retired.Dispose(ctx)
	})
}

// scopedEndpoint returns the endpoint belonging to scope, creating it on first use.
func (services *SessionWorkerServices) scopedEndpoint(scope WorkerServiceScope) *scopedServiceEndpoint {
	services.mu.Lock()
	defer services.mu.Unlock()
	for _, existing := range services.endpoints {
		if existing.scope == scope {
			return existing
		}
	}
	entry := &scopedServiceEndpoint{scope: scope, endpoint: chord.CreateRemoteServiceEndpoint(services.host.Services())}
	services.endpoints = append(services.endpoints, entry)
	return entry
}

// Invoke routes a service call through the endpoint belonging to scope, creating it on first use. It returns the original call's result or error.
func (services *SessionWorkerServices) Invoke(ctx context.Context, call chord.ServiceCall, scope WorkerServiceScope) (json.RawMessage, error) {
	entry := services.scopedEndpoint(scope)
	return entry.endpoint.Invoke(ctx, call, func(ctx context.Context, subscriptionId string, update chord.ServiceProviderUpdate) error {
		return services.publish(ctx, scope, subscriptionId, update)
	})
}

// BeginInvoke admits a service call on the endpoint belonging to scope, like Invoke, and returns once the call holds its place in
// that endpoint's ordering: calls begun in sequence reach their services in that sequence.
func (services *SessionWorkerServices) BeginInvoke(ctx context.Context, call chord.ServiceCall, scope WorkerServiceScope) (*chord.ServiceInvocation, error) {
	entry := services.scopedEndpoint(scope)
	return chord.BeginEndpointInvoke(ctx, entry.endpoint, call, func(ctx context.Context, subscriptionId string, update chord.ServiceProviderUpdate) error {
		return services.publish(ctx, scope, subscriptionId, update)
	})
}

// RemoveSubscriptions closes endpoints whose attachment scopes match, in their creation order.
func (services *SessionWorkerServices) RemoveSubscriptions(matches func(WorkerServiceScope) bool) {
	services.mu.Lock()
	entries := append([]*scopedServiceEndpoint(nil), services.endpoints...)
	services.mu.Unlock()
	for _, entry := range entries {
		if !matches(entry.scope) {
			continue
		}
		entry.endpoint.Dispose()
		services.mu.Lock()
		for i, current := range services.endpoints {
			if current == entry {
				services.endpoints = slices.Delete(services.endpoints, i, i+1)
				break
			}
		}
		services.mu.Unlock()
	}
}

// Dispose removes subscriptions, waits for queued reloads, disposes the host, and releases every loaded set even after an earlier cleanup failure.
func (services *SessionWorkerServices) Dispose() error {
	services.RemoveSubscriptions(func(WorkerServiceScope) bool { return true })
	services.reloads.wait()
	ctx := context.Background()
	var failures []error
	if err := services.host.Dispose(ctx); err != nil {
		failures = append(failures, err)
	}
	failures = append(failures, disposeWorkerFacetSets(ctx, services.loadedPlugins, services.builtins)...)
	return throwFailures(failures, "Failed to dispose Session facets")
}

func disposeWorkerFacetSets(ctx context.Context, plugins, builtins chord.LoadedFacets) []error {
	var failures [2]error
	var group sync.WaitGroup
	group.Go(func() { failures[0] = plugins.Dispose(ctx) })
	group.Go(func() { failures[1] = builtins.Dispose(ctx) })
	group.Wait()
	var result []error
	for _, err := range failures {
		if err != nil {
			result = append(result, err)
		}
	}
	return result
}
