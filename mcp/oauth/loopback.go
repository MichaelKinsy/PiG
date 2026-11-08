package oauth

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// Ports packages/mcp/src/oauth/loopback.ts (Pi bb266bf35).
//
// How the authorization response of a sign-in reaches a local application: a loopback callback server the browser redirects to, or a redirect
// URL the user pastes from the browser address bar.

// McpLoopbackCallback is a loopback callback server and the redirect URI it serves.
type McpLoopbackCallback struct {
	Callback *OAuthCallbackServer
	// RedirectURL is passed to [StartMcpSignIn] and [CompleteMcpSignIn].
	RedirectURL string
}

// ListenForLoopbackSignInOptions are the inputs of [ListenForLoopbackSignIn].
type ListenForLoopbackSignInOptions struct {
	ServerURL               string
	Store                   McpOAuthStateStore
	Settings                McpOAuthSettings
	ClientMetadataDocuments *McpClientMetadataDocuments
	// RenderPage renders the browser page after the redirect. Default: plain text.
	RenderPage func(OAuthCallbackPage) string
}

// ListenForLoopbackSignIn listens for the authorization response of a sign-in on loopback, where the settings say: CallbackURL and CallbackPort,
// else the port of the stored client's redirect URI (so that client stays valid) or a free one. Pass the sign-in's ClientMetadataDocuments, so
// the callback server also serves the server-specific redirect URI. Close the callback server when the sign-in ends.
func ListenForLoopbackSignIn(ctx context.Context, options ListenForLoopbackSignInOptions) (McpLoopbackCallback, error) {
	loopbackSettings, err := GetLoopbackSettings(options.Settings)
	if err != nil {
		return McpLoopbackCallback{}, err
	}
	stored, err := options.Store.Load(ctx)
	if err != nil {
		return McpLoopbackCallback{}, err
	}
	port := loopbackSettings.Port
	if port == nil && stored != nil {
		if registered := RegisteredRedirectURLs(stored.ClientInformation); len(registered) > 0 {
			if u, err := ParseURL(registered[0]); err == nil {
				if p, err := strconv.Atoi(u.Port); err == nil && p != 0 {
					port = &p
				}
			}
		}
	}
	var extraPaths []string
	// The redirect URI of a server-specific Client ID Metadata Document.
	if OffersClientMetadataDocument(options.Settings, options.ClientMetadataDocuments, loopbackSettings.URL) {
		path, err := ServerSpecificPath(loopbackSettings.Path, options.ServerURL)
		if err != nil {
			return McpLoopbackCallback{}, err
		}
		extraPaths = []string{path}
	}
	listen := OAuthCallbackServerOptions{
		Host: loopbackSettings.Host, RedirectHost: loopbackSettings.RedirectHost, Path: loopbackSettings.Path,
		ExtraPaths: extraPaths, RenderPage: options.RenderPage,
	}
	if port != nil {
		listen.Port = *port
	}
	callback, err := ListenOAuthCallbackServer(listen)
	if err != nil {
		// A configured port is required; the stored client's port is only preferred.
		if loopbackSettings.Port != nil || port == nil {
			return McpLoopbackCallback{}, err
		}
		listen.Port = 0
		if callback, err = ListenOAuthCallbackServer(listen); err != nil {
			return McpLoopbackCallback{}, err
		}
	}
	redirectURL := loopbackSettings.FixedRedirectURL
	if redirectURL == "" {
		redirectURL = callback.RedirectURL
	}
	return McpLoopbackCallback{Callback: callback, RedirectURL: redirectURL}, nil
}

// WaitForLoopbackSignInOptions are the inputs of [WaitForLoopbackSignIn].
type WaitForLoopbackSignInOptions struct {
	Callback *OAuthCallbackServer
	// Authorization is the `redirect` result of [StartMcpSignIn].
	Authorization McpSignInRedirect
	// OnRegistered is called once the callback server waits for the authorization response, and before the redirect URL is asked for. Go
	// mechanic: Pi registers the `state` in the same tick as it returns the promise, so the caller shows the authorization URL after the call;
	// Go's caller waits on its own goroutine, and a browser that follows the URL at once must reach a registered wait.
	OnRegistered func()
	// PromptForRedirectURL asks for the redirect URL, for when the browser cannot reach the loopback callback (for example over SSH). Its
	// context ends once the callback arrives or the sign-in is cancelled. Returning "" cancels the sign-in. Nil: no prompt.
	PromptForRedirectURL func(ctx context.Context) (string, error)
}

type responseResult struct {
	response McpAuthorizationResponse
	err      error
}

// WaitForLoopbackSignIn waits for the authorization response on a loopback callback server, or for a redirect URL the user pastes from the
// browser address bar, whichever comes first. Ending ctx stops the wait with [McpSignInCancelledError].
func WaitForLoopbackSignIn(ctx context.Context, options WaitForLoopbackSignInOptions) (McpAuthorizationResponse, error) {
	return cancellable(ctx, func() (McpAuthorizationResponse, error) {
		authorization := options.Authorization
		redirect, err := ParseURL(authorization.RedirectURL)
		if err != nil {
			return McpAuthorizationResponse{}, err
		}
		waitCtx, cancelWait := context.WithCancel(ctx)
		defer cancelWait()
		registered := make(chan struct{})
		fromBrowser := make(chan responseResult, 1)
		go func() {
			received, err := options.Callback.WaitForCallback(WithCallbackRegistered(waitCtx, func() { close(registered) }), authorization.State, redirect.Pathname)
			fromBrowser <- responseResult{received, err}
		}()
		select {
		case <-registered:
		case r := <-fromBrowser:
			return r.response, r.err
		}
		if options.OnRegistered != nil {
			options.OnRegistered()
		}
		var fromUser chan responseResult
		if options.PromptForRedirectURL != nil {
			fromUser = make(chan responseResult, 1)
			go func() {
				input, err := options.PromptForRedirectURL(waitCtx)
				if err != nil {
					fromUser <- responseResult{err: err}
					return
				}
				if strings.TrimFunc(input, jsstring.IsSpace) == "" {
					fromUser <- responseResult{err: NewMcpSignInCancelledError()}
					return
				}
				response, err := ResponseFromRedirectURL(input, authorization)
				fromUser <- responseResult{response, err}
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

// ResponseFromRedirectURL returns the authorization response in a redirect URL the user pasted from the browser address bar. It fails when the
// URL is not on the sign-in's redirect URI, carries another `state`, or reports an error.
func ResponseFromRedirectURL(input string, authorization McpSignInRedirect) (McpAuthorizationResponse, error) {
	u, err := nodeurl.ParseURL(strings.TrimFunc(input, jsstring.IsSpace))
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
	query := nodeurl.SearchParams(u.Query)
	if failure := query.Get("error"); failure != "" {
		if query.Has("error_description") {
			return McpAuthorizationResponse{}, errors.New(query.Get("error_description"))
		}
		return McpAuthorizationResponse{}, errors.New(failure)
	}
	state := query.Get("state")
	if !query.Has("state") || state != authorization.State {
		return McpAuthorizationResponse{}, errors.New("The redirect URL belongs to a different sign-in")
	}
	code := query.Get("code")
	if code == "" {
		return McpAuthorizationResponse{}, errors.New("The redirect URL does not contain an authorization code")
	}
	return McpAuthorizationResponse{Code: code, State: state, Iss: query.Get("iss")}, nil
}
