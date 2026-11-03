package oauth

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// Ports packages/mcp/src/oauth/sign-in.ts.
//
// Sign-in to an OAuth-protected MCP server, in two halves so an application can complete it in another request with the
// same state store: [StartMcpSignIn] refreshes the stored tokens when it can, or else prepares the browser step and
// returns the authorization URL; [CompleteMcpSignIn] exchanges the authorization code. The code arrives through a
// loopback callback server (loopback.go), the application's own https callback route, or a redirect URL the user pastes.
// Ending the context of either half is upstream's `signal` aborting.

// McpSignInOptions are the options of both halves of a sign-in. Pass the same values to [StartMcpSignIn] and
// [CompleteMcpSignIn].
type McpSignInOptions struct {
	McpOAuthClientOptions
	// RedirectURL is the redirect URI chosen by the caller: a loopback one ([ListenForLoopbackSignIn]) or an https
	// callback route of the application. Pass the same one to both halves: the authorization request may use a variant
	// specific to the MCP server ([McpSignInRedirect.RedirectURL]), which the flow derives from it.
	RedirectURL string
	// Challenge is the server's challenge that asked for the sign-in; its resource metadata URL and scope apply.
	Challenge *OAuthChallenge
}

// McpSignInRedirect is the browser step of a sign-in, from [StartMcpSignIn]. It is plain data, so an application can
// persist it.
type McpSignInRedirect struct {
	// Kind is "redirect".
	Kind string `json:"kind"`
	// AuthorizationURL is opened in the user's browser.
	AuthorizationURL string `json:"authorizationUrl"`
	// RedirectURL is the redirect URI of the authorization request; the authorization response must arrive on it.
	RedirectURL string `json:"redirectUrl"`
	// State is the `state` parameter the authorization response must carry.
	State string `json:"state"`
	// Scope is the scope requested, if any. Saved with the tokens when the token response names none.
	Scope string `json:"scope,omitempty"`
}

// McpSignInStart is the result of [StartMcpSignIn]: signed in by refreshing the stored tokens, or the browser step.
type McpSignInStart struct {
	// Kind is "authorized" or "redirect".
	Kind string
	// Redirect is the browser step when Kind is "redirect".
	Redirect *McpSignInRedirect
}

// McpAuthorizationResponse is an authorization response: from a callback server, a callback route, or
// [ResponseFromRedirectURL]. An empty Iss means the response had none.
type McpAuthorizationResponse = OAuthCallback

// McpSignInCancelledError reports that the user or the application cancelled a sign-in (its context ended, or the user
// gave up).
type McpSignInCancelledError struct{}

func (e *McpSignInCancelledError) Error() string { return "Sign-in cancelled" }

// Name is the `name` property, "McpSignInCancelledError".
func (e *McpSignInCancelledError) Name() string { return "McpSignInCancelledError" }

// StartMcpSignIn is the first half of a sign-in. Without a step-up (`insufficient_scope`), it first tries to refresh
// the stored tokens with the stored client, whatever RedirectURL is ("authorized"). The refresh takes the store's
// refresh lock, so never call this while holding it; when another process holds the lock for the whole wait
// ([McpRefreshLockBusyError]), the refresh is skipped, and any other failure of the lock fails the sign-in. Otherwise it
// prepares the browser step ("redirect"): a fresh `state`, and a stored client that cannot use RedirectURL is dropped
// with its tokens. Requests to the authorization server are time-limited. Ending ctx stops the sign-in at once with
// [McpSignInCancelledError], also while it waits for the store's refresh lock. A refresh already sent still completes
// and saves its tokens.
func StartMcpSignIn(ctx context.Context, options McpSignInOptions) (McpSignInStart, error) {
	if ctx.Err() != nil {
		return McpSignInStart{}, &McpSignInCancelledError{}
	}
	// A refresh keeps the granted scope; a server asking for more needs the browser step.
	stepUp := options.Challenge != nil && options.Challenge.Error == "insufficient_scope"
	if !stepUp {
		result, err := refreshBeforeSignIn(ctx, options)
		if err != nil {
			return McpSignInStart{}, err
		}
		if result == OAuthAuthorized {
			return McpSignInStart{Kind: "authorized"}, nil
		}
	}
	redirect, err := cancellable(ctx, func() (McpSignInRedirect, error) { return startBrowserStep(ctx, options, stepUp) })
	if err != nil {
		return McpSignInStart{}, err
	}
	return McpSignInStart{Kind: "redirect", Redirect: &redirect}, nil
}

// refreshBeforeSignIn refreshes the stored tokens, unless `cimd` is to replace a registered client. The refresh uses
// RedirectURL when the tokens came from a Client ID Metadata Document whose documents list it. Ending ctx returns at
// once; the refresh keeps running, since a refresh the server answered may have rotated the refresh token, so its
// answer must be saved.
func refreshBeforeSignIn(ctx context.Context, options McpSignInOptions) (OAuthFlowResult, error) {
	settings := options.Settings
	type outcome struct {
		result OAuthFlowResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result := OAuthRedirect
		err := options.Store.WithRefreshLock(ctx, func(lockCtx context.Context) error {
			// The lock may arrive after the sign-in was cancelled: return at once, so it is released.
			if ctx.Err() != nil {
				return nil
			}
			flowCtx := context.WithoutCancel(lockCtx)
			state, err := options.Store.Load(flowCtx)
			if err != nil {
				return err
			}
			if state == nil || state.Tokens == nil || state.Tokens.RefreshToken == "" {
				return nil
			}
			if settings.ClientRegistration == "cimd" && state.ClientInformation != nil && settings.ClientID == "" {
				return nil
			}
			result, err = refreshStoredTokens(flowCtx, options.McpOAuthClientOptions, state, refreshOptions{challenge: options.Challenge, redirectURL: options.RedirectURL})
			return err
		})
		done <- outcome{result, err}
	}()
	select {
	case o := <-done:
		// Another process is refreshing and took too long. The user signs in instead.
		if _, busy := errors.AsType[*McpRefreshLockBusyError](o.err); busy {
			return OAuthRedirect, nil
		}
		if o.err != nil {
			return "", o.err
		}
		return o.result, nil
	case <-ctx.Done():
		return "", &McpSignInCancelledError{}
	}
}

func startBrowserStep(ctx context.Context, options McpSignInOptions, stepUp bool) (McpSignInRedirect, error) {
	settings, challenge := options.Settings, options.Challenge
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
	provider, err := createOAuthClientProvider(options.McpOAuthClientOptions, options.RedirectURL, func(u URL) { authorizationURL = &u })
	if err != nil {
		return McpSignInRedirect{}, err
	}
	var challengeScope string
	if challenge != nil {
		challengeScope = challenge.Scope
	}
	if stepUp {
		grantedScope := ""
		if stored != nil && stored.Tokens != nil {
			grantedScope = stored.Tokens.Scope
		}
		challengeScope = StepUpScope(grantedScope, challengeScope)
	}
	// A server asking for more scope gets it on top of the configured scope and, since the challenge may list only the
	// missing scopes, on top of the scope granted so far.
	flow := flowOptions(options)
	flow.Scope = mergeScopes(settings.Scope, challengeScope)
	flow.SkipRefresh = true
	if _, err := AuthorizeMcp(ctx, provider, flow); err != nil {
		return McpSignInRedirect{}, err
	}
	if authorizationURL == nil {
		return McpSignInRedirect{}, errors.New("OAuth flow did not produce an authorization URL")
	}
	query := nodeurl.SearchParams(authorizationURL.Query)
	// The flow picks the redirect URI, which may be specific to the MCP server.
	redirectURL := options.RedirectURL
	if query.Has("redirect_uri") {
		redirectURL = query.Get("redirect_uri")
	}
	state, err := provider.State(ctx)
	if err != nil {
		return McpSignInRedirect{}, err
	}
	return McpSignInRedirect{
		Kind: "redirect", AuthorizationURL: authorizationURL.Href(), RedirectURL: redirectURL, State: state, Scope: query.Get("scope"),
	}, nil
}

// stateForSignIn is the stored state for a browser step: without the last sign-in's `state`, and without a client that
// cannot use RedirectURL, along with its tokens.
func stateForSignIn(stored McpOAuthState, options McpSignInOptions) McpOAuthState {
	settings, redirectURL := options.Settings, options.RedirectURL
	next := stored.Clone()
	next.OAuthState = ""
	// A registered client cannot use another redirect URI, and its tokens belong to it. `cimd` replaces a registered
	// client; `auto` keeps it. A Client ID Metadata Document is not stored, so tokens without a stored client belong to
	// the document.
	keepClient := settings.ClientID != ""
	if !keepClient {
		if stored.ClientInformation != nil {
			keepClient = settings.ClientRegistration != "cimd" && slices.Contains(registeredRedirectURLs(stored.ClientInformation), redirectURL)
		} else {
			keepClient = offersClientMetadataDocument(settings, options.ClientMetadataDocuments, redirectURL)
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
		for _, s := range scopeFields(scope) {
			if !slices.Contains(merged, s) {
				merged = append(merged, s)
			}
		}
	}
	return strings.Join(merged, " ")
}

// McpSignInCompletion are the options of [CompleteMcpSignIn]: those given to [StartMcpSignIn], the result of its
// browser step, and the authorization response.
type McpSignInCompletion struct {
	McpSignInOptions
	// Authorization is the "redirect" result of [StartMcpSignIn].
	Authorization McpSignInRedirect
	Response      McpAuthorizationResponse
}

// CompleteMcpSignIn is the second half of a sign-in: it exchanges the authorization code of Response and saves the
// tokens. Pass the options given to [StartMcpSignIn], including its RedirectURL (not Authorization.RedirectURL). It
// fails when the response carries another `state`, when another sign-in started with the store since, or when the
// exchange would use another redirect URI than the authorization request (the options changed). A redirect URL the user
// pasted goes through [ResponseFromRedirectURL] first, which checks its path.
func CompleteMcpSignIn(ctx context.Context, options McpSignInCompletion) error {
	_, err := cancellable(ctx, func() (struct{}, error) {
		authorization, response := options.Authorization, options.Response
		if response.State != authorization.State {
			return struct{}{}, errors.New("The authorization response belongs to a different sign-in")
		}
		// The stored PKCE verifier belongs to the latest sign-in; exchanging with it would fail with `invalid_grant`,
		// which discards the stored tokens.
		state, err := options.Store.Load(ctx)
		if err != nil {
			return struct{}{}, err
		}
		if state == nil || state.OAuthState != authorization.State {
			return struct{}{}, errors.New("Another sign-in to this MCP server started since; sign in again")
		}
		provider, err := createOAuthClientProvider(options.McpOAuthClientOptions, options.RedirectURL, nil)
		if err != nil {
			return struct{}{}, err
		}
		flow := flowOptions(options.McpSignInOptions)
		// Saved with the tokens when the token response names no scope, so it must be the requested one.
		flow.Scope = authorization.Scope
		flow.AuthorizationRedirectURL = &authorization.RedirectURL
		flow.AuthorizationCode = response.Code
		if response.Iss != "" {
			flow.Iss = &response.Iss
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
		Fetch:                          timedFetch(options.Fetch),
		AuthorizationServerMetadataURL: options.Settings.AuthServerMetadataURL,
	}
	if options.Challenge != nil {
		flow.ResourceMetadataURL = options.Challenge.ResourceMetadataURL
	}
	return flow
}

// cancellable runs fn, reporting an end of ctx as [McpSignInCancelledError].
func cancellable[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	var zero T
	if ctx.Err() != nil {
		return zero, &McpSignInCancelledError{}
	}
	value, err := fn()
	// Aborted requests fail with the context's cause; report them as the cancellation they are.
	if err != nil && ctx.Err() != nil {
		return zero, &McpSignInCancelledError{}
	}
	return value, err
}
