package envs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/envs/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
)

// Ports apps/p2/plugins/envs/server.ts: the server facet's `EnvService` (the configuration observer, published) and the
// manager `EnvManagers` carries. Gaps (rows/p2-A.md): the SSH manager's connections, `Envs` and `SandboxSecrets`, the
// restart contribution that stops workers of an edited environment, environment tests.

// errRemote is what a named environment answers until the SSH manager is ported.
var errRemote = errors.New("SSH environments are not available in this build")

// manager is the manager's local part: a session's working directory on this machine.
type manager struct{}

// WorkingDirectory is cwd resolved on this machine, optionally created, and checked to be a directory.
func (manager) WorkingDirectory(_ context.Context, env, cwd string, create bool) (string, error) {
	if env != "" {
		return "", errRemote
	}
	resolved := cwd
	if resolved == "~" || strings.HasPrefix(resolved, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		resolved = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(resolved, "~"), "/"))
	}
	if create {
		if err := nodeerrno.MkdirAll(resolved, 0o777); err != nil {
			return "", err
		}
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", nodeerrno.FromPathError(err)
	}
	if !info.IsDir() {
		return "", errors.New("Working directory is not a directory")
	}
	return resolved, nil
}

var serverFacet = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	if err := contract.ServeEnvManagers(facet); err != nil {
		return err
	}
	return facet.Serve(contract.EnvService, host.LocalFactory(serveEnvService), host.ServeOptions{})
})

func serveEnvService(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
	server := self.Get(p2runtime.ServerLocal, delta.NewJsonObject(0))
	opened := self.Open(
		func(ctx context.Context) (any, error) {
			info, err := server.Member("info").Value()
			if err != nil {
				return nil, err
			}
			dataDir, _ := info.(*delta.JsonObject).Value("dataDir").(string)
			store := NewEnvStore(dataDir)
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
	store := func() *EnvStore {
		value, err := opened.Value()
		if err != nil {
			panic(err)
		}
		return value.(*EnvStore)
	}
	self.(host.StoreSelf).Contribute(contract.EnvManagers, &contract.EnvManagerParts{Manager: manager{}})
	// envs.json as the store last read it validly, and why the last read failed: `reload` resolves after the store told
	// this source.
	snapshot := p2runtime.FollowNative(self,
		func(changed func()) func() { return store().Subscribe(func(EnvSnapshot) { changed() }) },
		func() (delta.JsonValue, error) {
			read := store().Snapshot()
			var failure any
			if read.Error != "" {
				failure = read.Error
			}
			return delta.JsonObjectOf("configs", read.Configs, "error", failure), nil
		},
	)
	member := func(key string) chord.Watchable[delta.JsonValue] {
		return self.Derive([]any{snapshot}, func(values []any) (delta.JsonValue, error) {
			return values[0].(*delta.JsonObject).Value(key), nil
		})
	}
	return contracts.Implementation{Members: contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{
			"configs": member("configs"),
			// No environment is tested until the SSH manager is ported.
			"tests":   host.Expose(state.NewConstant(delta.JsonValue(delta.NewJsonObject(0)))),
			"failure": member("error"),
		},
		Methods: map[string]contracts.MethodFunction{
			"test": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, errRemote },
			"reload": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return nil, store().Reload(ctx)
			},
		},
	}}, nil
}
