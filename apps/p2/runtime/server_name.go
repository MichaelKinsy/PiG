package runtime

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/src/runtime/server-name.ts (the contracts) and the `ServerName` / `ServerNameSettings` objects of
// serverCore in apps/p2/src/runtime/server.ts: what this p2 calls itself, the device's name it falls back to, and a rename.

// MaxServerNameLength is the longest server name, in characters, as Radius stores it (`MAX_SERVER_NAME_LENGTH`): a DNS
// label (63), so a host name is never cut. Linux 64, Windows 15, Kubernetes and cloud VMs 63; only a macOS computer name
// can be longer.
const MaxServerNameLength = 63

// serverNameSchema is a name, or null.
var serverNameSchema = schema.Union([]schema.Schema{schema.Str(), schema.Nul()})

// serverNameParams is a rename's argument: the new name, or null.
var serverNameParams = schema.Obj(schema.Properties{
	schema.Prop("name", schema.Union([]schema.Schema{schema.Str(schema.MaxLength(1024)), schema.Nul()})),
})

// ServerNameSettings is the server's name as the owner sees and changes it (owner only): `name` is what the server calls
// itself (`chosen`, else `device`; null when neither has one), `chosen` the name the owner gave it (null: none, the device's
// name stands), `device` the device's name as of the server's start (null when the device has none). `rename` names the
// server (normalized to one line of at most MaxServerNameLength characters), or, for null or a name that normalizes to
// nothing, back to the device's name; it answers once saved.
var ServerNameSettings = mustContract(contracts.Remote("p2.server-name-settings", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{},
	HostAddress: ServerHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("owner", contracts.VariantDeclaration{
			State: []contracts.Property{
				StateProp("name", serverNameSchema),
				StateProp("chosen", serverNameSchema),
				StateProp("device", serverNameSchema),
			},
			Methods: []contracts.MethodEntry{contracts.Meth("rename", contracts.NewMethod(Contract(serverNameParams)))},
		}),
	},
}))

// ServerName is what the server calls itself (`p2.server-name`): the owner's name for it, else the device's. Plugins that
// show the server elsewhere follow `name` (Radius keeps a copy for its shell); the owner's Settings change it through
// ServerNameSettings.
var ServerName = mustContract(contracts.Local("p2.server-name", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		// `chosen`, else `device`; null when neither has one.
		contracts.Mem("name", Contract(serverNameSchema)),
		// The owner's name for the server; null: none, the device's name stands.
		contracts.Mem("chosen", Contract(serverNameSchema)),
		// The device's name as of the server's start; null when the device has none.
		contracts.Mem("device", Contract(serverNameSchema)),
		// Names the server `name` (normalized), or back to the device's name for null or nothing left.
		contracts.Mem("rename", contracts.NewMethod(Contract(serverNameParams))),
	},
}))

// nameJSON is a name as a state value: the text, or null.
func nameJSON(name string, ok bool) delta.JsonValue {
	if !ok {
		return nil
	}
	return name
}

// serveServerName serves ServerName and ServerNameSettings on the core facet.
func serveServerName(facet host.FacetHost, info ServerInfo) error {
	if err := facet.Serve(ServerName, host.LocalFactory(func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
		chosen := self.Writable(delta.JsonValue(nil))
		device := self.Writable(delta.JsonValue(nil))
		self.Open(
			func(context.Context) (any, error) {
				if err := device.Set(nameJSON(DeviceName())); err != nil {
					return nil, err
				}
				name, ok, err := ReadChosenServerName(info.DataDir)
				if err != nil {
					// A label must never keep the server from starting: the device's name stands until a rename replaces the file.
					ProcessLogs.Notice("server", "identity.json is not usable ("+err.Error()+"); the server goes by the device's name")
					return nil, nil
				}
				return nil, chosen.Set(nameJSON(name, ok))
			},
			func(any, host.NativeCloseReason, context.Context) error { return nil },
		)
		// Renames save one at a time, so the name shown is always the one saved last.
		var saving sync.Mutex
		return contracts.Implementation{Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{
				"name": self.Derive([]any{chosen, device}, func(values []any) (delta.JsonValue, error) {
					if values[0] != nil {
						return values[0], nil
					}
					return values[1], nil
				}),
				"chosen": self.Derive([]any{chosen}, func(values []any) (delta.JsonValue, error) { return values[0], nil }),
				"device": self.Derive([]any{device}, func(values []any) (delta.JsonValue, error) { return values[0], nil }),
			},
			Methods: map[string]contracts.MethodFunction{
				"rename": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					var next *string
					if object, _ := args.(*delta.JsonObject); object != nil {
						if text, ok := object.Value("name").(string); ok {
							if name, ok := NormalizeServerName(text); ok {
								next = &name
							}
						}
					}
					saving.Lock()
					defer saving.Unlock()
					if err := WriteChosenServerName(info.DataDir, next); err != nil {
						return nil, err
					}
					if next == nil {
						return nil, chosen.Set(nil)
					}
					return nil, chosen.Set(*next)
				},
			},
		}}, nil
	}), host.ServeOptions{}); err != nil {
		return err
	}
	return facet.Serve(ServerNameSettings, host.RemoteFactory(func(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		who, err := FromChord(principal)
		if err != nil || !IsOwner(who) {
			return contracts.Implementation{}, Forbidden("the server's name is the owner's")
		}
		source := self.Get(ServerName, delta.NewJsonObject(0))
		follow := func(member string) chord.Watchable[delta.JsonValue] {
			return self.Derive([]any{source.Member(member)}, func(values []any) (delta.JsonValue, error) { return values[0], nil })
		}
		return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"name": follow("name"), "chosen": follow("chosen"), "device": follow("device")},
			Methods: map[string]contracts.MethodFunction{
				"rename": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return source.Call("rename", args, ctx)
				},
			},
		}}, nil
	}), host.ServeOptions{})
}
