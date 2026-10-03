package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/MichaelKinsy/PiG/mcp"
)

// Ports packages/mcp/src/oauth/discovery.ts.

func discard(response *http.Response) {
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
}

// isDiscoveryMiss: 4xx and 502 mean "not here", so discovery tries the next candidate URL.
func isDiscoveryMiss(status int) bool {
	return (status >= 400 && status < 500) || status == 502
}

// pathSuffix is the path for `/.well-known/<kind><path>`; empty for the root path.
func pathSuffix(pathname string) string {
	return strings.TrimSuffix(pathname, "/")
}

func challengeField(header, name string) (string, bool) {
	re := regexp.MustCompile(`(?i)(?:^|[,\s])` + regexp.QuoteMeta(name) + `=(?:"([^"]*)"|([^\s,]+))`)
	match := re.FindStringSubmatch(header)
	if match == nil {
		return "", false
	}
	if match[1] != "" || strings.Contains(match[0], `"`) {
		return match[1], true
	}
	return match[2], true
}

// ParseWWWAuthenticate parses a Bearer or DPoP challenge. Any other scheme,
// or an empty header, gives an empty challenge.
func ParseWWWAuthenticate(header string) OAuthChallenge {
	if header == "" {
		return OAuthChallenge{}
	}
	fields := strings.Fields(strings.TrimLeft(header, " \t\r\n"))
	if len(fields) == 0 {
		return OAuthChallenge{}
	}
	if scheme := strings.ToLower(fields[0]); scheme != "bearer" && scheme != "dpop" {
		return OAuthChallenge{}
	}
	var challenge OAuthChallenge
	if raw, ok := challengeField(header, "resource_metadata"); ok && raw != "" {
		if u, err := parseURL(raw); err == nil {
			challenge.ResourceMetadataURL = u
		}
	}
	challenge.Scope, _ = challengeField(header, "scope")
	challenge.Error, _ = challengeField(header, "error")
	challenge.ErrorDescription, _ = challengeField(header, "error_description")
	return challenge
}

func fetchOrDefault(fetch mcp.McpFetch) mcp.McpFetch {
	if fetch == nil {
		return http.DefaultClient
	}
	return fetch
}

func fetchMetadata(ctx context.Context, u *url.URL, fetch mcp.McpFetch, protocolVersion string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("MCP-Protocol-Version", protocolVersion)
	return fetch.Do(request)
}

func readBody(response *http.Response) ([]byte, error) {
	defer func() { _ = response.Body.Close() }()
	return io.ReadAll(response.Body)
}

// isNetworkError is upstream's `error instanceof TypeError`: fetch reports
// network failures that way.
func isNetworkError(err error) bool {
	var urlErr *url.Error
	var netErr net.Error
	return errors.As(err, &urlErr) || errors.As(err, &netErr)
}

// DiscoveryOptions configure the discovery functions.
type DiscoveryOptions struct {
	ResourceMetadataURL string
	// AuthorizationServerMetadataURL is a metadata document that
	// [DiscoverOAuthServerInfo] uses instead of discovery. It is trusted as
	// configured, so its issuer is not checked.
	AuthorizationServerMetadataURL *url.URL
	ProtocolVersion                string
	Fetch                          mcp.McpFetch
	SkipIssuerValidation           bool
}

func (o DiscoveryOptions) version() string {
	if o.ProtocolVersion != "" {
		return o.ProtocolVersion
	}
	return mcp.LatestProtocolVersion
}

// DiscoverProtectedResourceMetadata loads the RFC 9728 metadata of a server.
func DiscoverProtectedResourceMetadata(ctx context.Context, serverURL string, options DiscoveryOptions) (*OAuthProtectedResourceMetadata, error) {
	server, err := parseURL(serverURL)
	if err != nil {
		return nil, err
	}
	fetch := fetchOrDefault(options.Fetch)
	version := options.version()
	origin := server.Scheme + "://" + server.Host
	first := origin + "/.well-known/oauth-protected-resource" + pathSuffix(server.Path)
	if options.ResourceMetadataURL != "" {
		first = options.ResourceMetadataURL
	}
	firstURL, err := parseURL(first)
	if err != nil {
		return nil, err
	}
	response, err := fetchMetadata(ctx, firstURL, fetch, version)
	if err != nil {
		return nil, err
	}
	if options.ResourceMetadataURL == "" && server.Path != "/" && isDiscoveryMiss(response.StatusCode) {
		discard(response)
		rootURL, _ := parseURL(origin + "/.well-known/oauth-protected-resource")
		//nolint:bodyclose // the body is closed by readBody or discard below
		if response, err = fetchMetadata(ctx, rootURL, fetch, version); err != nil {
			return nil, err
		}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		discard(response)
		return nil, fmt.Errorf("HTTP %d loading OAuth protected resource metadata", response.StatusCode)
	}
	body, err := readBody(response)
	if err != nil {
		return nil, err
	}
	if !json.Valid(body) {
		return nil, errors.New("Unexpected token in JSON")
	}
	return ParseProtectedResourceMetadata(body)
}

// DiscoveryURL is one candidate metadata URL and its kind.
type DiscoveryURL struct {
	URL  *url.URL
	Type string // "oauth" or "oidc"
}

// BuildAuthorizationServerDiscoveryURLs lists the candidate metadata URLs
// for an issuer, in the order to try them.
func BuildAuthorizationServerDiscoveryURLs(authorizationServerURL string) ([]DiscoveryURL, error) {
	issuer, err := parseURL(authorizationServerURL)
	if err != nil {
		return nil, err
	}
	origin := issuer.Scheme + "://" + issuer.Host
	path := pathSuffix(issuer.Path)
	build := func(p string) *url.URL {
		u, _ := parseURL(origin + p)
		return u
	}
	urls := []DiscoveryURL{
		{build("/.well-known/oauth-authorization-server" + path), "oauth"},
		{build("/.well-known/openid-configuration" + path), "oidc"},
	}
	if path != "" {
		urls = append(urls, DiscoveryURL{build(path + "/.well-known/openid-configuration"), "oidc"})
	}
	return urls, nil
}

// DiscoverAuthorizationServerMetadata loads RFC 8414 or OpenID metadata. It
// returns nil metadata when no candidate URL has any, and an
// [OAuthIssuerMismatchError] when the metadata's issuer differs from the URL.
func DiscoverAuthorizationServerMetadata(ctx context.Context, authorizationServerURL string, options DiscoveryOptions) (*AuthorizationServerMetadata, error) {
	fetch := fetchOrDefault(options.Fetch)
	candidates, err := BuildAuthorizationServerDiscoveryURLs(authorizationServerURL)
	if err != nil {
		return nil, err
	}
	for _, candidate := range candidates {
		response, err := fetchMetadata(ctx, candidate.URL, fetch, options.version())
		if err != nil {
			return nil, err
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			discard(response)
			if isDiscoveryMiss(response.StatusCode) {
				continue
			}
			return nil, fmt.Errorf("HTTP %d loading authorization server metadata from %s", response.StatusCode, candidate.URL)
		}
		body, err := readBody(response)
		if err != nil {
			return nil, err
		}
		if !json.Valid(body) {
			return nil, errors.New("Unexpected token in JSON")
		}
		metadata, err := ParseAuthorizationServerMetadata(body)
		if err != nil {
			return nil, err
		}
		if !options.SkipIssuerValidation {
			// URL parsing adds a trailing slash to bare origins, so compare without one on either side.
			if strings.TrimSuffix(metadata.Issuer, "/") != strings.TrimSuffix(authorizationServerURL, "/") {
				return nil, &OAuthIssuerMismatchError{Expected: authorizationServerURL, Received: &metadata.Issuer}
			}
		}
		return metadata, nil
	}
	return nil, nil
}

// DiscoverOAuthServerInfo discovers the protected resource metadata and the
// authorization server it names, falling back to the server's own origin.
func DiscoverOAuthServerInfo(ctx context.Context, serverURL string, options DiscoveryOptions) (*OAuthServerInfo, error) {
	resourceMetadata, err := DiscoverProtectedResourceMetadata(ctx, serverURL, DiscoveryOptions{
		ResourceMetadataURL: options.ResourceMetadataURL, Fetch: options.Fetch,
	})
	if err != nil && isNetworkError(err) {
		return nil, err
	}
	if err != nil {
		resourceMetadata = nil
	}
	if metadataURL := options.AuthorizationServerMetadataURL; metadataURL != nil {
		response, err := fetchMetadata(ctx, metadataURL, fetchOrDefault(options.Fetch), mcp.LatestProtocolVersion)
		if err != nil {
			return nil, err
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			discard(response)
			return nil, fmt.Errorf("HTTP %d loading authorization server metadata from %s", response.StatusCode, metadataURL)
		}
		body, err := readBody(response)
		if err != nil {
			return nil, err
		}
		if !json.Valid(body) {
			return nil, errors.New("Unexpected token in JSON")
		}
		metadata, err := ParseAuthorizationServerMetadata(body)
		if err != nil {
			return nil, err
		}
		return &OAuthServerInfo{
			AuthorizationServerURL: metadata.Issuer, AuthorizationServerMetadata: metadata, ResourceMetadata: resourceMetadata,
		}, nil
	}
	authorizationServerURL := ""
	if resourceMetadata != nil && len(resourceMetadata.AuthorizationServers) > 0 {
		authorizationServerURL = resourceMetadata.AuthorizationServers[0]
	} else {
		server, err := parseURL(serverURL)
		if err != nil {
			return nil, err
		}
		authorizationServerURL = server.Scheme + "://" + server.Host + "/"
	}
	metadata, err := DiscoverAuthorizationServerMetadata(ctx, authorizationServerURL, DiscoveryOptions{
		Fetch: options.Fetch, SkipIssuerValidation: options.SkipIssuerValidation,
	})
	if err != nil {
		return nil, err
	}
	return &OAuthServerInfo{
		AuthorizationServerURL: authorizationServerURL, AuthorizationServerMetadata: metadata, ResourceMetadata: resourceMetadata,
	}, nil
}

// ResourceURLFromServerURL is the server URL without its fragment.
func ResourceURLFromServerURL(value string) (*url.URL, error) {
	u, err := parseURL(value)
	if err != nil {
		return nil, err
	}
	u.Fragment = ""
	u.RawFragment = ""
	return u, nil
}

// SelectResource returns the protected resource identifier to send as the
// `resource` parameter, or "" without metadata. It fails when the metadata
// names a resource that does not cover the server URL.
func SelectResource(serverURL string, metadata *OAuthProtectedResourceMetadata) (string, error) {
	if metadata == nil {
		return "", nil
	}
	requested, err := ResourceURLFromServerURL(serverURL)
	if err != nil {
		return "", err
	}
	configured, err := parseURL(metadata.Resource)
	if err != nil {
		return "", err
	}
	mismatch := fmt.Errorf("Protected resource %s does not match MCP server %s", metadata.Resource, requested)
	if requested.Scheme != configured.Scheme || requested.Host != configured.Host {
		return "", mismatch
	}
	withSlash := func(p string) string {
		if strings.HasSuffix(p, "/") {
			return p
		}
		return p + "/"
	}
	if !strings.HasPrefix(withSlash(requested.Path), withSlash(configured.Path)) {
		return "", mismatch
	}
	return metadata.Resource, nil
}
