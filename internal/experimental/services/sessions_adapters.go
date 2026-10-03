package services

// Ports packages/coding-agent/src/experimental/services/sessions.ts (typed remote/guarded facades).

import (
	"context"

	"github.com/MichaelKinsy/PiG/internal/chord"
)

func init() {
	chord.RegisterServiceView(SessionDirectoryDefinition, func(resolve func() (SessionDirectory, error)) SessionDirectory {
		return sessionDirectoryView{resolve: resolve}
	})
	chord.RegisterRemoteClient(SessionDirectoryDefinition, func(service *chord.RemoteService) SessionDirectory { return remoteSessionDirectory{service: service} })
	chord.RegisterServiceView(SessionManagementDefinition, func(resolve func() (SessionManagement, error)) SessionManagement {
		return sessionManagementView{resolve: resolve}
	})
	chord.RegisterRemoteClient(SessionManagementDefinition, func(service *chord.RemoteService) SessionManagement { return remoteSessionManagement{service: service} })
}

type sessionDirectoryView struct {
	resolve func() (SessionDirectory, error)
}

func (view sessionDirectoryView) State() chord.ReplicatedStateOf[*SessionDirectoryState] {
	return chord.StateView(func() (chord.ReplicatedStateOf[*SessionDirectoryState], error) {
		service, err := view.resolve()
		if err != nil {
			return nil, err
		}
		return service.State(), nil
	})
}

type remoteSessionDirectory struct{ service *chord.RemoteService }

func (service remoteSessionDirectory) State() chord.ReplicatedStateOf[*SessionDirectoryState] {
	replica, err := service.service.State("state")
	if err != nil {
		panic(err)
	}
	return chord.TypedReplica[*SessionDirectoryState](replica)
}

type sessionManagementView struct {
	resolve func() (SessionManagement, error)
}

func (view sessionManagementView) Create(ctx context.Context, options SessionCreateOptions) (SessionSummary, error) {
	service, err := view.resolve()
	if err != nil {
		return SessionSummary{}, err
	}
	return service.Create(ctx, options)
}
func (view sessionManagementView) Remove(ctx context.Context, id string) error {
	service, err := view.resolve()
	if err != nil {
		return err
	}
	return service.Remove(ctx, id)
}
func (view sessionManagementView) Attach(ctx context.Context, id string) error {
	service, err := view.resolve()
	if err != nil {
		return err
	}
	return service.Attach(ctx, id)
}
func (view sessionManagementView) Detach(ctx context.Context) error {
	service, err := view.resolve()
	if err != nil {
		return err
	}
	return service.Detach(ctx)
}

type remoteSessionManagement struct{ service *chord.RemoteService }

func (service remoteSessionManagement) Create(ctx context.Context, options SessionCreateOptions) (SessionSummary, error) {
	return chord.CallResult[SessionSummary](ctx, service.service, "create", options)
}
func (service remoteSessionManagement) Remove(ctx context.Context, id string) error {
	_, err := service.service.Call(ctx, "remove", id)
	return err
}
func (service remoteSessionManagement) Attach(ctx context.Context, id string) error {
	_, err := service.service.Call(ctx, "attach", id)
	return err
}
func (service remoteSessionManagement) Detach(ctx context.Context) error {
	_, err := service.service.Call(ctx, "detach")
	return err
}
