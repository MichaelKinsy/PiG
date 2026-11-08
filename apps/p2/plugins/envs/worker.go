package envs

import (
	"context"
	"fmt"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports apps/p2/plugins/envs/worker.ts: the worker facet contributes the Harness's `remote` environment provider (the
// connections to the environments of envs.json) and tells it which of them are sandboxes.

// machine is one machine's connection (`p2runtime.Machine`): one for every conversation working on the environment.
type machine struct{ session RemoteSession }

func (m machine) At(cwd string) durableenv.ExecutionEnv { return m.session.At(cwd) }
func (m machine) Close()                                { m.session.Connection.Close() }

var workerFacet = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	return p2runtime.Contribute(facet, "p2.worker-envs", registerWorkerEnvs)
})

func registerWorkerEnvs(self host.StoreSelf) error {
	worker := p2runtime.WorkerOf(self)
	dataDir := func() (string, error) {
		parts, err := worker()
		if err != nil {
			return "", err
		}
		return parts.Init.DataDir, nil
	}
	opened := self.Open(
		func(ctx context.Context) (any, error) {
			directory, err := dataDir()
			if err != nil {
				return nil, err
			}
			store := NewEnvStore(directory)
			if err := store.Start(ctx); err != nil {
				store.Close()
				return nil, err
			}
			return store, nil
		},
		func(value any, _ host.NativeCloseReason, _ context.Context) error {
			value.(*EnvStore).Close()
			return nil
		},
	)
	configOf := func(ctx context.Context, name string) (*chordjson.Object, error) {
		directory, err := dataDir()
		if err != nil {
			return nil, err
		}
		configs, err := LoadEnvs(ctx, directory)
		if err != nil {
			return nil, err
		}
		config, _ := configs.Value(name).(*chordjson.Object)
		return config, nil
	}
	p2runtime.ContributeKeyed(self, p2runtime.HarnessEnvironments, p2runtime.Entry("remote", p2runtime.HarnessEnvironmentEntry{
		Open: func(ctx context.Context, name string) (p2runtime.Machine, error) {
			directory, err := dataDir()
			if err != nil {
				return nil, err
			}
			config, err := configOf(ctx, name)
			if err != nil {
				return nil, err
			}
			if config == nil {
				return nil, fmt.Errorf(`Environment "%s" is not configured; refusing local execution`, name)
			}
			// One connection for every conversation working on this environment, at its directories.
			return machine{session: RemoteSessionFor(directory, name, config, "/", ConnectOptions{})}, nil
		},
		Describe: func(ctx context.Context, name string) (string, bool, error) {
			config, err := configOf(ctx, name)
			if err != nil || config == nil {
				return "", false, err
			}
			switch config.Value("type") {
			case "ssh":
				return fmt.Sprintf(`the remote environment "%s" (SSH host %s)`, name, config.Value("host")), true, nil
			case "gondolin":
				return fmt.Sprintf(`the sandbox "%s" (a VM on the environment "%s"; you cannot leave it, and only its own folders and allowed hosts are reachable)`, name, config.Value("host")), true, nil
			}
			return "", false, nil
		},
		// The observed envs.json: a sandbox stays one while its configuration is valid (the last valid state).
		Sandbox: func(name string) bool {
			value, err := opened.Value()
			if err != nil {
				return false
			}
			return IsSandbox(value.(*EnvStore).Snapshot().Configs, name)
		},
	}))
	return nil
}
