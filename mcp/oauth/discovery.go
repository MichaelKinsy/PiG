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

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	_ "github.com/MichaelKinsy/PiG/internal/tlsdistrust" // hardens http.DefaultClient (pig additive (D191))
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

// challengeFieldPattern is `(?:^|[,\s])name=(?:"([^"]*)"|([^\s,]+))` with the `i` flag of a non-unicode JavaScript
// RegExp, which folds ASCII letters only.
func challengeFieldPattern(name string) *regexp.Regexp {
	var folded strings.Builder
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			folded.WriteString("[" + strings.ToLower(string(c)) + strings.ToUpper(string(c)) + "]")
		} else {
			folded.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return regexp.MustCompile(`(?:^|[,` + jsstring.SpaceClass + `])` + folded.String() + `=(?:"([^"]*)"|([^` + jsstring.SpaceClass + `,]+))`)
}

// challengeField is upstream's `field`: the first match's quoted or bare value. An empty value (`scope=""`) carries no
// information, so it counts as absent.
func challengeField(header, name string) (string, bool) {
	match := challengeFieldPattern(name).FindStringSubmatch(header)
	if match == nil {
		return "", false
	}
	if match[1] != "" {
		return match[1], true
	}
	return match[2], match[2] != ""
}

// ParseWWWAuthenticate parses a Bearer or DPoP challenge. Any other scheme,
// or an empty header, gives an empty challenge.
func ParseWWWAuthenticate(header string) OAuthChallenge {
	if header == "" {
		return OAuthChallenge{}
	}
	scheme := strings.ToLower(firstWord(header))
	if scheme != "bearer" && scheme != "dpop" {
		return OAuthChallenge{}
	}
	var challenge OAuthChallenge
	if raw, ok := challengeField(header, "resource_metadata"); ok && raw != "" {
		if u, err := ParseURL(raw); err == nil {
			challenge.ResourceMetadataURL = &u
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

func fetchMetadata(ctx context.Context, u URL, fetch mcp.McpFetch, protocolVersion string) (*http.Response, error) {
	request, err := newFetchRequest(ctx, http.MethodGet, u, nil)
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
	AuthorizationServerMetadataURL *URL
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
	server, err := ParseURL(serverURL)
	if err != nil {
		return nil, err
	}
	fetch := fetchOrDefault(options.Fetch)
	version := options.version()
	var firstURL URL
	if options.ResourceMetadataURL != "" {
		firstURL, err = ParseURL(options.ResourceMetadataURL)
	} else {
		firstURL, err = resolveAtOrigin("/.well-known/oauth-protected-resource"+pathSuffix(server.Pathname), server)
	}
	if err != nil {
		return nil, err
	}
	response, err := fetchMetadata(ctx, firstURL, fetch, version)
	if err != nil {
		return nil, err
	}
	if options.ResourceMetadataURL == "" && server.Pathname != "/" && isDiscoveryMiss(response.StatusCode) {
		discard(response)
		rootURL, err := resolveAtOrigin("/.well-known/oauth-protected-resource", server)
		if err != nil {
			return nil, err
		}
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
	URL  URL
	Type string // "oauth" or "oidc"
}

// BuildAuthorizationServerDiscoveryURLs lists the candidate metadata URLs
// for an issuer, in the order to try them.
func BuildAuthorizationServerDiscoveryURLs(authorizationServerURL string) ([]DiscoveryURL, error) {
	issuer, err := ParseURL(authorizationServerURL)
	if err != nil {
		return nil, err
	}
	path := pathSuffix(issuer.Pathname)
	var urls []DiscoveryURL
	add := func(p, kind string) error {
		u, err := resolveAtOrigin(p, issuer)
		if err != nil {
			return err
		}
		urls = append(urls, DiscoveryURL{u, kind})
		return nil
	}
	if err := add("/.well-known/oauth-authorization-server"+path, "oauth"); err != nil {
		return nil, err
	}
	if err := add("/.well-known/openid-configuration"+path, "oidc"); err != nil {
		return nil, err
	}
	if path != "" {
		if err := add(path+"/.well-known/openid-configuration", "oidc"); err != nil {
			return nil, err
		}
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
			return nil, fmt.Errorf("HTTP %d loading authorization server metadata from %s", response.StatusCode, candidate.URL.Href())
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
				return nil, NewOAuthIssuerMismatchError(&authorizationServerURL, &metadata.Issuer)
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
		response, err := fetchMetadata(ctx, *metadataURL, fetchOrDefault(options.Fetch), mcp.LatestProtocolVersion)
		if err != nil {
			return nil, err
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			discard(response)
			return nil, fmt.Errorf("HTTP %d loading authorization server metadata from %s", response.StatusCode, metadataURL.Href())
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
		server, err := ParseURL(serverURL)
		if err != nil {
			return nil, err
		}
		root, err := resolveURL("/", server)
		if err != nil {
			return nil, err
		}
		authorizationServerURL = root.Href()
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
func ResourceURLFromServerURL(value string) (URL, error) {
	u, err := ParseURL(value)
	if err != nil {
		return URL{}, err
	}
	return u.WithoutFragment(), nil
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
	configured, err := ParseURL(metadata.Resource)
	if err != nil {
		return "", err
	}
	mismatch := fmt.Errorf("Protected resource %s does not match MCP server %s", metadata.Resource, requested)
	// Origins compare as Pi compares them: every scheme but http, https, ws, wss and ftp has the origin "null".
	if requested.Origin() != configured.Origin() {
		return "", mismatch
	}
	withSlash := func(p string) string {
		if strings.HasSuffix(p, "/") {
			return p
		}
		return p + "/"
	}
	if !strings.HasPrefix(withSlash(requested.Pathname), withSlash(configured.Pathname)) {
		return "", mismatch
	}
	return metadata.Resource, nil
}

// firstWord is header.trimStart().split(/\s+/, 1)[0].
func firstWord(header string) string {
	trimmed := jsstring.TrimStart(header)
	if end := strings.IndexFunc(trimmed, jsstring.IsSpace); end >= 0 {
		return trimmed[:end]
	}
	return trimmed
}
