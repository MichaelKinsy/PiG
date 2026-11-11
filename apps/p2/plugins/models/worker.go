package models

import (
	"context"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Ports apps/p2/plugins/models/worker.ts: the worker facet. `WorkerModels` holds the worker's own configuration
// observer, contributes the Harness's model access and settings (key "default"), and publishes `settings.json`.

var workerFacet = p2runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	return facet.Serve(contract.WorkerModels, host.LocalFactory(serveWorkerModels), host.ServeOptions{})
})

func serveWorkerModels(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
	worker := p2runtime.WorkerOf(self)
	opened := self.Open(
		func(ctx context.Context) (any, error) {
			parts, err := worker()
			if err != nil {
				return nil, err
			}
			return OpenResources(ctx, ResourceOptions{Directory: parts.Init.DataDir, Fake: parts.Init.Fake})
		},
		func(value any, _ host.NativeCloseReason, ctx context.Context) error {
			return value.(*Resources).Close(ctx)
		},
	)
	self.(host.StoreSelf).Contribute(p2runtime.HarnessConfig.Registry, p2runtime.Keyed[p2runtime.HarnessConfigEntry]{
		p2runtime.Entry("default", p2runtime.HarnessConfigEntry{
			Models:   func() durable.Models { return resourcesNative(opened).Durable() },
			Settings: func() *harness.HarnessSettings { return resourcesNative(opened).HarnessSettings() },
		}),
	})
	settings := p2runtime.FollowNative(self,
		func(changed func()) func() { return resourcesNative(opened).Subscribe(changed) },
		func() (delta.JsonValue, error) {
			value, err := opened.Value()
			if err != nil {
				return nil, err
			}
			return value.(*Resources).Settings(), nil
		},
	)
	return contracts.Implementation{Members: contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{"settings": settings},
	}}, nil
}
