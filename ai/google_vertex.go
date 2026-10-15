//go:build !pig_strip_google_vertex

package ai

// Ports packages/ai/src/api/google-vertex.ts.

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

type googleVertexProvider struct {
	cfg         GoogleVertexConfig
	client      *http.Client
	accessToken func(context.Context, ProviderEnv) (string, error)
}

// NewGoogleVertexProvider creates a Vertex provider. Placeholder keys select ADC; real keys select Vertex express mode without project or location.
func NewGoogleVertexProvider(cfg GoogleVertexConfig) Provider {
	if cfg.ProviderID == "" {
		cfg.ProviderID = string(APIGoogleVertex)
	}
	return &googleVertexProvider{cfg: cfg, client: streamingHTTPClient(), accessToken: vertexAccessToken}
}

// NewGoogleVertexAPIProvider builds the google-vertex provider for cfg, the provider every model-to-provider factory uses.
func NewGoogleVertexAPIProvider(cfg GoogleVertexConfig) (Provider, error) {
	// pig additive (D92): a Piglet's strip.apis disables Vertex at runtime the way a pig_strip_google_vertex build compiles it out.
	if pigstrip.Has(pigstrip.ListAPIs, string(APIGoogleVertex)) {
		return nil, strippedGoogleVertexError(&cfg)
	}
	return NewGoogleVertexProvider(cfg), nil
}

func init() {
	builtInProviders[APIGoogleVertex] = func(apiKey, model, baseURL string) Provider {
		return NewGoogleVertexProvider(GoogleVertexConfig{
			APIKey:  apiKey,
			Model:   model,
			BaseURL: baseURL,
		})
	}
}

func (p *googleVertexProvider) ID() string   { return p.cfg.ProviderID }
func (p *googleVertexProvider) Close() error { p.client.CloseIdleConnections(); return nil }

func (p *googleVertexProvider) Stream(ctx context.Context, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
	// upstream: packages/ai/src/api/google-vertex.ts:stream
	if options.Fetch != nil && options.Fetch != http.DefaultClient {
		return nil, errors.New("Custom fetch is not supported by the Google Vertex adapter")
	}
	key := resolveVertexAPIKey(p.cfg.APIKey)
	project, location := "", ""
	if key == "" {
		project = resolveVertexProject(cmp.Or(options.Project, p.cfg.Project), options.Env)
		if project == "" {
			return nil, errors.New("Vertex AI requires a project ID. Set GOOGLE_CLOUD_PROJECT/GCLOUD_PROJECT or pass project in options.")
		}
		location = resolveVertexLocation(cmp.Or(options.Location, p.cfg.Location), options.Env)
		if location == "" {
			return nil, errors.New("Vertex AI requires a location. Set GOOGLE_CLOUD_LOCATION or pass location in options.")
		}
	}
	provider := &googleProvider{cfg: GoogleConfig{api: APIGoogleVertex, APIKey: key, Model: p.cfg.Model, ProviderID: p.cfg.ProviderID, BaseURL: resolveVertexBaseURL(p.cfg.BaseURL, project, location), ExtraHeaders: p.cfg.Headers, ThinkingLevelMap: p.cfg.ThinkingLevelMap, ModelMetadata: p.cfg.ModelMetadata}, client: p.client}
	if key == "" {
		provider.cfg.accessToken = p.accessToken
	}
	return provider.Stream(ctx, transcript, options)
}

func resolveVertexAPIKey(key string) string {
	key = trimJSWhitespace(key)
	if key == "gcp-vertex-credentials" || (len(key) > 2 && strings.HasPrefix(key, "<") && strings.HasSuffix(key, ">") && !strings.Contains(key[1:len(key)-1], ">")) {
		return ""
	}
	return key
}

func resolveVertexProject(explicit string, env ProviderEnv) string {
	if explicit != "" {
		return explicit
	}
	if value := getProviderEnvValue("GOOGLE_CLOUD_PROJECT", env); value != "" {
		return value
	}
	return getProviderEnvValue("GCLOUD_PROJECT", env)
}

func resolveVertexLocation(explicit string, env ProviderEnv) string {
	if explicit != "" {
		return explicit
	}
	return getProviderEnvValue("GOOGLE_CLOUD_LOCATION", env)
}

var vertexAPIVersionPath = lazyregexp.New(`(?:^|/)v\d+(?:beta\d*)?(?:/|$)`)

// vertexBaseURLIncludesAPIVersion is baseUrlIncludesApiVersion: a path segment of the WHATWG-parsed URL that is v<digits> with an optional beta<digits>; text that does not parse as a URL is tested whole.
func vertexBaseURLIncludesAPIVersion(baseURL string) bool {
	path := baseURL
	if parsed, err := nodeurl.ParseURL(baseURL); err == nil {
		path = parsed.Pathname
	}
	return vertexAPIVersionPath.MatchString(path)
}

func resolveVertexBaseURL(explicit, project, location string) string {
	if custom := resolveCustomBaseURL(explicit); custom != "" {
		if !vertexBaseURLIncludesAPIVersion(custom) {
			custom = strings.TrimRight(custom, "/") + "/v1"
		}
		return strings.TrimRight(custom, "/") + "/publishers/google"
	}
	if project == "" {
		return "https://aiplatform.googleapis.com/v1/publishers/google"
	}
	host := "aiplatform.googleapis.com"
	if location != "global" {
		host = location + "-" + host
	}
	return "https://" + host + "/v1/projects/" + project + "/locations/" + location + "/publishers/google"
}

func resolveCustomBaseURL(baseURL string) string {
	trimmed := trimJSWhitespace(baseURL)
	if trimmed == "" || strings.Contains(trimmed, "{location}") {
		return ""
	}
	return trimmed
}

func vertexAccessToken(ctx context.Context, env ProviderEnv) (string, error) {
	const scope = "https://www.googleapis.com/auth/cloud-platform"
	tokenClient := http.Client{}
	if client, ok := ctx.Value(oauth2.HTTPClient).(*http.Client); ok && client != nil {
		tokenClient = *client
	}
	tokenTransport := &vertexTokenTransport{base: tokenClient.Transport}
	if tokenTransport.base == nil {
		tokenTransport.base = http.DefaultTransport
	}
	tokenClient.Transport = tokenTransport
	ctx = context.WithValue(ctx, oauth2.HTTPClient, &tokenClient)
	var credentials *google.Credentials
	var err error
	if filename := getProviderEnvValue("GOOGLE_APPLICATION_CREDENTIALS", env); filename != "" {
		data, readErr := os.ReadFile(filename)
		if readErr != nil {
			return "", fmt.Errorf("Vertex ADC: %w", readErr)
		}
		var credType google.CredentialsType
		credType, data, err = vertexADCFileType(data)
		if err == nil {
			credentials, err = google.CredentialsFromJSONWithType(ctx, data, credType, scope)
		}
	} else {
		credentials, err = google.FindDefaultCredentials(ctx, scope)
	}
	if err != nil {
		return "", fmt.Errorf("Vertex ADC: %w", err)
	}
	token, err := credentials.TokenSource.Token()
	if err != nil {
		// google-auth-library's gaxios rejects a failed token request with node-fetch's FetchError text.
		if failedURL, failure := tokenTransport.failure(); failure != nil {
			if reason, ok := nodeNetErrorMessage(failure); ok {
				return "", &nodeTransportError{message: "request to " + failedURL + " failed, reason: " + reason, cause: err}
			}
		}
		return "", fmt.Errorf("Vertex ADC token: %w", err)
	}
	return token.AccessToken, nil
}

// vertexTokenTransport records the transport failure of a token request. The oauth2 package flattens a service account's
// token failure into its own text, so the failure is not in the returned error's chain.
type vertexTokenTransport struct {
	base      http.RoundTripper
	mu        sync.Mutex
	failedURL string
	err       error
}

func (transport *vertexTokenTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.base.RoundTrip(request)
	if err != nil {
		transport.mu.Lock()
		transport.failedURL, transport.err = request.URL.String(), err
		transport.mu.Unlock()
	}
	return response, err
}

func (transport *vertexTokenTransport) failure() (string, error) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return transport.failedURL, transport.err
}

// vertexADCFileTypes are the credential file types Pi's Vertex client accepts through google-auth-library GoogleAuth.fromJSON (keyFilename from GOOGLE_APPLICATION_CREDENTIALS). Any other type is rejected before its configuration is used.
var vertexADCFileTypes = []google.CredentialsType{
	google.ServiceAccount,
	google.AuthorizedUser,
	google.ExternalAccount,
	google.ExternalAccountAuthorizedUser,
	google.ImpersonatedServiceAccount,
}

func vertexADCFileType(data []byte) (google.CredentialsType, []byte, error) {
	var file map[string]json.RawMessage
	if err := json.Unmarshal(data, &file); err != nil {
		return "", nil, fmt.Errorf("parse credentials file: %w", err)
	}
	var typ string
	if raw, ok := file["type"]; ok {
		if err := json.Unmarshal(raw, &typ); err != nil {
			return "", nil, fmt.Errorf("parse credentials file: %w", err)
		}
	}
	if typ == "" {
		// google-auth-library's fromJSON falls through to its JWT (service account) client when a file names no type.
		file["type"] = json.RawMessage(`"service_account"`)
		normalized, err := json.Marshal(file)
		if err != nil {
			return "", nil, fmt.Errorf("parse credentials file: %w", err)
		}
		return google.ServiceAccount, normalized, nil
	}
	credType := google.CredentialsType(typ)
	if !slices.Contains(vertexADCFileTypes, credType) {
		return "", nil, fmt.Errorf("unsupported credentials type %q", typ)
	}
	return credType, data, nil
}
