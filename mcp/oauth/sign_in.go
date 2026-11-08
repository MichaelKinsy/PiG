package oauth

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// Ports packages/mcp/src/oauth/sign-in.ts (Pi bb266bf35).
//
// Sign-in to an OAuth-protected MCP server, in two halves so an application can complete it in another request with the same state store:
// [StartMcpSignIn] refreshes the stored tokens when it can, or else prepares the browser step and returns the authorization URL;
// [CompleteMcpSignIn] exchanges the authorization code. The code arrives through a loopback callback server (see loopback.go), the application's
// own https callback route, or a redirect URL the user pastes.

// McpSignInOptions are the options of both halves of a sign-in. Pass the same values to [StartMcpSignIn] and [CompleteMcpSignIn].
//
// Go mechanic: Pi's `signal` is the context given to each half. Ending it stops the sign-in with [McpSignInCancelledError].
type McpSignInOptions struct {
	McpOAuthClientOptions
	// RedirectURL is the redirect URI chosen by the caller: a loopback one ([ListenForLoopbackSignIn]) or an https callback route of the
	// application. Pass the same one to both halves: the authorization request may use a variant specific to the MCP server
	// ([McpSignInRedirect.RedirectURL]), which the flow derives from it.
	RedirectURL string
	// Challenge is the server's challenge that asked for the sign-in; its resource metadata URL and scope apply.
	Challenge *OAuthChallenge
}

// McpSignInRedirect is the browser step of a sign-in, from [StartMcpSignIn].
//
// Plain data, so an application can persist it: its JSON is Pi's.
type McpSignInRedirect struct {
	// Kind is "redirect" ([McpSignInKindRedirect]).
	Kind McpSignInKind `json:"kind"`
	// AuthorizationURL is opened in the user's browser.
	AuthorizationURL string `json:"authorizationUrl,omitempty"`
	// RedirectURL is the redirect URI of the authorization request; the authorization response must arrive on it.
	RedirectURL string `json:"redirectUrl,omitempty"`
	// State is the `state` parameter the authorization response must carry.
	State string `json:"state,omitempty"`
	// Scope is the scope requested, or "". Saved with the tokens when the token response names none.
	Scope string `json:"scope,omitempty"`
}

// McpSignInKind is the `kind` of a [McpSignInStart] or a [McpSignInRedirect].
type McpSignInKind string

// The kinds of a [McpSignInStart].
const (
	McpSignInKindAuthorized McpSignInKind = "authorized"
	McpSignInKindRedirect   McpSignInKind = "redirect"
)

// McpSignInStart is the result of [StartMcpSignIn]: signed in by refreshing the stored tokens (Kind "authorized"), or the browser step (Kind
// "redirect", with the fields of [McpSignInRedirect]).
//
// Go mechanic: Pi's `{ kind: "authorized" } | McpSignInRedirect` is a struct that carries both members, told apart by Kind. The members of
// [McpSignInRedirect] are zero for an "authorized" result and omitted from its JSON.
type McpSignInStart struct {
	Kind McpSignInKind `json:"kind"`
	// AuthorizationURL, RedirectURL, State and Scope are those of [McpSignInRedirect], for Kind "redirect".
	AuthorizationURL string `json:"authorizationUrl,omitempty"`
	RedirectURL      string `json:"redirectUrl,omitempty"`
	State            string `json:"state,omitempty"`
	Scope            string `json:"scope,omitempty"`
}

// Redirect is the browser step of a "redirect" result.
func (s McpSignInStart) Redirect() McpSignInRedirect {
	return McpSignInRedirect(s)
}

// McpAuthorizationResponse is an authorization response: from a callback server, a callback route, or [ResponseFromRedirectURL].
type McpAuthorizationResponse = OAuthCallback

// McpSignInCancelledError reports that the user or the application cancelled a sign-in (its context ended, or the user gave up).
type McpSignInCancelledError struct{}

// NewMcpSignInCancelledError is `new McpSignInCancelledError()`.
func NewMcpSignInCancelledError() *McpSignInCancelledError { return &McpSignInCancelledError{} }

// Name is the `name` property, "McpSignInCancelledError".
func (e *McpSignInCancelledError) Name() string { return "McpSignInCancelledError" }

func (e *McpSignInCancelledError) Error() string { return "Sign-in cancelled" }

// StartMcpSignIn is the first half of a sign-in. Without a step-up (`insufficient_scope`), it first tries to refresh the stored tokens with the
// stored client, whatever RedirectURL is (Authorized). The refresh takes the store's refresh lock, so never call this while holding it; when the
// lock cannot be taken (for example it timed out), the refresh is skipped. Otherwise it prepares the browser step (Redirect): a fresh `state`,
// and a stored client that cannot use RedirectURL is dropped with its tokens. Requests to the authorization server are time-limited.
func StartMcpSignIn(ctx context.Context, options McpSignInOptions) (McpSignInStart, error) {
	if ctx.Err() != nil {
		return McpSignInStart{}, NewMcpSignInCancelledError()
	}
	// A refresh keeps the granted scope; a server asking for more needs the browser step.
	stepUp := options.Challenge != nil && options.Challenge.Error == "insufficient_scope"
	if !stepUp {
		result, err := refreshBeforeSignIn(ctx, options)
		if err != nil {
			return McpSignInStart{}, err
		}
		if result == OAuthAuthorized {
			return McpSignInStart{Kind: McpSignInKindAuthorized}, nil
		}
	}
	redirect, err := cancellable(ctx, func() (McpSignInRedirect, error) { return startBrowserStep(ctx, options, stepUp) })
	if err != nil {
		return McpSignInStart{}, err
	}
	return McpSignInStart{Kind: McpSignInKindRedirect, AuthorizationURL: redirect.AuthorizationURL, RedirectURL: redirect.RedirectURL, State: redirect.State, Scope: redirect.Scope}, nil
}

// refreshBeforeSignIn refreshes the stored tokens, unless `cimd` is to replace a registered client. The refresh uses RedirectURL when the tokens came
// from a Client ID Metadata Document whose documents list it.
//
// Ending ctx stops the wait for the refresh lock at once ([McpSignInCancelledError]). A lock that arrives after that runs nothing and is released, and
// a refresh already sent still completes and saves its tokens (Pi 797e122cb).
func refreshBeforeSignIn(ctx context.Context, options McpSignInOptions) (OAuthFlowResult, error) {
	type outcome struct {
		result OAuthFlowResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		var result OAuthFlowResult
		// The lock wait is not tied to ctx: it ends when the lock is taken (or fails) and releases it at once for a sign-in that was cancelled.
		err := options.Store.WithRefreshLock(context.WithoutCancel(ctx), func(lockCtx context.Context) error {
			result = OAuthRedirect
			// The lock may arrive after the sign-in was cancelled: return at once, so it is released.
			if ctx.Err() != nil {
				return nil
			}
			state, err := options.Store.Load(lockCtx)
			if err != nil {
				return err
			}
			if state == nil || state.Tokens == nil || state.Tokens.RefreshToken == "" {
				return nil
			}
			if options.Settings.ClientRegistration == "cimd" && state.ClientInformation != nil && options.Settings.ClientID == "" {
				return nil
			}
			// A refresh the server answered may have rotated the refresh token, so its answer must be saved.
			result, err = RefreshStoredTokens(lockCtx, options.McpOAuthClientOptions, *state, RefreshStoredTokensOptions{Challenge: options.Challenge, RedirectURL: options.RedirectURL})
			return err
		})
		done <- outcome{result, err}
	}()
	select {
	case <-ctx.Done():
		return "", NewMcpSignInCancelledError()
	case o := <-done:
		// Another process is refreshing and took too long. The user signs in instead.
		if _, busy := errors.AsType[*McpRefreshLockBusyError](o.err); busy {
			return OAuthRedirect, nil
		}
		return o.result, o.err
	}
}

func startBrowserStep(ctx context.Context, options McpSignInOptions, stepUp bool) (McpSignInRedirect, error) {
	stored, err := options.Store.Load(ctx)
	if err != nil {
		return McpSignInRedirect{}, err
	}
	if stored != nil {
		if err := options.Store.Save(ctx, stateForSignIn(*stored, options)); err != nil {
			return McpSignInRedirect{}, err
		}
	}
	var authorizationURL *URL
	provider, err := CreateOAuthClientProvider(options.McpOAuthClientOptions, options.RedirectURL, func(u URL) { authorizationURL = &u })
	if err != nil {
		return McpSignInRedirect{}, err
	}
	// A server asking for more scope gets it on top of the configured scope and, since the challenge may list only the missing scopes, on top of
	// the scope granted so far.
	challengeScope := ""
	if options.Challenge != nil {
		challengeScope = options.Challenge.Scope
	}
	if stepUp {
		granted := ""
		if stored != nil && stored.Tokens != nil {
			granted = stored.Tokens.Scope
		}
		challengeScope = StepUpScope(granted, challengeScope)
	}
	flow := flowOptions(options)
	flow.Scope = mergeScopes(options.Settings.Scope, challengeScope)
	flow.SkipRefresh = true
	if _, err := AuthorizeMcp(ctx, provider, flow); err != nil {
		return McpSignInRedirect{}, err
	}
	if authorizationURL == nil {
		return McpSignInRedirect{}, errors.New("OAuth flow did not produce an authorization URL")
	}
	query := nodeurl.SearchParams(authorizationURL.Query)
	state, err := provider.State(ctx)
	if err != nil {
		return McpSignInRedirect{}, err
	}
	// The flow picks the redirect URI, which may be specific to the MCP server.
	redirectURL := options.RedirectURL
	if query.Has("redirect_uri") {
		redirectURL = query.Get("redirect_uri")
	}
	return McpSignInRedirect{Kind: McpSignInKindRedirect, AuthorizationURL: authorizationURL.Href(), RedirectURL: redirectURL, State: state, Scope: query.Get("scope")}, nil
}

// stateForSignIn is the stored state for a browser step: without the last sign-in's `state`, and without a client that cannot use the redirect
// URI, along with its tokens.
func stateForSignIn(stored McpOAuthState, options McpSignInOptions) McpOAuthState {
	settings := options.Settings
	next := stored.Clone()
	next.OAuthState = ""
	// A registered client cannot use another redirect URI, and its tokens belong to it. `cimd` replaces a registered client; `auto` keeps it. A
	// Client ID Metadata Document is not stored, so tokens without a stored client belong to the document.
	keepClient := settings.ClientID != ""
	if !keepClient {
		if stored.ClientInformation != nil {
			keepClient = settings.ClientRegistration != "cimd" && slices.Contains(RegisteredRedirectURLs(stored.ClientInformation), options.RedirectURL)
		} else {
			keepClient = OffersClientMetadataDocument(settings, options.ClientMetadataDocuments, options.RedirectURL)
		}
	}
	if !keepClient {
		next.ClientInformation, next.Tokens, next.TokensExpireAt = nil, nil, nil
	}
	return next
}

// mergeScopes returns the scopes of both lists, each once.
func mergeScopes(scopes ...string) string {
	var merged []string
	for _, scope := range scopes {
		// `split(/\s+/)`: JavaScript whitespace.
		for s := range strings.FieldsFuncSeq(scope, jsstring.IsSpace) {
			if !slices.Contains(merged, s) {
				merged = append(merged, s)
			}
		}
	}
	return strings.Join(merged, " ")
}

// McpCompleteSignInOptions are the options of [CompleteMcpSignIn]: those of [StartMcpSignIn] and the two results of the first half. Pi types them
// as an inline intersection of McpSignInOptions with `authorization` and `response`.
type McpCompleteSignInOptions struct {
	McpSignInOptions
	// Authorization is the `redirect` result of [StartMcpSignIn].
	Authorization McpSignInRedirect
	// Response is the authorization response.
	Response McpAuthorizationResponse
}

// CompleteMcpSignIn is the second half of a sign-in: it exchanges the authorization code of response and saves the tokens. Pass the options
// given to [StartMcpSignIn], including its RedirectURL (not authorization.RedirectURL). It fails when the response carries another `state`,
// when another sign-in started with the store since, or when the exchange would use another redirect URI than the authorization request (the
// options changed). A redirect URL the user pasted goes through [ResponseFromRedirectURL] first, which checks its path.
func CompleteMcpSignIn(ctx context.Context, completion McpCompleteSignInOptions) error {
	options, authorization, response := completion.McpSignInOptions, completion.Authorization, completion.Response
	_, err := cancellable(ctx, func() (struct{}, error) {
		if response.State != authorization.State {
			return struct{}{}, errors.New("The authorization response belongs to a different sign-in")
		}
		// The stored PKCE verifier belongs to the latest sign-in; exchanging with it would fail with `invalid_grant`, which discards the stored
		// tokens.
		stored, err := options.Store.Load(ctx)
		if err != nil {
			return struct{}{}, err
		}
		if stored == nil || stored.OAuthState != authorization.State {
			return struct{}{}, errors.New("Another sign-in to this MCP server started since; sign in again")
		}
		provider, err := CreateOAuthClientProvider(options.McpOAuthClientOptions, options.RedirectURL, nil)
		if err != nil {
			return struct{}{}, err
		}
		flow := flowOptions(options)
		// Saved with the tokens when the token response names no scope, so it must be the requested one.
		flow.Scope = authorization.Scope
		flow.AuthorizationRedirectURL = &authorization.RedirectURL
		flow.AuthorizationCode = response.Code
		if response.Iss != "" {
			iss := response.Iss
			flow.Iss = &iss
		}
		_, err = AuthorizeMcp(ctx, provider, flow)
		return struct{}{}, err
	})
	return err
}

// flowOptions are the [AuthorizeMcp] options of a sign-in, without the scope.
func flowOptions(options McpSignInOptions) OAuthFlowOptions {
	flow := OAuthFlowOptions{
		ServerURL:                      options.ServerURL,
		Fetch:                          TimedFetch(options.Fetch),
		AuthorizationServerMetadataURL: options.Settings.AuthServerMetadataURL,
	}
	if options.Challenge != nil {
		flow.ResourceMetadataURL = options.Challenge.ResourceMetadataURL
	}
	return flow
}

// cancellable runs fn, reporting the end of ctx as [McpSignInCancelledError].
func cancellable[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	var zero T
	if ctx.Err() != nil {
		return zero, NewMcpSignInCancelledError()
	}
	value, err := fn()
	if err != nil {
		// Aborted requests fail with the context's cause; report them as the cancellation they are.
		if ctx.Err() != nil {
			return zero, NewMcpSignInCancelledError()
		}
		return zero, err
	}
	return value, nil
}
