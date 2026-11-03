package oauth

import (
	"context"
	"errors"
	"strconv"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// Ports packages/mcp/src/oauth/loopback.ts.
//
// How the authorization response of a sign-in reaches a local application: a loopback callback server the browser
// redirects to, or a redirect URL the user pastes from the browser address bar.

// McpLoopbackCallback is a loopback callback server and the redirect URI it serves.
type McpLoopbackCallback struct {
	Callback *OAuthCallbackServer
	// RedirectURL is passed to [StartMcpSignIn] and [CompleteMcpSignIn].
	RedirectURL string
}

// ListenForLoopbackSignInOptions configure [ListenForLoopbackSignIn].
type ListenForLoopbackSignInOptions struct {
	ServerURL string
	Store     McpOAuthStateStore
	Settings  McpOAuthSettings
	// ClientMetadataDocuments are the sign-in's documents, so the callback server also serves the server-specific
	// redirect URI.
	ClientMetadataDocuments *McpClientMetadataDocuments
	// RenderPage renders the browser page after the redirect. Default: plain text.
	RenderPage func(OAuthCallbackPage) string
}

// ListenForLoopbackSignIn listens for the authorization response of a sign-in on loopback, where Settings say:
// `callbackUrl` and `callbackPort`, else the port of the stored client's redirect URI (so that client stays valid) or a
// free one. Close the callback server when the sign-in ends.
func ListenForLoopbackSignIn(ctx context.Context, options ListenForLoopbackSignInOptions) (*McpLoopbackCallback, error) {
	settings := options.Settings
	loopback, err := loopbackSettings(settings)
	if err != nil {
		return nil, err
	}
	stored, err := options.Store.Load(ctx)
	if err != nil {
		return nil, err
	}
	port := loopback.port
	if port == nil && stored != nil {
		if registered := registeredRedirectURLs(stored.ClientInformation); len(registered) > 0 {
			u, err := ParseURL(registered[0])
			if err != nil {
				return nil, err
			}
			// `Number(url.port) || undefined`
			//portlint:allow numbers a parsed URL's port is only ASCII digits, which Atoi and Number read the same; an empty port is 0 in both
			if p, err := strconv.Atoi(u.Port); err == nil && p != 0 {
				port = &p
			}
		}
	}
	listen := OAuthCallbackServerOptions{
		Host: loopback.host, RedirectHost: loopback.redirectHost, Path: loopback.path, RenderPage: options.RenderPage,
	}
	// The redirect URI of a server-specific Client ID Metadata Document.
	if offersClientMetadataDocument(settings, options.ClientMetadataDocuments, loopback.url) {
		path, err := serverSpecificPath(loopback.path, options.ServerURL)
		if err != nil {
			return nil, err
		}
		listen.ExtraPaths = []string{path}
	}
	if port != nil {
		listen.Port = *port
	}
	callback, err := ListenOAuthCallbackServer(listen)
	if err != nil {
		// A configured port is required; the stored client's port is only preferred.
		if loopback.port != nil || port == nil {
			return nil, err
		}
		listen.Port = 0
		if callback, err = ListenOAuthCallbackServer(listen); err != nil {
			return nil, err
		}
	}
	redirectURL := loopback.fixedRedirectURL
	if redirectURL == "" {
		redirectURL = callback.RedirectURL
	}
	return &McpLoopbackCallback{Callback: callback, RedirectURL: redirectURL}, nil
}

// WaitForLoopbackSignInOptions configure [WaitForLoopbackSignIn].
type WaitForLoopbackSignInOptions struct {
	Callback *OAuthCallbackServer
	// Authorization is the "redirect" result of [StartMcpSignIn].
	Authorization McpSignInRedirect
	// PromptForRedirectURL asks for the redirect URL, for when the browser cannot reach the loopback callback (for
	// example over SSH). Its context ends once the callback arrives or the sign-in is cancelled. An empty string
	// cancels the sign-in.
	PromptForRedirectURL func(ctx context.Context) (string, error)
	// OnPending runs once the wait for the browser callback is registered and before the prompt starts: the moment a
	// caller shows the authorization URL, so a browser that follows the redirect at once reaches a pending wait.
	OnPending func()
}

type loopbackResponse struct {
	response McpAuthorizationResponse
	err      error
}

// WaitForLoopbackSignIn waits for the authorization response on a loopback callback server, or for a redirect URL the
// user pastes from the browser address bar, whichever comes first. Ending ctx stops the wait with
// [McpSignInCancelledError].
func WaitForLoopbackSignIn(ctx context.Context, options WaitForLoopbackSignInOptions) (McpAuthorizationResponse, error) {
	return cancellable(ctx, func() (McpAuthorizationResponse, error) {
		authorization := options.Authorization
		redirect, err := ParseURL(authorization.RedirectURL)
		if err != nil {
			return McpAuthorizationResponse{}, err
		}
		waitCtx, stop := context.WithCancel(ctx)
		// The losing side ends once the prompt's context ends or the callback server closes.
		defer stop()
		registered := make(chan struct{})
		fromBrowser := make(chan loopbackResponse, 1)
		go func() {
			response, err := options.Callback.WaitForCallback(WithCallbackRegistered(waitCtx, func() { close(registered) }), authorization.State, redirect.Pathname)
			fromBrowser <- loopbackResponse{response, err}
		}()
		select {
		case <-registered:
		case r := <-fromBrowser:
			return r.response, r.err
		}
		if options.OnPending != nil {
			options.OnPending()
		}
		fromUser := make(chan loopbackResponse, 1)
		if options.PromptForRedirectURL != nil {
			go func() {
				input, err := options.PromptForRedirectURL(waitCtx)
				if err != nil {
					fromUser <- loopbackResponse{err: err}
					return
				}
				if jsstring.Trim(input) == "" {
					fromUser <- loopbackResponse{err: &McpSignInCancelledError{}}
					return
				}
				response, err := ResponseFromRedirectURL(input, authorization)
				fromUser <- loopbackResponse{response, err}
			}()
		}
		select {
		case r := <-fromBrowser:
			return r.response, r.err
		case r := <-fromUser:
			return r.response, r.err
		case <-ctx.Done():
			return McpAuthorizationResponse{}, context.Cause(ctx)
		}
	})
}

// ResponseFromRedirectURL is the authorization response in a redirect URL the user pasted from the browser address bar.
// It fails when the URL is not on the sign-in's redirect URI, carries another `state`, or reports an error.
func ResponseFromRedirectURL(input string, authorization McpSignInRedirect) (McpAuthorizationResponse, error) {
	u, err := nodeurl.ParseURL(jsstring.Trim(input))
	if err != nil {
		return McpAuthorizationResponse{}, errors.New("Expected the full redirect URL from the browser address bar")
	}
	redirect, err := nodeurl.ParseURL(authorization.RedirectURL)
	if err != nil {
		return McpAuthorizationResponse{}, err
	}
	// A server-specific redirect URI tells authorization servers apart, so it must match exactly.
	if u.Origin() != redirect.Origin() || u.Pathname != redirect.Pathname {
		return McpAuthorizationResponse{}, errors.New("The redirect URL does not match this sign-in's redirect URI")
	}
	query := nodeurl.SearchPairs(u.Query)
	get := func(name string) (string, bool) {
		for _, pair := range query {
			if pair[0] == name {
				return pair[1], true
			}
		}
		return "", false
	}
	if failure, _ := get("error"); failure != "" {
		if description, ok := get("error_description"); ok {
			return McpAuthorizationResponse{}, errors.New(description)
		}
		return McpAuthorizationResponse{}, errors.New(failure)
	}
	if state, _ := get("state"); state != authorization.State {
		return McpAuthorizationResponse{}, errors.New("The redirect URL belongs to a different sign-in")
	}
	code, _ := get("code")
	if code == "" {
		return McpAuthorizationResponse{}, errors.New("The redirect URL does not contain an authorization code")
	}
	iss, _ := get("iss")
	return McpAuthorizationResponse{Code: code, State: authorization.State, Iss: iss}, nil
}
