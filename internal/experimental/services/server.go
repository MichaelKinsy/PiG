package services

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
	"github.com/MichaelKinsy/PiG/internal/chord"
)

// PreparedSessionPlugins contains the resolved package selection and its presentation artifacts.
type PreparedSessionPlugins struct {
	PackagePaths        []string
	PresentationPlugins pico3.JsonValue
}

// ExperimentalServerServicesOptions supplies application-owned Session storage and plugin selection. Nil package paths select defaults; an empty slice selects no packages.
type ExperimentalServerServicesOptions struct {
	List                      func(context.Context) ([]SessionSummary, error)
	Create                    func(context.Context, SessionCreateOptions) (SessionSummary, error)
	Remove                    func(context.Context, string) error
	PrepareSessionPlugins     func(context.Context, string, []string) (PreparedSessionPlugins, error)
	ReloadPresentationPlugins func(context.Context, []string) (pico3.JsonValue, error)
}

// RoutedServerPresentation supplies the presentation-scoped routing capabilities used by server management.
type RoutedServerPresentation interface {
	AttachSession(context.Context, string) error
	DetachSession(context.Context) error
	PrepareSessionRemoval(context.Context, string) error
}

// ExperimentalServerServices owns a shared directory and one service endpoint per attached presentation. It has no listener or default CLI activation.
type ExperimentalServerServices struct {
	Host *RoutedServerServiceHost
}

// RoutedServerServiceHost serializes all application mutations across its presentation attachments.
type RoutedServerServiceHost struct {
	options     ExperimentalServerServicesOptions
	directory   *chord.MutableReplicatedState[*SessionDirectoryState]
	revision    int
	mutations   serviceMutationTail
	mu          sync.Mutex
	attachments []*RoutedServerServiceAttachment
}

// serviceMutationTail preserves Promise-tail admission order without detached work. A failed call still releases the next operation. Cancellation belongs to the application callback, including when the call waited in the queue.
type serviceMutationTail struct {
	mu   sync.Mutex
	tail chan struct{}
}

func (tail *serviceMutationTail) run(operation func() error) error {
	tail.mu.Lock()
	previous := tail.tail
	done := make(chan struct{})
	tail.tail = done
	tail.mu.Unlock()
	defer close(done)
	if previous != nil {
		<-previous
	}
	return operation()
}

func (tail *serviceMutationTail) wait() {
	tail.mu.Lock()
	pending := tail.tail
	tail.mu.Unlock()
	if pending != nil {
		<-pending
	}
}

// CreateExperimentalServerServices reads the initial directory in the background context. Application operations remain opt-in through the returned host.
func CreateExperimentalServerServices(options ExperimentalServerServicesOptions) (*ExperimentalServerServices, error) {
	sessions, err := options.List(context.Background())
	if err != nil {
		return nil, err
	}
	directory, err := chord.NewReplicatedState(&SessionDirectoryState{Revision: 1, Sessions: sessions})
	if err != nil {
		return nil, err
	}
	return &ExperimentalServerServices{Host: &RoutedServerServiceHost{options: options, directory: directory, revision: 1}}, nil
}

func (host *RoutedServerServiceHost) refreshNow(ctx context.Context) error {
	sessions, err := host.options.List(ctx)
	if err != nil {
		return err
	}
	host.revision++
	return host.directory.Change(ctx, func(draft *SessionDirectoryState) error {
		draft.Revision = host.revision
		draft.Sessions = sessions
		return nil
	})
}

// Refresh queues a directory refresh after preceding mutations.
func (services *ExperimentalServerServices) Refresh(ctx context.Context) error {
	return services.Host.mutations.run(func() error { return services.Host.refreshNow(ctx) })
}

// Dispose releases every current attachment before waiting for queued mutations. In-flight operations retain their application context and are not cancelled.
func (services *ExperimentalServerServices) Dispose() error {
	host := services.Host
	host.mu.Lock()
	attachments := slices.Clone(host.attachments)
	host.mu.Unlock()
	var failures []error
	for _, attachment := range attachments {
		if err := attachment.Release(context.Background()); err != nil {
			failures = append(failures, err)
		}
	}
	host.mu.Lock()
	host.attachments = nil
	host.mu.Unlock()
	host.mutations.wait()
	return throwFailures(failures, "Failed to release server service attachments")
}

type serverSessionDirectory struct {
	state *chord.MutableReplicatedState[*SessionDirectoryState]
}

func (directory serverSessionDirectory) State() pico3.ReplicatedStateOf[*SessionDirectoryState] {
	return directory.state
}

type serverPresentationServices struct {
	host                       *RoutedServerServiceHost
	presentation               RoutedServerPresentation
	preparedPluginPackagePaths []string
	prepared                   bool
}

func (service *serverPresentationServices) PrepareSession(ctx context.Context, request PrepareSessionPluginsRequest) (pico3.JsonValue, error) {
	var result pico3.JsonValue
	err := service.host.mutations.run(func() error {
		selected, err := service.host.options.PrepareSessionPlugins(ctx, request.SessionId, request.PackagePaths)
		if err != nil {
			return err
		}
		service.preparedPluginPackagePaths = selected.PackagePaths
		service.prepared = true
		result = selected.PresentationPlugins
		return nil
	})
	return result, err
}

func (service *serverPresentationServices) Reload(ctx context.Context) (pico3.JsonValue, error) {
	var result pico3.JsonValue
	err := service.host.mutations.run(func() error {
		if !service.prepared {
			return errors.New("No Session plugin selection is prepared")
		}
		var err error
		result, err = service.host.options.ReloadPresentationPlugins(ctx, service.preparedPluginPackagePaths)
		return err
	})
	return result, err
}

func (service *serverPresentationServices) Create(ctx context.Context, options SessionCreateOptions) (SessionSummary, error) {
	var created SessionSummary
	err := service.host.mutations.run(func() error {
		var err error
		created, err = service.host.options.Create(ctx, options)
		if err != nil {
			return err
		}
		return service.host.refreshNow(ctx)
	})
	return created, err
}

func (service *serverPresentationServices) Remove(ctx context.Context, id string) error {
	return service.host.mutations.run(func() error {
		if err := service.presentation.PrepareSessionRemoval(ctx, id); err != nil {
			return err
		}
		if err := service.host.options.Remove(ctx, id); err != nil {
			return err
		}
		return service.host.refreshNow(ctx)
	})
}

func (service *serverPresentationServices) Attach(ctx context.Context, id string) error {
	return service.host.mutations.run(func() error { return service.presentation.AttachSession(ctx, id) })
}

func (service *serverPresentationServices) Detach(ctx context.Context) error {
	return service.host.mutations.run(func() error {
		if err := service.presentation.DetachSession(ctx); err != nil {
			return err
		}
		service.preparedPluginPackagePaths = nil
		service.prepared = false
		return nil
	})
}

// AttachClient creates the three server-scoped services with independent plugin selection and subscriptions. Only the directory state and mutation queue are shared.
func (host *RoutedServerServiceHost) AttachClient(_ context.Context, presentation RoutedServerPresentation) (*RoutedServerServiceAttachment, error) {
	provider, err := chord.NewRemoteServiceProvider(chord.SingletonService(SessionDirectoryDefinition), chord.SingletonService(SessionManagementDefinition), chord.SingletonService(PresentationPluginsDefinition))
	if err != nil {
		return nil, err
	}
	implementation := &serverPresentationServices{host: host, presentation: presentation}
	if err := chord.Provide[SessionDirectory](provider, SessionDirectoryDefinition, serverSessionDirectory{state: host.directory}); err != nil {
		return nil, err
	}
	if err := chord.Provide[PresentationPlugins](provider, PresentationPluginsDefinition, implementation); err != nil {
		return nil, err
	}
	if err := chord.Provide[SessionManagement](provider, SessionManagementDefinition, implementation); err != nil {
		return nil, err
	}
	attachment := &RoutedServerServiceAttachment{endpoint: chord.CreateRemoteServiceEndpoint(provider), provider: provider, host: host}
	host.mu.Lock()
	host.attachments = append(host.attachments, attachment)
	host.mu.Unlock()
	return attachment, nil
}

// RoutedServerServiceAttachment owns the provider and subscriptions for one presentation connection.
type RoutedServerServiceAttachment struct {
	host     *RoutedServerServiceHost
	provider *chord.RemoteServiceProvider
	endpoint chord.RemoteServiceEndpoint
	mu       sync.Mutex
	released bool
}

// InvokeService forwards a service call while the attachment is live. Release rejects new calls but does not abort admitted calls.
func (attachment *RoutedServerServiceAttachment) InvokeService(ctx context.Context, call chord.ServiceCall, publish chord.ServiceUpdatePublisher) (json.RawMessage, error) {
	attachment.mu.Lock()
	released := attachment.released
	attachment.mu.Unlock()
	if released {
		return nil, errors.New("Server service attachment is released")
	}
	return attachment.endpoint.Invoke(ctx, call, publish)
}

// Release immediately removes subscriptions and the provider. It is idempotent and does not wait for application mutations.
func (attachment *RoutedServerServiceAttachment) Release(context.Context) error {
	attachment.mu.Lock()
	if attachment.released {
		attachment.mu.Unlock()
		return nil
	}
	attachment.released = true
	attachment.mu.Unlock()
	attachment.endpoint.Dispose()
	if err := attachment.provider.Dispose(); err != nil {
		return err
	}
	attachment.host.mu.Lock()
	attachment.host.attachments = slices.DeleteFunc(attachment.host.attachments, func(value *RoutedServerServiceAttachment) bool { return value == attachment })
	attachment.host.mu.Unlock()
	return nil
}
