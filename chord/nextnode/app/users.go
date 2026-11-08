package app

import (
	"context"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/src/node/app/users.ts
//
// The users plugin (slice 13 §3, decision "The app kit, point 2: principals and users"): accounts keyed by the user's id with a
// profile the app describes (a schema it passes in: a Slack-like profile or just a name), kept in the server's store. User data
// never rides in the principal: a principal names the user, `Users` holds the rest. An exported facet, not a plugin directory: it
// takes the app's schema as an argument (settings are arguments).
//
// Go forced differences (D456): a plugin directory is TypeScript the Node plugin host evaluates, and cannot import this Go
// facet; the Go app loads it as a core facet of the server (AppOptions.Facets), where Pi's app re-exports it through its API and a
// one-line plugin loads it. The generics of `defineUsers<T, P>` are designed out: a profile is a JSON value.

// DefineUsers is the users plugin for profiles of schema profile: its contract and the facet serving it on the server.
func DefineUsers(profile contracts.Schema) (users *contracts.Contract, facet host.Facet, err error) {
	users, err = contracts.Local("chord.app.users", contracts.LocalDeclaration{
		Version: 1,
		Params:  contracts.Params{},
		Members: []contracts.LocalEntry{
			// Every user: id and profile.
			contracts.Mem("list", contracts.Arr(contracts.Obj(contracts.Prop("id", contracts.Str()), contracts.Prop("profile", profile)))),
			// The profile of user `id`, or `null`.
			contracts.Mem("get", contracts.NewMethod(contracts.Str(), contracts.Union([]contracts.Schema{profile, contracts.Nul()}))),
			// Sets the profile of user `id` (a new user: an account made).
			contracts.Mem("update", contracts.NewMethod(contracts.Obj(contracts.Prop("id", contracts.Str()), contracts.Prop("profile", profile)))),
			// Removes user `id`'s account.
			contracts.Mem("remove", contracts.NewMethod(contracts.Str())),
		},
	})
	if err != nil {
		return nil, host.Facet{}, err
	}
	accounts, err := documents.DefineState("chord.app.users", documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("users", contracts.Map(profile))),
		Version: 1,
		Initial: func() *delta.JsonObject { return delta.JsonObjectOf("users", delta.NewJsonObject(0)) },
		Persist: true,
		CheckpointWhen: func(_ *delta.JsonObject, _ []delta.Op, info documents.CheckpointInfo) bool {
			return info.DeltasSinceBase >= 50
		},
	})
	if err != nil {
		return nil, host.Facet{}, err
	}
	facet = host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		return h.Serve(users, func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
			document := self.State(accounts).(*documents.Singleton)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"list": self.Derive([]any{document}, func(values []any) (delta.JsonValue, error) {
						all := values[0].(*delta.JsonObject).Value("users").(*delta.JsonObject)
						list := make([]any, 0, all.Len())
						for id, each := range all.All() {
							list = append(list, delta.JsonObjectOf("id", id, "profile", each))
						}
						return list, nil
					}),
				},
				Methods: map[string]contracts.MethodFunction{
					// A trust boundary: `id` is the caller's (an own key only: no inherited member is a user).
					"get": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
						value, err := document.Value()
						if err != nil {
							return nil, err
						}
						all := value.Value("users").(*delta.JsonObject)
						if id := args.(string); all.Has(id) {
							return all.Value(id), nil
						}
						return nil, nil
					},
					"update": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						arguments := args.(*delta.JsonObject)
						id := arguments.Value("id").(string)
						// As a key it would set the record's prototype, not add a user.
						if id == "__proto__" {
							return nil, data.Invalid(data.ReasonArgs, "users: __proto__ is no user id")
						}
						_, err := document.Change(ctx, func(draft *delta.Object) (any, error) {
							return nil, draft.Object("users").Set(id, arguments.Value("profile"))
						})
						return nil, err
					},
					"remove": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						_, err := document.Change(ctx, func(draft *delta.Object) (any, error) {
							draft.Object("users").Delete(args.(string))
							return nil, nil
						})
						return nil, err
					},
				},
			}}, nil
		}, host.ServeOptions{})
	})
	return users, facet, nil
}
