package experimental

// Ports packages/coding-agent/src/experimental/client-runtime.ts (built-in service activation).

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

// ActivateBuiltinClientServices acquires the actual remote service facades and waits for both namespaces to hydrate. As upstream's context-free activation does, initial readiness uses the background context; subsequent operations retain their caller context.
func ActivateBuiltinClientServices(_ context.Context, server *ClientRuntimeServer) (*ActivatedClientRuntimeServer, error) {
	serverServices, err := server.Server.Open(chord.RemoteServiceSourceOpenOptions{Services: []chord.ServiceReference{services.SessionDirectoryDefinition, services.SessionManagementDefinition, services.PresentationPluginsDefinition}})
	if err != nil {
		return nil, err
	}
	sessionServices, err := server.Session.Open(chord.RemoteServiceSourceOpenOptions{Services: []chord.ServiceReference{services.ModelsDefinition, services.AgentControllerDefinition, services.TranscriptDefinition}})
	if err != nil {
		return nil, err
	}
	directory, err := chord.UseRemoteClient(serverServices, services.SessionDirectoryDefinition)
	if err != nil {
		return nil, err
	}
	management, err := chord.UseRemoteClient(serverServices, services.SessionManagementDefinition)
	if err != nil {
		return nil, err
	}
	plugins, err := chord.UseRemoteClient(serverServices, services.PresentationPluginsDefinition)
	if err != nil {
		return nil, err
	}
	models, err := chord.UseRemoteClient(sessionServices, services.ModelsDefinition)
	if err != nil {
		return nil, err
	}
	agent, err := chord.UseRemoteClient(sessionServices, services.AgentControllerDefinition)
	if err != nil {
		return nil, err
	}
	transcript, err := chord.UseRemoteClient(sessionServices, services.TranscriptDefinition)
	if err != nil {
		return nil, err
	}
	// Promise.all rejects with the first failure. Its other member cannot be abandoned in Go, so the first failure cancels and joins it.
	waiting, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	var group sync.WaitGroup
	var first sync.Once
	var failure error
	for _, binding := range []chord.RemoteServices{serverServices, sessionServices} {
		group.Go(func() {
			if err := binding.Ready(waiting); err != nil {
				first.Do(func() {
					failure = err
					cancel(err)
				})
			}
		})
	}
	group.Wait()
	if failure != nil {
		return nil, failure
	}
	return &ActivatedClientRuntimeServer{ClientRuntimeServer: server, Directory: directory, Management: runtimeSessionManagement{server: server, remote: management}, Plugins: plugins, Models: models, Agent: agent, Transcript: transcript}, nil
}

type runtimeSessionManagement struct {
	server *ClientRuntimeServer
	remote services.SessionManagement
}

func (management runtimeSessionManagement) Create(ctx context.Context, options services.SessionCreateOptions) (services.SessionSummary, error) {
	return management.remote.Create(ctx, options)
}
func (management runtimeSessionManagement) Remove(ctx context.Context, id string) error {
	attachment := management.server.Client.Attachment()
	removesCurrent := attachment != nil && attachment.SessionId == id
	if err := management.remote.Remove(ctx, id); err != nil {
		return err
	}
	if removesCurrent {
		return management.server.Session.WhenDetached(ctx)
	}
	return nil
}
func (management runtimeSessionManagement) Attach(ctx context.Context, id string) error {
	if err := management.remote.Attach(ctx, id); err != nil {
		return err
	}
	return management.server.Session.WhenAttached(ctx, id)
}
func (management runtimeSessionManagement) Detach(ctx context.Context) error {
	if err := management.remote.Detach(ctx); err != nil {
		return err
	}
	return management.server.Session.WhenDetached(ctx)
}
