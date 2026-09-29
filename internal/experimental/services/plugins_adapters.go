// Ports packages/coding-agent/src/experimental/services/plugins.ts.
package services

import (
	"context"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
	"github.com/MichaelKinsy/PiG/internal/chord"
)

func init() {
	chord.RegisterServiceView(SessionPluginsDefinition, func(resolve func() (SessionPlugins, error)) SessionPlugins {
		return sessionPluginsView{resolve: resolve}
	})
	chord.RegisterRemoteClient(SessionPluginsDefinition, func(service *chord.RemoteService) SessionPlugins { return remoteSessionPlugins{service: service} })
	chord.RegisterServiceView(PresentationPluginsDefinition, func(resolve func() (PresentationPlugins, error)) PresentationPlugins {
		return presentationPluginsView{resolve: resolve}
	})
	chord.RegisterRemoteClient(PresentationPluginsDefinition, func(service *chord.RemoteService) PresentationPlugins {
		return remotePresentationPlugins{service: service}
	})
}

type sessionPluginsView struct {
	resolve func() (SessionPlugins, error)
}

func (view sessionPluginsView) Reload(ctx context.Context) error {
	service, err := view.resolve()
	if err != nil {
		return err
	}
	return service.Reload(ctx)
}

type remoteSessionPlugins struct{ service *chord.RemoteService }

func (client remoteSessionPlugins) Reload(ctx context.Context) error {
	_, err := client.service.Call(ctx, "reload")
	return err
}

type presentationPluginsView struct {
	resolve func() (PresentationPlugins, error)
}

func (view presentationPluginsView) PrepareSession(ctx context.Context, request PrepareSessionPluginsRequest) (pico3.JsonValue, error) {
	service, err := view.resolve()
	if err != nil {
		return nil, err
	}
	return service.PrepareSession(ctx, request)
}
func (view presentationPluginsView) Reload(ctx context.Context) (pico3.JsonValue, error) {
	service, err := view.resolve()
	if err != nil {
		return nil, err
	}
	return service.Reload(ctx)
}

type remotePresentationPlugins struct{ service *chord.RemoteService }

func (client remotePresentationPlugins) PrepareSession(ctx context.Context, request PrepareSessionPluginsRequest) (pico3.JsonValue, error) {
	return chord.CallResult[pico3.JsonValue](ctx, client.service, "prepareSession", request)
}
func (client remotePresentationPlugins) Reload(ctx context.Context) (pico3.JsonValue, error) {
	return chord.CallResult[pico3.JsonValue](ctx, client.service, "reload")
}
