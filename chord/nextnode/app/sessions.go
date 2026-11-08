package app

import (
	"context"
	"encoding/json"
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

// Ports packages/chord/src/node/app/sessions.ts
//
// Sessions (slice 13 §2, decision "The app kit, point 3: authentication"): the kit's object on the server that issues and revokes
// sessions, and its authenticator. A session is a principal behind a token `<id>.<secret>` (random); only the secret's hash is
// kept. A browser carries its token in the kit's signed session cookie (`HttpOnly`, `SameSite=Strict`), a command-line client in
// `Authorization: Bearer <token>`. Browser and device sessions persist in the server's store and expire after 400 days unused;
// ephemeral sessions (a pick-a-name login, a public route) live in memory only (EphemeralSessions), at most maxEphemeral of them
// (past it the oldest ends), so a public login costs the same whatever came before; each ends with its client's connection (its
// last link closed, a grace period passed: the links endpoint's `opened` hook counts them), and none outlives the server's run.
//
// Go forced differences (D457): a function that rejects returns `(value, error)`; the principal's generic parameter is designed
// out (a principal is a JSON object); the facet contributes through the `Sessions` object's StoreSelf, as the host-lifetime local
// type it is; a session whose `lastUsed` or removal is written after an authentication is written in a goroutine of its own
// (upstream's un-awaited promise), on a context that keeps the request's values and not its cancellation.

// SessionKind is what a session is for: a browser (its cookie), a device (a command-line client's token), an ephemeral login.
type SessionKind string

// The kinds of a session.
const (
	SessionBrowser   SessionKind = "browser"
	SessionDevice    SessionKind = "device"
	SessionEphemeral SessionKind = "ephemeral"
)

// Sessions is the kit's sessions (one per server): issue, verify, revoke, and the watchable list of persisted ones.
//
// `list` is the browser and device sessions as SessionInfo shows them: id, label, kind, principal, created, lastUsed; ephemeral
// ones are not listed (there may be many, and they are short). `issue` makes a new session for `principal` and answers its id, its
// token (shown once, kept only hashed) and the session cookie carrying it for a browser (`Set-Cookie`; `secure` for a remote one).
// `verify` is the principal `token` authenticates as, or null (unknown, revoked, expired, malformed). `revoke` ends session `id`
// at once: its token authenticates nothing more.
var Sessions = mustLocal("chord.app.sessions", contracts.LocalDeclaration{
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

// SessionCookie is the name of the kit's session cookie.
const SessionCookie = "chord-session"

// sessionsDocument is the stored sessions: by id, each with its secret's hash and its principal (as JSON text: any of the
// app's).
var sessionsDocument = mustState("chord.app.sessions", documents.StateOptions{
	Schema: contracts.Obj(contracts.Prop("sessions", contracts.Map(contracts.Obj(
		contracts.Prop("hash", contracts.Str()),
		contracts.Prop("label", contracts.Str()),
		contracts.Prop("kind", contracts.OneOf("browser", "device")),
		contracts.Prop("principal", contracts.Str()),
		contracts.Prop("created", contracts.Num()),
		contracts.Prop("lastUsed", contracts.Num()),
	)))),
	Version: 1,
	Initial: func() *delta.JsonObject { return delta.JsonObjectOf("sessions", delta.NewJsonObject(0)) },
	Persist: true,
	CheckpointWhen: func(_ *delta.JsonObject, _ []delta.Op, info documents.CheckpointInfo) bool {
		return info.DeltasSinceBase >= 50
	},
})

const (
	// touchMs is how often a session's `lastUsed` is written at most: once a minute (not one commit per request).
	touchMs = 60_000
	// sessionIdleMs is how long a persisted session lives unused: 400 days, as its cookie.
	sessionIdleMs = 400 * 24 * 60 * 60_000
	// rollMs is how old a session cookie gets before a response renews it: a day.
	rollMs = 24 * 60 * 60_000
)

// sessionWrites counts the un-awaited writes an authentication started (a session's `lastUsed`, a lazy removal): tests wait for them
// instead of sleeping.
var sessionWrites sync.WaitGroup

// SessionLimits are the sessions' limits: how many ephemeral sessions live at once (1,000 by default; past it the oldest ends).
type SessionLimits struct{ MaxEphemeral int }

// KitSessions are the kit's sessions: the facet for the server's core facets, and the links endpoint's `opened` hook.
type KitSessions struct {
	Facet host.Facet
	// Opened says a link opened with request's credentials: an ephemeral session's links are counted (it ends with its last).
	// It returns what to call when the link closes, nil for none.
	Opened func(request chordhttp.HttpRequest) (closed func())
}

// CreateSessions are the kit's sessions: its facet serving `Sessions` (a host-lifetime object) and contributing its authenticator
// (the bearer token first, else the session cookie signed with cookieSecret), and the links hook ending an ephemeral session with
// its client's connection.
func CreateSessions(cookieSecret string, limits SessionLimits) *KitSessions {
	maxEphemeral := limits.MaxEphemeral
	if maxEphemeral == 0 {
		maxEphemeral = 1000
	}
	ephemeral := NewEphemeralSessions(maxEphemeral, 0)
	opened := func(request chordhttp.HttpRequest) func() {
		token, ok := bearerToken(request)
		if !ok {
			token, ok = chordhttp.ReadCookie(request, SessionCookie, cookieSecret)
		}
		if dot := strings.IndexByte(token, '.'); ok && dot > 0 {
			return ephemeral.LinkOpened(token[:dot])
		}
		return nil
	}
	return &KitSessions{Facet: sessionsFacet(cookieSecret, ephemeral), Opened: opened}
}

// sessionsFacet is the facet of CreateSessions.
func sessionsFacet(cookieSecret string, ephemeral *EphemeralSessions) host.Facet {
	return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		return h.Serve(Sessions, func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
			document := self.State(sessionsDocument).(*documents.Singleton)
			change := func(ctx context.Context, fn func(sessions *delta.Object)) {
				// Not awaited by authentication: the request goes on; a write that fails is made again by a later request.
				detached := context.WithoutCancel(ctx)
				sessionWrites.Add(1)
				go func() {
					defer sessionWrites.Done()
					_, _ = document.Change(detached, func(draft *delta.Object) (any, error) {
						fn(draft.Object("sessions"))
						return nil, nil
					})
				}()
			}
			// authenticate is the principal of token, or nil; it touches or lazily removes the persisted session it names.
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
				// A trust boundary: `id` is the client's (an own key only: no inherited member is a session).
				sessions := value.Value("sessions").(*delta.JsonObject)
				found, ok := sessions.Value(id).(*delta.JsonObject)
				if !ok || !MatchesHash(secret, found.Value("hash").(string)) {
					return nil
				}
				now, lastUsed := jsclock.DateNow(), found.Value("lastUsed").(float64)
				if now-lastUsed > sessionIdleMs {
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
				func(request chordhttp.HttpRequest, ctx context.Context) (data.Principal, error) {
					token, ok := bearerToken(request)
					if !ok {
						token, ok = chordhttp.ReadCookie(request, SessionCookie, cookieSecret)
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
						sessions := values[0].(*delta.JsonObject).Value("sessions").(*delta.JsonObject)
						list := make([]any, 0, sessions.Len())
						for id, each := range sessions.All() {
							session := each.(*delta.JsonObject)
							// Stored by `issue` as JSON text.
							principal, err := delta.DecodeJson([]byte(session.Value("principal").(string)))
							if err != nil {
								return nil, err
							}
							list = append(list, delta.JsonObjectOf(
								"id", id, "label", session.Value("label"), "kind", session.Value("kind"), "principal", principal,
								"created", session.Value("created"), "lastUsed", session.Value("lastUsed"),
							))
						}
						return list, nil
					}),
				},
				Methods: map[string]contracts.MethodFunction{
					"issue": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						arguments := args.(*delta.JsonObject)
						id, secret := NewToken(12), NewToken(32)
						kind := SessionKind(arguments.Value("kind").(string))
						principal := arguments.Value("principal")
						if kind == SessionEphemeral {
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
								return nil, draft.Object("sessions").Set(id, delta.JsonObjectOf(
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
						return delta.JsonObjectOf("id", id, "token", token, "cookie", sessionCookie(token, kind, provenanceRequest{provenance: provenance}, cookieSecret)), nil
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
							draft.Object("sessions").Delete(id)
							return nil, nil
						})
						return nil, err
					},
				},
			}}, nil
		}, host.ServeOptions{})
	})
}

// provenanceRequest is the only part of a request a cookie reads when it is made for a session: where it came from.
type provenanceRequest struct {
	chordhttp.HttpRequest
	provenance chordhttp.Provenance
}

func (r provenanceRequest) Provenance() chordhttp.Provenance { return r.provenance }

// sessionCookie is the session cookie carrying token (a session of kind) for a browser of request's provenance: a persisted
// session's lives as long as the session does unused (400 days, renewed by rollingCookie), an ephemeral one's only while the
// browser runs.
func sessionCookie(token string, kind SessionKind, request chordhttp.HttpRequest, secret string) string {
	var maxAge *float64
	if kind != SessionEphemeral {
		maxAge = new(float64(sessionIdleMs / 1000))
	}
	return chordhttp.Cookie(SessionCookie, token, chordhttp.CookieOptions{Secret: secret, Request: request, MaxAge: maxAge})
}

// rollingCookie is the wrap around every route renewing the session cookie a request signed in with once it is older than a day:
// the response sets it again with a fresh signed expiry, so a browser used within every 400 days stays signed in. Not when the
// route sets a cookie itself, nor for an ephemeral session's (it has no expiry) or a token that no longer authenticates.
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
			signed, ok := chordhttp.ReadSignedCookie(request, SessionCookie, secret)
			if !ok || signed.Expires == nil || *signed.Expires-jsclock.DateNow() > sessionIdleMs-rollMs {
				return response, nil
			}
			if authenticate(signed.Value, ctx) == nil {
				return response, nil
			}
			renewed := sessionCookie(signed.Value, SessionBrowser, request, secret)
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
