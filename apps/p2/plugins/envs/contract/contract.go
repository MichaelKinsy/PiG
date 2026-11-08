// Package contract ports apps/p2/plugins/envs/contract.ts (and the schemas of schema.ts and sandbox-schema.ts): the
// environment service's configuration (`EnvService`), the SSH manager's registry (`EnvManagers`), the owner's `Envs`
// and `SandboxSecrets` and their schemas.
package contract

import (
	"context"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/env"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
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

// EnvManager is the server's SSH manager (the part other plugins use; the envs plugin's manager implements it).
type EnvManager interface {
	// WorkingDirectory is cwd as it resolves on environment env (`~` expanded), checked to be a directory; with
	// create the directory is made first.
	WorkingDirectory(ctx context.Context, env, cwd string, create bool) (string, error)
	// Connection is a pi-env connection to environment name configured as config (a sandbox: through its host), with
	// p2's keys and known hosts. It starts at its first request; the caller closes it.
	Connection(name string, config *chordjson.Object) *env.Connection
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

// DefaultDiskGB is the disk size of a sandbox without `diskGb`.
const DefaultDiskGB = 32

// SandboxStatusSchema is a sandbox's VM: stopped, or running since `startedAt`; `current` is false while it runs an older
// configuration (changes apply when it restarts).
var SandboxStatusSchema = union(
	obj(schema.Properties{prop("state", p2runtime.Literal("stopped"))}),
	obj(schema.Properties{
		prop("state", p2runtime.Literal("running")), prop("startedAt", num()), prop("cpus", num()),
		prop("memoryMb", num()), prop("current", schema.Bool()),
	}),
)

// EnvUserSchema is a session using an environment (working there, or with a subagent whose own location is there).
var EnvUserSchema = obj(schema.Properties{prop("id", str()), prop("title", str()), prop("main", num()), prop("archived", schema.Bool())})

// EnvSummarySchema is an environment as the owner lists it.
var EnvSummarySchema = obj(schema.Properties{
	prop("name", str()), prop("config", EnvConfigSchema), prop("sessions", num()),
	opt("users", arr(EnvUserSchema)), opt("test", EnvTestSchema),
})

// DirectoryListingSchema is the folders of a directory.
var DirectoryListingSchema = p2runtime.DirectoryListingSchema

// envConfigSchemaV2 is the environment configuration of versions 1 and 2 of `Envs`: sandboxes without `diskGb`.
var envConfigSchemaV2 = union(SSHEnvConfigSchema, obj(schema.Properties{
	prop("type", p2runtime.Literal("gondolin")),
	prop("host", str(schema.MinLength(1), schema.MaxLength(32))),
	opt("image", str(schema.MinLength(1), schema.MaxLength(255))),
	opt("mounts", schema.Arr(SandboxMountSchema, schema.MaxItems(16))),
	opt("allowHosts", schema.Arr(str(schema.MinLength(1), schema.MaxLength(255)), schema.MaxItems(256))),
	opt("cpus", schema.Int(schema.Minimum(1), schema.Maximum(64))),
	opt("memoryMb", schema.Int(schema.Minimum(256), schema.Maximum(262144))),
}))

var envSummarySchemaV1 = obj(schema.Properties{
	prop("name", str()), prop("config", envConfigSchemaV2), prop("sessions", num()), opt("test", EnvTestSchema),
})

var envSummarySchemaV2 = obj(schema.Properties{
	prop("name", str()), prop("config", envConfigSchemaV2), prop("sessions", num()),
	opt("users", arr(EnvUserSchema)), opt("test", EnvTestSchema),
})

var (
	nameArgs = obj(schema.Properties{prop("name", str())})
	keyReply = obj(schema.Properties{prop("publicKey", str()), prop("identityFile", str())})
)

func meth(args, result schema.Schema) *contracts.Method {
	return contracts.NewMethod(c(args), c(result))
}

var EnvsV1 = mustContract(contracts.Remote("p2.envs", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{},
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{contracts.Var("owner", contracts.VariantDeclaration{
		State: []contracts.Property{
			p2runtime.StateProp("envs", arr(envSummarySchemaV1)),
			p2runtime.StateProp("failure", union(str(), schema.Nul())),
		},
		Methods: []contracts.MethodEntry{
			contracts.Meth("list", contracts.NewMethod(contracts.Schema{}, c(arr(envSummarySchemaV1)))),
			// Starts every environment's SSH connection in the background (the folder picker opened).
			contracts.Meth("warm", contracts.NewMethod()),
			contracts.Meth("save", meth(obj(schema.Properties{prop("name", str()), prop("config", envConfigSchemaV2)}), schema.Nul())),
			contracts.Meth("delete", contracts.NewMethod(c(nameArgs))),
			contracts.Meth("test", meth(nameArgs, EnvTestSchema)),
			contracts.Meth("acceptHostKey", contracts.NewMethod(c(obj(schema.Properties{prop("name", str()), prop("lines", arr(str()))})))),
			contracts.Meth("forgetHostKey", contracts.NewMethod(c(nameArgs))),
			contracts.Meth("generateKey", meth(nameArgs, keyReply)),
			contracts.Meth("uploadKey", meth(obj(schema.Properties{prop("name", str()), prop("key", str(schema.MaxLength(16384)))}), keyReply)),
			contracts.Meth("publicKey", meth(nameArgs, union(str(), schema.Nul()))),
			contracts.Meth("listDirectory", meth(obj(schema.Properties{opt("env", str()), prop("path", str())}), DirectoryListingSchema)),
			// A sandbox's VM state, read from its host.
			contracts.Meth("sandboxStatus", meth(nameArgs, SandboxStatusSchema)),
			// Starts a sandbox's VM (with its current configuration) unless it runs.
			contracts.Meth("sandboxStart", meth(nameArgs, SandboxStatusSchema)),
			// Stops a sandbox's VM; conversations working there reconnect and start it again at their next tool call.
			contracts.Meth("sandboxStop", meth(nameArgs, SandboxStatusSchema)),
		},
	})},
}))

// EnvsV2 is version 2: `EnvSummary.users`, and `delete`'s `force`.
var EnvsV2 = must(contracts.Evolve(EnvsV1, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
	return e.State("owner", "envs", c(arr(envSummarySchemaV2))).
		Result("list", c(arr(envSummarySchemaV2))).
		Args("delete", c(obj(schema.Properties{prop("name", str()), opt("force", schema.Bool())})))
}))

// Envs is the owner's environments and sandboxes (`p2.envs`). 2: `EnvSummary.users` (the sessions using an environment),
// and `delete`'s `force`: refused while sessions use the env unless `force`; their history stays, and their tools refuse
// to run until they move to another location (never on this device instead). 3: a sandbox's `diskGb`, and
// `sandboxResetDisk`: stops the VM and removes its disk, so the next start begins from the guest image again. The
// provider still serves 1 and 2 (its `accepts`).
var Envs = must(contracts.Evolve(EnvsV2, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
	return e.State("owner", "envs", c(arr(EnvSummarySchema))).
		Result("list", c(arr(EnvSummarySchema))).
		Args("save", c(obj(schema.Properties{prop("name", str()), prop("config", EnvConfigSchema)}))).
		AddMethod("owner", "sandboxResetDisk", c(nameArgs), c(SandboxStatusSchema))
}))

func must(contract *contracts.Contract, err error) *contracts.Contract {
	return mustContract(contract, err)
}

// EnvironmentPath is the browser-safe display of a directory: the machine is part of a directory's identity.
func EnvironmentPath(envName *string, cwd string) string {
	if envName != nil && *envName != "" {
		return *envName + ":" + cwd
	}
	return cwd
}

// SecretInfoSchema is a sandbox secret as the owner sees it: its name, hosts, preset and when its value was set; never
// the value.
var SecretInfoSchema = obj(schema.Properties{prop("name", str()), prop("hosts", arr(str())), opt("preset", str()), prop("setAt", num())})

// SandboxSecrets is the owner's sandbox secrets (`p2.sandbox-secrets`): values the sandbox's HTTP proxy injects into
// outbound request headers for their hosts, while the VM only sees placeholders. Changes apply when the sandbox's VM
// starts next.
var SandboxSecrets = mustContract(contracts.Remote("p2.sandbox-secrets", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{},
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{contracts.Var("owner", contracts.VariantDeclaration{
		Methods: []contracts.MethodEntry{
			contracts.Meth("list", meth(nameArgs, arr(SecretInfoSchema))),
			// `value` absent: keep the stored value; `addHosts`: allow the hosts the sandbox does not allow yet.
			contracts.Meth("set", contracts.NewMethod(c(obj(schema.Properties{
				prop("sandbox", str()),
				prop("name", str(schema.MinLength(1), schema.MaxLength(64))),
				opt("value", str(schema.MinLength(1), schema.MaxLength(16384))),
				prop("hosts", schema.Arr(str(schema.MinLength(1), schema.MaxLength(255)), schema.MinItems(1), schema.MaxItems(32))),
				opt("preset", str(schema.MaxLength(32))),
				opt("addHosts", schema.Bool()),
			})))),
			contracts.Meth("delete", contracts.NewMethod(c(obj(schema.Properties{prop("sandbox", str()), prop("name", str())})))),
		},
	})},
}))
