// Package contract ports the server-side part of apps/p2/plugins/envs/contract.ts that other plugins read: the
// environment service's configuration (`EnvService`) and the SSH manager's registry (`EnvManagers`). The remaining
// contracts of envs (Envs, its UI schemas) belong to the envs plugin's own port.
package contract

import (
	"context"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/schema"
)

var (
	str  = schema.Str
	num  = schema.Num
	obj  = func(properties schema.Properties) schema.Schema { return schema.Obj(properties) }
	prop = schema.Prop
	opt  = schema.Opt
	arr  = func(item schema.Schema) schema.Schema { return schema.Arr(item) }
	c    = p2runtime.Contract
)

func union(branches ...schema.Schema) schema.Schema { return schema.Union(branches) }

// EnvNamePattern is the shape of an environment's name (`ENV_NAME`).
const EnvNamePattern = "^[a-z0-9][a-z0-9_-]{0,31}$"

// SSHEnvConfigSchema is a machine reached over SSH.
var SSHEnvConfigSchema = obj(schema.Properties{
	prop("type", p2runtime.Literal("ssh")),
	prop("host", str(schema.MinLength(1), schema.MaxLength(255))),
	opt("user", str(schema.MinLength(1), schema.MaxLength(255))),
	opt("port", schema.Int(schema.Minimum(1), schema.Maximum(65535))),
	opt("identityFile", str(schema.MinLength(1), schema.MaxLength(4096))),
	opt("shellPath", str(schema.MinLength(1), schema.MaxLength(4096))),
	opt("loginShell", schema.Bool()),
	opt("shellEnv", p2runtime.Record(str())),
})

// SandboxMountSchema is a folder of the host shared with a sandbox at `guest`.
var SandboxMountSchema = obj(schema.Properties{
	prop("host", str(schema.MinLength(1), schema.MaxLength(4096))),
	prop("guest", str(schema.MinLength(2), schema.MaxLength(4096))),
	opt("readonly", schema.Bool()),
})

// GondolinEnvConfigSchema is a Gondolin sandbox: a VM on the SSH environment `host`.
var GondolinEnvConfigSchema = obj(schema.Properties{
	prop("type", p2runtime.Literal("gondolin")),
	prop("host", str(schema.MinLength(1), schema.MaxLength(32))),
	opt("image", str(schema.MinLength(1), schema.MaxLength(255))),
	opt("mounts", schema.Arr(SandboxMountSchema, schema.MaxItems(16))),
	opt("allowHosts", schema.Arr(str(schema.MinLength(1), schema.MaxLength(255)), schema.MaxItems(256))),
	opt("cpus", schema.Int(schema.Minimum(1), schema.Maximum(64))),
	opt("memoryMb", schema.Int(schema.Minimum(256), schema.Maximum(262144))),
	opt("diskGb", schema.Int(schema.Minimum(1), schema.Maximum(4096))),
})

// EnvConfigSchema is an environment's configuration.
var EnvConfigSchema = union(SSHEnvConfigSchema, GondolinEnvConfigSchema)

// SandboxSupportSchema is whether an SSH env can host sandboxes.
var SandboxSupportSchema = obj(schema.Properties{
	prop("runtime", union(p2runtime.Literal("native"), p2runtime.Literal("docker"), p2runtime.Literal("none"))),
	prop("detail", str()),
})

// EnvTestSchema is the outcome of testing an environment.
var EnvTestSchema = union(
	obj(schema.Properties{
		prop("ok", p2runtime.Literal(true)),
		prop("testedAt", num()),
		prop("platform", str()),
		prop("arch", str()),
		prop("home", str()),
		prop("version", str()),
		prop("latencyMs", num()),
		prop("watchMode", union(p2runtime.Literal("native"), p2runtime.Literal("polling"))),
		prop("warnings", arr(str())),
		opt("sandboxes", SandboxSupportSchema),
	}),
	obj(schema.Properties{
		prop("ok", p2runtime.Literal(false)),
		prop("testedAt", num()),
		prop("error", str()),
		opt("hostKey", obj(schema.Properties{
			prop("state", union(p2runtime.Literal("unknown"), p2runtime.Literal("changed"))),
			prop("fingerprints", arr(str())),
			prop("lines", arr(str())),
		})),
	}),
)

// EnvService is the server-only independently observed configuration (`p2.env-service`, version 2). No key material is
// in state. The SSH manager is `EnvManagers`.
var EnvService = mustContract(contracts.Local("p2.env-service", contracts.LocalDeclaration{
	Version: 2,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("configs", c(p2runtime.Record(EnvConfigSchema))),
		contracts.Mem("tests", c(p2runtime.Record(obj(schema.Properties{prop("revision", str()), prop("test", EnvTestSchema)})))),
		contracts.Mem("test", contracts.NewMethod(c(obj(schema.Properties{prop("name", str())})), c(EnvTestSchema))),
		contracts.Mem("failure", c(union(str(), schema.Nul()))),
		contracts.Mem("reload", contracts.NewMethod()),
	},
}))

func mustContract(contract *contracts.Contract, err error) *contracts.Contract {
	if err != nil {
		panic(err)
	}
	return contract
}

// EnvManager is the part of the server's SSH manager that other plugins use. The envs plugin's manager implements the
// rest.
type EnvManager interface {
	// WorkingDirectory is cwd as it resolves on environment env (`~` expanded), checked to be a directory; with
	// create the directory is made first.
	WorkingDirectory(ctx context.Context, env, cwd string, create bool) (string, error)
}

// EnvManagerParts is what `EnvService` contributes to EnvManagers once open.
type EnvManagerParts struct {
	Manager EnvManager
}

// EnvManagers is the registry of the server's SSH manager (`p2.env-manager`): its value is `EnvService`'s
// contribution, nil while it is not open. A factory that needs it declares `EnvService` too, so it waits for it.
var EnvManagers = p2runtime.NewNativeRegistry("p2.env-manager")

// ServeEnvManagers serves the registry (the envs plugin's facet; test hosts).
func ServeEnvManagers(facet host.FacetHost) error {
	return p2runtime.ServeNative[EnvManagerParts](facet, EnvManagers)
}

// EnvManagersOf is the SSH manager's parts for a factory, read at each use: it declares the registry.
func EnvManagersOf(self host.Self) func() (*EnvManagerParts, error) {
	return p2runtime.NativeOf[EnvManagerParts](self.Registry(EnvManagers), "The SSH manager")
}
