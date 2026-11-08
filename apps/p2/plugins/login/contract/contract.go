// Package contract is the provider login plugin's contract (apps/p2/web/upstream/plugins/login/contract.ts, `p2.provider-login`):
// signing in to model providers, for owners only. Credentials are stored in auth.json by the models plugin's configuration and never
// appear in state.
package contract

import (
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/schema"
)

var (
	c      = p2runtime.Contract
	str    = schema.Str
	num    = schema.Num
	nul    = schema.Nul
	prop   = schema.Prop
	opt    = schema.Opt
	method = func(sides ...schema.Schema) *contracts.Method {
		contractSides := make([]contracts.Schema, len(sides))
		for i, side := range sides {
			contractSides[i] = c(side)
		}
		return contracts.NewMethod(contractSides...)
	}
)

func obj(properties schema.Properties) schema.Schema { return schema.Obj(properties) }
func arr(item schema.Schema) schema.Schema           { return schema.Arr(item) }
func union(branches ...schema.Schema) schema.Schema  { return schema.Union(branches) }

// AuthTypeSchema is "api_key" or "oauth".
var AuthTypeSchema = union(schema.Lit("api_key"), schema.Lit("oauth"))

// ProviderSchema is a provider with its login methods and how its stored credential signs in (null: none; environment keys may still apply).
var ProviderSchema = obj(schema.Properties{
	prop("id", str()),
	prop("name", str()),
	prop("loginMethods", arr(obj(schema.Properties{prop("type", AuthTypeSchema), prop("label", str())}))),
	prop("signedIn", union(AuthTypeSchema, nul())),
})

// LoginStateSchema is the login in progress or just finished (one at a time): the provider, its status, the question its flow waits
// for (`prompt`: {id, type: text|secret|manual_code|select, message, placeholder?, options?}) and what the flow told so far
// (`notices`: {id, event}, an event being an auth_url, device_code, info or progress record).
var LoginStateSchema = obj(schema.Properties{
	prop("attemptId", str()),
	prop("providerId", str()),
	prop("authType", AuthTypeSchema),
	prop("status", union(schema.Lit("working"), schema.Lit("success"), schema.Lit("cancelled"), schema.Lit("error"))),
	prop("prompt", union(schema.Unknown(), nul())),
	prop("notices", arr(obj(schema.Properties{prop("id", num()), prop("event", schema.Unknown())}))),
	opt("message", str()),
})

var attemptSchema = obj(schema.Properties{prop("attemptId", str())})
var providerIDSchema = obj(schema.Properties{prop("providerId", str())})

// ProviderLogin is signing in to model providers (`p2.provider-login`; server; owners only).
var ProviderLogin = func() *contracts.Contract {
	contract, err := contracts.Remote("p2.provider-login", contracts.RemoteDeclaration{
		Version:     1,
		Params:      contracts.Params{},
		HostAddress: p2runtime.ServerHost,
		Variants: []contracts.VariantEntry{
			contracts.Var("owner", contracts.VariantDeclaration{
				State: []contracts.Property{
					p2runtime.StateProp("providers", arr(ProviderSchema)),
					p2runtime.StateProp("login", union(LoginStateSchema, nul())),
				},
				Methods: []contracts.MethodEntry{
					contracts.Meth("start", method(obj(schema.Properties{prop("providerId", str()), prop("authType", AuthTypeSchema)}), attemptSchema)),
					contracts.Meth("answer", method(obj(schema.Properties{
						prop("attemptId", str()), prop("promptId", str()), prop("value", str(schema.MaxLength(100_000))),
					}))),
					contracts.Meth("cancel", method(attemptSchema)),
					// Clears a finished login.
					contracts.Meth("dismiss", method(attemptSchema)),
					contracts.Meth("logout", method(providerIDSchema)),
					// Fetches the provider's current model list; returns how many models it has.
					contracts.Meth("refresh", method(providerIDSchema, obj(schema.Properties{prop("models", num())}))),
				},
			}),
		},
	})
	if err != nil {
		panic(err)
	}
	return contract
}()
