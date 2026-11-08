package oauth

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/mcp"
)

// Ports packages/mcp/src/oauth/client-settings.ts (Pi bb266bf35).
//
// The OAuth client that sign-in and refreshes use for one MCP server: its settings, how it identifies itself (a pre-registered client, a Client
// ID Metadata Document, or dynamic client registration), and the [McpOAuthProvider] built from them.

// McpOAuthSettings are the client settings of one MCP server, usually from the application's configuration.
type McpOAuthSettings struct {
	// ClientID is the pre-registered client id. Without it, sign-in uses a Client ID Metadata Document or registers a client.
	ClientID string
	// ClientSecret is already resolved.
	ClientSecret string
	// CallbackPort is the port of the loopback callback server, for clients registered with a fixed redirect URI. Without CallbackURL, the
	// redirect URI is `http://127.0.0.1:<port>/callback`. Only [ListenForLoopbackSignIn] and the redirect URI of refreshes use it;
	// [StartMcpSignIn] and [CompleteMcpSignIn] take theirs from the caller.
	CallbackPort *int
	// CallbackURL is the loopback redirect URI registered for ClientID, for example `http://localhost:8080/oauth/callback`. Without a port, the
	// callback server listens on CallbackPort or a free port, which is added to the URI (RFC 8252). Default: `http://127.0.0.1/callback`.
	// Like CallbackPort, only [ListenForLoopbackSignIn] and refreshes use it.
	CallbackURL string
	// Scope lists the scopes to request, separated by spaces. Default: the scopes the server advertises.
	Scope string
	// ClientName is the `client_name` for dynamic client registration. Default: the application's client name.
	ClientName string
	// ClientRegistration is how to identify without ClientID. `auto` (default): a Client ID Metadata Document when the authorization server
	// supports them for public clients and the documents list the redirect URI, otherwise dynamic client registration; with ClientName, always
	// dynamic client registration. `cimd`: always the document, and sign-in fails without support. `dcr`: always dynamic client registration.
	ClientRegistration string
	// AuthServerMetadataURL is the authorization server metadata document to use instead of discovery through the server, for servers that
	// advertise a wrong authorization server or none. It is trusted as configured.
	AuthServerMetadataURL *URL
}

// McpClientMetadataDocuments are the Client ID Metadata Documents an application publishes: `<BaseURL>/client.json`, and for each MCP server
// `<BaseURL>/<callback ID>/client.json`, whose redirect URIs carry the path `<path>/<callback ID>`.
type McpClientMetadataDocuments struct {
	// BaseURL is for example `https://pi.dev/oauth`.
	BaseURL string
	// ListsRedirectURL reports whether the documents list redirectURL. Loopback redirect URIs match on any port (RFC 8252).
	ListsRedirectURL func(redirectURL string) bool
}

// McpOAuthServerStore is the state store of one MCP server, with a lock that keeps other processes from refreshing at the same time.
type McpOAuthServerStore interface {
	McpOAuthStateStore
	// WithRefreshLock runs fn while no other process refreshes the server's tokens. Not reentrant: [StartMcpSignIn] and
	// [CreateMcpAuthProvider] take it themselves, so never call them from fn. Return [McpRefreshLockBusyError] when another process holds the
	// lock for the whole wait: [StartMcpSignIn] then goes to the browser step. Any other error fails the sign-in or refresh.
	WithRefreshLock(ctx context.Context, fn func(ctx context.Context) error) error
}

// McpOAuthClientOptions are the OAuth client of one MCP server: where its state lives, its settings, and the application's identity.
type McpOAuthClientOptions struct {
	ServerURL string
	Store     McpOAuthServerStore
	Settings  McpOAuthSettings
	// ClientName is the `client_name` for dynamic client registration unless Settings.ClientName names another: the application.
	ClientName string
	// ClientMetadataDocuments is nil for dynamic client registration only.
	ClientMetadataDocuments *McpClientMetadataDocuments
	// Fetch sends the requests to the authorization server. Go mechanic: Pi uses the global fetch; nil is [http.DefaultClient].
	Fetch mcp.McpFetch
}

// CreateOAuthClientProvider returns an [McpOAuthProvider] for the client, redirecting to redirectURL or the document's variant of it.
func CreateOAuthClientProvider(options McpOAuthClientOptions, redirectURL string, onRedirect func(URL)) (*McpOAuthProvider, error) {
	settings, documents := options.Settings, options.ClientMetadataDocuments
	var document func(*AuthorizationServerMetadata) (*OAuthClientMetadataDocument, error)
	if OffersClientMetadataDocument(settings, documents, redirectURL) {
		document = func(metadata *AuthorizationServerMetadata) (*OAuthClientMetadataDocument, error) {
			if documents == nil || documents.ListsRedirectURL == nil || !documents.ListsRedirectURL(redirectURL) {
				return nil, fmt.Errorf(`oauth.clientRegistration "cimd" needs Client ID Metadata Documents that list the redirect URI %s`, redirectURL)
			}
			doc, err := clientMetadataDocument(options.ServerURL, redirectURL, metadata, documents.BaseURL)
			if err == nil && doc == nil && settings.ClientRegistration == "cimd" {
				return nil, errors.New(`The authorization server does not support Client ID Metadata Documents for public clients; remove oauth.clientRegistration "cimd"`)
			}
			return doc, err
		}
	}
	if onRedirect == nil {
		onRedirect = func(URL) {}
	}
	return NewMcpOAuthProvider(McpOAuthProviderOptions{
		ServerURL:              options.ServerURL,
		RedirectURL:            redirectURL,
		ClientMetadata:         OAuthClientMetadata{ClientName: cmp.Or(settings.ClientName, options.ClientName)},
		ClientMetadataDocument: document,
		ClientID:               settings.ClientID,
		ClientSecret:           settings.ClientSecret,
		Store:                  options.Store,
		OnRedirect: func(_ context.Context, u URL) error {
			onRedirect(u)
			return nil
		},
	})
}

// OffersClientMetadataDocument reports whether the client may identify with a Client ID Metadata Document instead of registering. `auto`
// registers when the user asked for a client name or the documents do not list the redirect URI.
func OffersClientMetadataDocument(settings McpOAuthSettings, documents *McpClientMetadataDocuments, redirectURL string) bool {
	registration := cmp.Or(settings.ClientRegistration, "auto")
	if registration != "auto" {
		return registration == "cimd"
	}
	return settings.ClientID == "" && settings.ClientName == "" && documents != nil && documents.ListsRedirectURL != nil && documents.ListsRedirectURL(redirectURL)
}

// clientMetadataDocument is the Client ID Metadata Document to use, chosen like Codex chooses its own, or nil when the authorization server does
// not support documents for public clients. Without the `iss` parameter in authorization responses (RFC 9207), the redirect URI and the
// document are specific to the MCP server, so a response cannot be mixed up with one from another authorization server (RFC 9700 section 4.4.2.2).
func clientMetadataDocument(serverURL, redirectURL string, metadata *AuthorizationServerMetadata, baseURL string) (*OAuthClientMetadataDocument, error) {
	if metadata == nil || metadata.ClientIDMetadataDocumentSupported == nil || !*metadata.ClientIDMetadataDocumentSupported ||
		!slices.Contains(metadata.TokenEndpointAuthMethodsSupported, "none") {
		return nil, nil
	}
	if metadata.AuthorizationResponseIssParameterSupported != nil && *metadata.AuthorizationResponseIssParameterSupported {
		return &OAuthClientMetadataDocument{URL: baseURL + "/client.json", RedirectURL: redirectURL}, nil
	}
	id, err := callbackID(serverURL)
	if err != nil {
		return nil, err
	}
	redirect, err := ParseURL(redirectURL)
	if err != nil {
		return nil, fmt.Errorf("Invalid URL: %s", redirectURL)
	}
	path, err := ServerSpecificPath(redirect.Pathname, serverURL)
	if err != nil {
		return nil, err
	}
	return &OAuthClientMetadataDocument{URL: baseURL + "/" + id + "/client.json", RedirectURL: redirect.WithPathname(path)}, nil
}

// ServerSpecificPath is the path of the redirect URI of a server-specific Client ID Metadata Document: `<path>/<callback ID>`.
func ServerSpecificPath(path, serverURL string) (string, error) {
	id, err := callbackID(serverURL)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(path, "/") + "/" + id, nil
}

// callbackID is 12 characters identifying an MCP server URL in callback paths, computed like Codex does.
func callbackID(serverURL string) (string, error) {
	u, err := ParseURL(serverURL)
	if err != nil {
		return "", fmt.Errorf("Invalid URL: %s", serverURL)
	}
	sum := sha256.Sum256([]byte(u.WithoutHash().Href()))
	return base64.RawURLEncoding.EncodeToString(sum[:9]), nil
}

// RegisteredRedirectURLs are the redirect URIs a stored client was registered with.
func RegisteredRedirectURLs(client *OAuthClientInformationMixed) []string {
	if client == nil {
		return nil
	}
	return client.RedirectURIs
}

// oauthRequestTimeout bounds each request to the authorization server, so an unresponsive one cannot hold the refresh lock, delay shutdown, or
// keep a sign-in waiting for long.
const oauthRequestTimeout = 15 * time.Second

// TimedFetch is fetch with a 15 s timeout per request, on top of the request's own context. A nil fetch is [http.DefaultClient].
func TimedFetch(fetch mcp.McpFetch) mcp.McpFetch {
	return timeoutFetch{inner: fetchOrDefault(fetch), timeout: oauthRequestTimeout}
}

type timeoutFetch struct {
	inner   mcp.McpFetch
	timeout time.Duration
}

func (t timeoutFetch) Do(request *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(request.Context(), t.timeout)
	response, err := t.inner.Do(request.WithContext(ctx))
	if err != nil {
		cancel()
		return nil, err
	}
	response.Body = &cancelOnClose{ReadCloser: response.Body, cancel: cancel}
	return response, nil
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelOnClose) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

const (
	callbackHost = "127.0.0.1"
	callbackPath = "/callback"
	// DefaultRedirectURL is the redirect URI when the settings name none and no client is stored.
	DefaultRedirectURL = "http://" + callbackHost + callbackPath
)

// LoopbackSettings is where the loopback callback server listens and the redirect URI it serves, from CallbackURL and CallbackPort.
type LoopbackSettings struct {
	// URL is the redirect URI, without the port unless one is configured.
	URL string
	// Host is the address to listen on.
	Host string
	// RedirectHost is the host name in the redirect URI.
	RedirectHost string
	Port         *int
	Path         string
	// FixedRedirectURL is the exact redirect URI when the port is fixed, or "".
	FixedRedirectURL string
}

// GetLoopbackSettings returns the loopback callback settings of settings.
func GetLoopbackSettings(settings McpOAuthSettings) (LoopbackSettings, error) {
	configured := cmp.Or(settings.CallbackURL, DefaultRedirectURL)
	u, err := ParseURL(configured)
	if err != nil {
		return LoopbackSettings{}, err
	}
	address := strings.TrimSuffix(strings.TrimPrefix(u.Hostname, "["), "]")
	var port *int
	fixed := ""
	if u.Port != "" {
		p, _ := strconv.Atoi(u.Port)
		port = &p
		// A configured URI with a port is sent exactly as written, since servers compare it as a string.
		fixed = settings.CallbackURL
	} else if settings.CallbackPort != nil {
		port = settings.CallbackPort
		fixed = u.WithPort(strconv.Itoa(*port)).Href()
	}
	host := address
	// `localhost` is served on 127.0.0.1; browsers fall back to it when ::1 refuses.
	if address == "localhost" {
		host = callbackHost
	}
	return LoopbackSettings{URL: configured, Host: host, RedirectHost: address, Port: port, Path: u.Pathname, FixedRedirectURL: fixed}, nil
}
