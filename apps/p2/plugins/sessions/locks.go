package sessions

import (
	"context"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/plugins/sessions/locks.ts: session locks (server): every locked session's picked resources, persisted,
// and the remote view the owner sets them through and each session's worker follows its own lock with.

var locksState = mustState("p2.session-locks", documents.StateOptions{
	Schema:         p2runtime.Contract(schema.Obj(schema.Properties{schema.Prop("locks", p2runtime.Record(p2runtime.SessionLockSchema))})),
	Version:        1,
	Initial:        func() *delta.JsonObject { return delta.JsonObjectOf("locks", delta.NewJsonObject(0)) },
	Persist:        true,
	CheckpointWhen: func(*delta.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return true },
})

func mustState(name string, options documents.StateOptions) *documents.StateDefinition {
	definition, err := documents.DefineState(name, options)
	if err != nil {
		panic(err)
	}
	return definition
}

// objectArgs is a method's arguments as the object they are checked to be.
func objectArgs(args delta.JsonValue) *delta.JsonObject {
	object, _ := args.(*delta.JsonObject)
	if object == nil {
		return delta.NewJsonObject(0)
	}
	return object
}

// serveLocks serves SessionLocks (the persisted state) and SessionLock (the views).
func serveLocks(facet host.FacetHost) error {
	err := facet.Serve(contract.SessionLocks, host.LocalFactory(func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
		doc := self.State(locksState).(*documents.Singleton)
		locks := self.Derive([]any{doc}, func(values []any) (delta.JsonValue, error) {
			return values[0].(*delta.JsonObject).Value("locks"), nil
		})
		return contracts.Implementation{Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"locks": locks},
			Methods: map[string]contracts.MethodFunction{
				"set": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					in := objectArgs(args)
					session, _ := in.Value("session").(string)
					lock := in.Value("lock")
					_, err := doc.Change(ctx, func(draft *delta.Object) (any, error) {
						locks := draft.Object("locks")
						if lock == nil {
							locks.Delete(session)
							return nil, nil
						}
						return nil, locks.Set(session, lock)
					})
					return nil, err
				},
			},
		}}, nil
	}), host.ServeOptions{})
	if err != nil {
		return err
	}
	return facet.Serve(contract.SessionLock, host.RemoteFactory(func(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		who, err := p2runtime.FromChord(principal)
		if err != nil {
			return contracts.Implementation{}, p2runtime.Forbidden("session locks are the owner's")
		}
		locks := self.Get(contract.SessionLocks, delta.NewJsonObject(0))
		switch typed := who.(type) {
		case p2runtime.WorkerPrincipal:
			session := typed.Session
			lock := self.Derive([]any{locks.Member("locks")}, func(values []any) (delta.JsonValue, error) {
				if all, ok := values[0].(*delta.JsonObject); ok {
					if own := all.Value(session); own != nil {
						return own, nil
					}
				}
				return nil, nil
			})
			return contracts.Implementation{Variant: "worker", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"lock": lock},
			}}, nil
		case p2runtime.OwnerPrincipal:
			return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"locks": host.Expose(locks.Member("locks"))},
				Methods: map[string]contracts.MethodFunction{
					"set": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						return locks.Call("set", args, ctx)
					},
				},
			}}, nil
		}
		return contracts.Implementation{}, p2runtime.Forbidden("session locks are the owner's")
	}), host.ServeOptions{})
}
