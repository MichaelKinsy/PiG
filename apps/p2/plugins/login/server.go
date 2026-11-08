package login

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/login/contract"
	modelscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports apps/p2/plugins/login/server.ts: the server's `LoginService` (the Logins of the server, host-internal) and
// `ProviderLogin`, its view per owner.

func objectOf(value any) *delta.JsonObject {
	object, _ := value.(*delta.JsonObject)
	return object
}

func text(args delta.JsonValue, key string) string {
	s, _ := objectOf(args).Value(key).(string)
	return s
}

// withOwner is args with `owner` (and `remote`, when given) added.
func withOwner(args delta.JsonValue, owner string, remote *bool) *delta.JsonObject {
	out := objectOf(args).Clone()
	out.Set("owner", owner)
	if remote != nil {
		out.Set("remote", *remote)
	}
	return out
}

func serveLoginService(holders state.SourceHandle) host.LocalFactory {
	return func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
		// Declared, so the configuration is open before the logins are.
		self.Get(modelscontract.ModelConfig, delta.NewJsonObject(0))
		native := p2runtime.NativeOf[modelscontract.ModelResourcesValue](self.Registry(modelscontract.ModelResources), "The model configuration")
		resources := func() (modelscontract.Resources, error) {
			value, err := native()
			if err != nil {
				return nil, err
			}
			return value.Resources(), nil
		}
		opened := self.Open(
			func(context.Context) (any, error) {
				current, err := resources()
				if err != nil {
					return nil, err
				}
				return NewLogins(current.Directory()), nil
			},
			func(value any, _ host.NativeCloseReason, _ context.Context) error {
				value.(*Logins).Close()
				return nil
			},
		)
		logins := func() *Logins {
			value, err := opened.Value()
			if err != nil {
				panic(err)
			}
			return value.(*Logins)
		}
		// open is the logins unless they are closed (the object is going away).
		open := func() *Logins {
			value, err := opened.Value()
			if err != nil {
				return nil
			}
			return value.(*Logins)
		}
		// The flow's state as the logins hold it: each method's change is made before it returns.
		state := p2runtime.FollowNative(self,
			func(changed func()) func() { return logins().Subscribe(changed) },
			func() (delta.JsonValue, error) {
				login, owner := logins().State()
				return delta.JsonObjectOf("login", login, "owner", owner), nil
			},
		)
		member := func(key string) chord.Watchable[delta.JsonValue] {
			return self.Derive([]any{state}, func(values []any) (delta.JsonValue, error) {
				return objectOf(values[0]).Value(key), nil
			})
		}
		// An owner leaving the Providers sheet everywhere (no reference of theirs left: closed sheets, disconnects) ends
		// their unfinished login; another owner's sheet coming and going does not.
		abandoned := self.Derive([]any{holders, state}, func(values []any) (delta.JsonValue, error) {
			current := objectOf(values[1])
			login := objectOf(current.Value("login"))
			owner, _ := current.Value("owner").(string)
			if login == nil || login.Value("status") != "working" || owner == "" {
				return nil, nil
			}
			references, _ := values[0].([]any)
			for _, reference := range references {
				principal, _ := objectOf(reference).Value("principal").(data.Principal)
				if principal == nil {
					continue
				}
				if who, err := p2runtime.FromChord(principal); err == nil && p2runtime.Key(who) == owner {
					return nil, nil
				}
			}
			return owner, nil
		})
		self.On(abandoned, func(call chord.WatchCall[delta.JsonValue], _ host.Ctx) *host.Handled {
			if owner, _ := call.Value.(string); owner != "" {
				if current := open(); current != nil {
					current.Disconnect(owner)
				}
			}
			return nil
		})
		return contracts.Implementation{Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"login": member("login"), "owner": member("owner")},
			Methods: map[string]contracts.MethodFunction{
				"start": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					current, err := resources()
					if err != nil {
						return nil, err
					}
					chordctx.Suspend(ctx) // the state's publications are serialized off the loop (Logins.publish)
					remote, _ := objectOf(args).Value("remote").(bool)
					id, err := logins().Start(current, text(args, "owner"), text(args, "providerId"), ai.AuthType(text(args, "authType")), remote)
					if err != nil {
						return nil, err
					}
					return delta.JsonObjectOf("attemptId", id), nil
				},
				"answer": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					chordctx.Suspend(ctx)
					return nil, logins().Answer(text(args, "owner"), text(args, "attemptId"), text(args, "promptId"), text(args, "value"))
				},
				"cancel": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					chordctx.Suspend(ctx)
					return nil, logins().Cancel(text(args, "owner"), text(args, "attemptId"))
				},
				"dismiss": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					chordctx.Suspend(ctx)
					logins().Dismiss(text(args, "owner"), text(args, "attemptId"))
					return nil, nil
				},
				"logout": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					// The registry is read on the loop, then the loop is given up for the work.
					current, err := resources()
					if err != nil {
						return nil, err
					}
					chordctx.Suspend(ctx)
					return nil, logins().Logout(ctx, current, text(args, "providerId"))
				},
				"refresh": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					current, err := resources()
					if err != nil {
						return nil, err
					}
					chordctx.Suspend(ctx)
					count, err := logins().Refresh(ctx, current, text(args, "providerId"))
					if err != nil {
						return nil, err
					}
					return delta.JsonObjectOf("models", float64(count)), nil
				},
			},
		}}, nil
	}
}

// providersOf is the state's `providers`: the providers with a login method and how their stored credential signs in.
func providersOf(snapshot *delta.JsonObject) delta.JsonValue {
	signedIn := map[string]any{}
	credentials, _ := snapshot.Value("credentials").([]any)
	for _, each := range credentials {
		credential := objectOf(each)
		id, _ := credential.Value("providerId").(string)
		signedIn[id] = credential.Value("type")
	}
	out := []any{}
	providers, _ := snapshot.Value("providers").([]any)
	for _, each := range providers {
		provider := objectOf(each)
		methods, _ := provider.Value("loginMethods").([]any)
		if len(methods) == 0 {
			continue
		}
		id, _ := provider.Value("id").(string)
		out = append(out, delta.JsonObjectOf("id", id, "name", provider.Value("name"), "loginMethods", methods, "signedIn", signedIn[id]))
	}
	return out
}

// Server is the login plugin's server facet.
var Server = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	if err := facet.Serve(contract.LoginService, host.LocalFactory(serveLoginService(facet.References(contract.ProviderLogin))), host.ServeOptions{}); err != nil {
		return err
	}
	return facet.Serve(contract.ProviderLogin, host.RemoteFactory(func(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		who, err := p2runtime.FromChord(principal)
		if err != nil || !p2runtime.IsOwner(who) {
			return contracts.Implementation{}, p2runtime.Forbidden("Provider login is only available to owners")
		}
		config := self.Get(modelscontract.ModelConfig, delta.NewJsonObject(0))
		service := self.Get(contract.LoginService, delta.NewJsonObject(0))
		// This owner's logins: one device's (its cookie), the Radius owner's, or the device owner's (every local tab).
		owner := p2runtime.Key(who)
		remote := who.(p2runtime.OwnerPrincipal).Access != p2runtime.AccessLocal
		// Prompts and notices (an auth URL, a device code) only for the owner that started the login.
		login := self.Derive([]any{service.Member("login"), service.Member("owner")}, func(values []any) (delta.JsonValue, error) {
			if started, _ := values[1].(string); started == owner {
				return values[0], nil
			}
			return nil, nil
		})
		providers := self.Derive([]any{config.Member("snapshot")}, func(values []any) (delta.JsonValue, error) {
			return providersOf(objectOf(values[0])), nil
		})
		forward := func(method string, remote *bool) contracts.MethodFunction {
			return func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return service.Call(method, withOwner(args, owner, remote), ctx)
			}
		}
		passthrough := func(method string) contracts.MethodFunction {
			return func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return service.Call(method, args, ctx)
			}
		}
		return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"providers": providers, "login": login},
			Methods: map[string]contracts.MethodFunction{
				"start":   forward("start", &remote),
				"answer":  forward("answer", nil),
				"cancel":  forward("cancel", nil),
				"dismiss": forward("dismiss", nil),
				"logout":  passthrough("logout"),
				"refresh": passthrough("refresh"),
			},
		}}, nil
	}), host.ServeOptions{})
})
