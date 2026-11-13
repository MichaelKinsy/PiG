package app

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	chordhttp "github.com/MichaelKinsy/PiG/chord/nextnode/http"
	"github.com/MichaelKinsy/PiG/internal/jsclock"
)

// Ports packages/chord/src/node/app/sign-ins.ts (Pi ff7414d51: `sessions` until then)
//
// SignIns (slice 13 §2, decision "The app kit, point 3: authentication"): the kit's object on the server that issues and revokes
// sign-ins, and its authenticator. A sign-in is a principal behind a token `<id>.<secret>` (random); only the secret's hash is
// kept. A browser carries its token in the kit's signed sign-in cookie (`HttpOnly`, `SameSite=Strict`), a command-line client in
// `Authorization: Bearer <token>`. Browser and device sign-ins persist in the server's store and expire after 400 days unused;
// ephemeral sign-ins (a pick-a-name login, a public route) live in memory only (EphemeralSignIns), at most maxEphemeral of them
// (past it the oldest ends), so a public login costs the same whatever came before; each ends with its client's connection (its
// last link closed, a grace period passed: the links endpoint's `opened` hook counts them), and none outlives the server's run.
//
// Go forced differences (language mechanics): a function that rejects returns `(value, error)`; the principal's generic parameter is designed
// out (a principal is a JSON object); the facet contributes through the `SignIns` object's StoreSelf, as the host-lifetime local
// type it is; a sign-in whose `lastUsed` or removal is written after an authentication is written in a goroutine of its own
// (upstream's un-awaited promise), on a context that keeps the request's values and not its cancellation.

// SignInKind is what a sign-in is for: a browser (its cookie), a device (a command-line client's token), an ephemeral login.
type SignInKind string

// The kinds of a sign-in.
const (
	SignInBrowser   SignInKind = "browser"
	SignInDevice    SignInKind = "device"
	SignInEphemeral SignInKind = "ephemeral"
)

// SignInInfo is a browser or device sign-in as `SignIns.list` shows it: never its token or hash. Kind is SignInBrowser or
// SignInDevice; Principal is the principal's JSON (the generic parameter is designed out: language mechanics).
type SignInInfo struct {
	ID        string
	Label     string
	Kind      SignInKind
	Principal delta.JsonValue
	Created   float64
	LastUsed  float64
}

// JSON is the sign-in as an element of the `list` member.
func (i SignInInfo) JSON() *delta.JsonObject {
	return delta.JsonObjectOf("id", i.ID, "label", i.Label, "kind", string(i.Kind), "principal", i.Principal, "created", i.Created, "lastUsed", i.LastUsed)
}

// SignIns is the kit's sign-ins (one per server): issue, verify, revoke, and the watchable list of persisted ones.
//
// `list` is the browser and device sign-ins as SignInInfo shows them: id, label, kind, principal, created, lastUsed; ephemeral
// ones are not listed (there may be many, and they are short). `issue` makes a new sign-in for `principal` and answers its id, its
// token (shown once, kept only hashed) and the sign-in cookie carrying it for a browser (`Set-Cookie`; `secure` for a remote one).
// `verify` is the principal `token` authenticates as, or null (unknown, revoked, expired, malformed). `revoke` ends sign-in `id`
// at once: its token authenticates nothing more.
var SignIns = mustLocal("chord.app.sign-ins", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("list", contracts.Arr(contracts.Obj(
			contracts.Prop("id", contracts.Str()),
			contracts.Prop("label", contracts.Str()),
			contracts.Prop("kind", contracts.OneOf("browser", "device")),
			contracts.Prop("principal", contracts.Unknown()),
			contracts.Prop("created", contracts.Num()),
			contracts.Prop("lastUsed", contracts.Num()),
		))),
		contracts.Mem("issue", contracts.NewMethod(
			contracts.Obj(
				contracts.Prop("principal", contracts.Unknown()),
				contracts.Prop("label", contracts.Str()),
				contracts.Prop("kind", contracts.OneOf("browser", "device", "ephemeral")),
				contracts.Prop("secure", contracts.Opt(contracts.Bool())),
			),
			contracts.Obj(contracts.Prop("id", contracts.Str()), contracts.Prop("token", contracts.Str()), contracts.Prop("cookie", contracts.Str())),
		)),
		contracts.Mem("verify", contracts.NewMethod(contracts.Str(), contracts.Unknown())),
		contracts.Mem("revoke", contracts.NewMethod(contracts.Str())),
	},
})

// SignInCookie is the name of the kit's sign-in cookie.
const SignInCookie = "chord-sign-in"

// signInsDocument is the stored sign-ins: by id, each with its secret's hash and its principal (as JSON text: any of the
// app's).
var signInsDocument = mustState("chord.app.sign-ins", documents.StateOptions{
	Schema: contracts.Obj(contracts.Prop("signIns", contracts.Map(contracts.Obj(
		contracts.Prop("hash", contracts.Str()),
		contracts.Prop("label", contracts.Str()),
		contracts.Prop("kind", contracts.OneOf("browser", "device")),
		contracts.Prop("principal", contracts.Str()),
		contracts.Prop("created", contracts.Num()),
		contracts.Prop("lastUsed", contracts.Num()),
	)))),
	Version: 1,
	Initial: func() *delta.JsonObject { return delta.JsonObjectOf("signIns", delta.NewJsonObject(0)) },
	Persist: true,
	CheckpointWhen: func(_ *delta.JsonObject, _ []delta.Op, info documents.CheckpointInfo) bool {
		return info.DeltasSinceBase >= 50
	},
})

const (
	// touchMs is how often a sign-in's `lastUsed` is written at most: once a minute (not one commit per request).
	touchMs = 60_000
	// signInIdleMs is how long a persisted sign-in lives unused: 400 days, as its cookie.
	signInIdleMs = 400 * 24 * 60 * 60_000
	// rollMs is how old a sign-in cookie gets before a response renews it: a day.
	rollMs = 24 * 60 * 60_000
)

// signInWrites counts the un-awaited writes an authentication started (a sign-in's `lastUsed`, a lazy removal): tests wait for them
// instead of sleeping.
var signInWrites sync.WaitGroup

// abortedWith is a context that ends as ctx does, except for the end of a finished request (chordhttp.ErrRequestFinished), which Pi's
// request context never has: the write it covers outlives the request. release frees it.
func abortedWith(ctx context.Context) (context.Context, func()) {
	write, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	ended := func() {
		if cause := context.Cause(ctx); !errors.Is(cause, chordhttp.ErrRequestFinished) {
			cancel(cause)
		}
	}
	if ctx.Err() != nil {
		ended()
		return write, func() { cancel(nil) }
	}
	stop := context.AfterFunc(ctx, ended)
	return write, func() { stop(); cancel(nil) }
}

// SignInLimits are the sign-ins' limits: how many ephemeral sign-ins live at once (1,000 by default; past it the oldest ends).
type SignInLimits struct{ MaxEphemeral int }

// KitSignIns are the kit's sign-ins: the facet for the server's core facets, and the links endpoint's `opened` hook.
type KitSignIns struct {
	Facet host.Facet
	// Opened says a link opened with request's credentials: an ephemeral sign-in's links are counted (it ends with its last).
	// It returns what to call when the link closes, nil for none.
	Opened func(request chordhttp.HttpRequest) (closed func())
}

// CreateSignIns are the kit's sign-ins: its facet serving `SignIns` (a host-lifetime object) and contributing its authenticator
// (the bearer token first, else the sign-in cookie signed with cookieSecret), and the links hook ending an ephemeral sign-in with
// its client's connection.
func CreateSignIns(cookieSecret string, limits SignInLimits) *KitSignIns {
	maxEphemeral := limits.MaxEphemeral
	if maxEphemeral == 0 {
		maxEphemeral = 1000
	}
	ephemeral := NewEphemeralSignIns(maxEphemeral, 0)
	opened := func(request chordhttp.HttpRequest) func() {
		token, ok := bearerToken(request)
		if !ok {
			token, ok = chordhttp.ReadCookie(request, SignInCookie, cookieSecret)
		}
		if dot := strings.IndexByte(token, '.'); ok && dot > 0 {
			return ephemeral.LinkOpened(token[:dot])
		}
		return nil
	}
	return &KitSignIns{Facet: signInsFacet(cookieSecret, ephemeral), Opened: opened}
}

// signInsFacet is the facet of CreateSignIns.
func signInsFacet(cookieSecret string, ephemeral *EphemeralSignIns) host.Facet {
	return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		return h.Serve(SignIns, func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
			document := self.State(signInsDocument).(*documents.Singleton)
			change := func(ctx context.Context, fn func(signIns *delta.Object)) {
				// Not awaited by authentication: the request goes on; a write that fails is made again by a later request. As Pi's
				// `document.change(fn, context)` the write is tied to the request's context: a disconnect or the server's close ends
				// it (and the touch is dropped, left to the next request). A finished request is no abort in Pi, so it ends nothing.
				write, release := abortedWith(ctx)
				signInWrites.Go(func() {
					defer release()
					_, _ = document.Change(write, func(draft *delta.Object) (any, error) {
						fn(draft.Object("signIns"))
						return nil, nil
					})
				})
			}
			// authenticate is the principal of token, or nil; it touches or lazily removes the persisted sign-in it names.
			authenticate := func(token string, ctx context.Context) data.Principal {
				dot := strings.IndexByte(token, '.')
				if dot <= 0 {
					return nil
				}
				id, secret := token[:dot], token[dot+1:]
				if hash, principal, ok := ephemeral.Get(id); ok {
					if MatchesHash(secret, hash) {
						return principal
					}
					return nil
				}
				value, err := document.Value()
				if err != nil {
					return nil
				}
				// A trust boundary: `id` is the client's (an own key only: no inherited member is a sign-in).
				signIns := value.Value("signIns").(*delta.JsonObject)
				found, ok := signIns.Value(id).(*delta.JsonObject)
				if !ok || !MatchesHash(secret, found.Value("hash").(string)) {
					return nil
				}
				now, lastUsed := jsclock.DateNow(), found.Value("lastUsed").(float64)
				if now-lastUsed > signInIdleMs {
					change(ctx, func(draft *delta.Object) { draft.Delete(id) })
					return nil
				}
				if now-lastUsed > touchMs {
					change(ctx, func(draft *delta.Object) {
						if draft.Has(id) {
							_ = draft.Object(id).Set("lastUsed", now)
						}
					})
				}
				// Stored by `issue` from a principal of the app's type.
				decoded, err := delta.DecodeJson([]byte(found.Value("principal").(string)))
				if err != nil {
					return nil
				}
				principal, _ := decoded.(*delta.JsonObject)
				return principal
			}
			store := self.(host.StoreSelf)
			store.Contribute(registries.Registry[any, any](chordhttp.Authenticators), chordhttp.Authenticator(
				func(ctx context.Context, request chordhttp.HttpRequest) (data.Principal, error) {
					token, ok := bearerToken(request)
					if !ok {
						token, ok = chordhttp.ReadCookie(request, SignInCookie, cookieSecret)
					}
					if !ok {
						return nil, nil
					}
					return authenticate(token, ctx), nil
				}))
			store.Contribute(registries.Registry[any, any](chordhttp.Http), chordhttp.HttpContribution{
				Wraps: []chordhttp.HttpWrap{rollingCookie(cookieSecret, authenticate)},
			})
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"list": self.Derive([]any{document}, func(values []any) (delta.JsonValue, error) {
						signIns := values[0].(*delta.JsonObject).Value("signIns").(*delta.JsonObject)
						list := make([]any, 0, signIns.Len())
						for id, each := range signIns.All() {
							signIn := each.(*delta.JsonObject)
							// Stored by `issue` as JSON text.
							principal, err := delta.DecodeJson([]byte(signIn.Value("principal").(string)))
							if err != nil {
								return nil, err
							}
							list = append(list, SignInInfo{
								ID: id, Label: signIn.Value("label").(string), Kind: SignInKind(signIn.Value("kind").(string)), Principal: principal,
								Created: signIn.Value("created").(float64), LastUsed: signIn.Value("lastUsed").(float64),
							}.JSON())
						}
						return list, nil
					}),
				},
				Methods: map[string]contracts.MethodFunction{
					"issue": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						arguments := args.(*delta.JsonObject)
						id, secret := NewToken(12), NewToken(32)
						kind := SignInKind(arguments.Value("kind").(string))
						principal := arguments.Value("principal")
						if kind == SignInEphemeral {
							// Principals are the app's: `issue`'s callers pass one of its type.
							owner, _ := principal.(*delta.JsonObject)
							ephemeral.Issue(id, HashToken(secret), owner)
						} else {
							now := jsclock.DateNow()
							stored, err := json.Marshal(principal)
							if err != nil {
								return nil, err
							}
							_, err = document.Change(ctx, func(draft *delta.Object) (any, error) {
								return nil, draft.Object("signIns").Set(id, delta.JsonObjectOf(
									"hash", HashToken(secret), "label", arguments.Value("label"), "kind", string(kind),
									"principal", string(stored), "created", now, "lastUsed", now,
								))
							})
							if err != nil {
								return nil, err
							}
						}
						token := id + "." + secret
						// Only whether the browser is remote matters to the cookie (`Secure`).
						provenance := chordhttp.Provenance{Kind: chordhttp.ProvenanceLocal}
						if secure, _ := arguments.Value("secure").(bool); secure {
							provenance = chordhttp.Provenance{Kind: chordhttp.ProvenanceRemote}
						}
						return delta.JsonObjectOf("id", id, "token", token, "cookie", signInCookie(token, kind, provenanceRequest{provenance: provenance}, cookieSecret)), nil
					},
					"verify": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						if principal := authenticate(args.(string), ctx); principal != nil {
							return principal, nil
						}
						return nil, nil
					},
					"revoke": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						id := args.(string)
						if ephemeral.Revoke(id) {
							return nil, nil
						}
						_, err := document.Change(ctx, func(draft *delta.Object) (any, error) {
							draft.Object("signIns").Delete(id)
							return nil, nil
						})
						return nil, err
					},
				},
			}}, nil
		}, host.ServeOptions{})
	})
}

// provenanceRequest is the only part of a request a cookie reads when it is made for a sign-in: where it came from.
type provenanceRequest struct {
	chordhttp.HttpRequest
	provenance chordhttp.Provenance
}

func (r provenanceRequest) Provenance() chordhttp.Provenance { return r.provenance }

// signInCookie is the sign-in cookie carrying token (a sign-in of kind) for a browser of request's provenance: a persisted
// sign-in's lives as long as the sign-in does unused (400 days, renewed by rollingCookie), an ephemeral one's only while the
// browser runs.
func signInCookie(token string, kind SignInKind, request chordhttp.HttpRequest, secret string) string {
	var maxAge *float64
	if kind != SignInEphemeral {
		maxAge = new(float64(signInIdleMs / 1000))
	}
	return chordhttp.Cookie(SignInCookie, token, chordhttp.CookieOptions{Secret: secret, Request: request, MaxAge: maxAge})
}

// rollingCookie is the wrap around every route renewing the sign-in cookie a request signed in with once it is older than a day:
// the response sets it again with a fresh signed expiry, so a browser used within every 400 days stays signed in. Not when the
// route sets a cookie itself, nor for an ephemeral sign-in's (it has no expiry) or a token that no longer authenticates.
func rollingCookie(secret string, authenticate func(token string, ctx context.Context) data.Principal) chordhttp.HttpWrap {
	return chordhttp.HttpWrap{
		Name:  "chord.app.rolling-cookie",
		Phase: chordhttp.PhaseHeaders,
		Scope: chordhttp.ScopeAll,
		Wrap: func(request chordhttp.HttpRequest, next func(chordhttp.HttpRequest) (chordhttp.HttpResponse, error), ctx host.Ctx) (chordhttp.HttpResponse, error) {
			response, err := next(request)
			if err != nil || ctx.Principal() == nil {
				return response, err
			}
			for _, header := range response.Headers {
				if strings.EqualFold(header.Name, "set-cookie") {
					return response, nil
				}
			}
			signed, ok := chordhttp.ReadSignedCookie(request, SignInCookie, secret)
			if !ok || signed.Expires == nil || *signed.Expires-jsclock.DateNow() > signInIdleMs-rollMs {
				return response, nil
			}
			if authenticate(signed.Value, ctx) == nil {
				return response, nil
			}
			renewed := signInCookie(signed.Value, SignInBrowser, request, secret)
			response.Headers = append(append([]chordhttp.ResponseHeader(nil), response.Headers...), chordhttp.HeaderOf("Set-Cookie", renewed))
			return response, nil
		},
	}
}

var bearerPattern = regexp.MustCompile(`^[Bb][Ee][Aa][Rr][Ee][Rr]\s+(\S+)$`)

// bearerToken is the token of an `Authorization: Bearer <token>` header.
func bearerToken(request chordhttp.HttpRequest) (string, bool) {
	header, ok := chordhttp.HeaderGet(request.Headers(), "authorization")
	if !ok {
		return "", false
	}
	match := bearerPattern.FindStringSubmatch(header)
	if match == nil {
		return "", false
	}
	return match[1], true
}
