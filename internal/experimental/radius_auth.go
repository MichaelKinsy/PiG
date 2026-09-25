// Package experimental provides opt-in transports for the experimental Session runtime. It does not register CLI commands or select network endpoints.
package experimental

import (
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// EnvRadiusGateway is the upstream experimental gateway override.
const EnvRadiusGateway = codingagent.EnvRadiusGateway

// AuthInput selects an explicit token or a token file. Nil selects stored credentials.
type AuthInput struct {
	Type  string
	Token string
	Path  string
}

// RadiusRelayAuth is one connection attempt's resolved credential.
type RadiusRelayAuth struct {
	Gateway string
	Token   string
}

// RadiusRelayAuthOptions requires an explicit gateway. CreateRuntime supplies the stored-auth runtime without model-network refresh; it is called once, lazily, only without Input. Its owner must configure OAuth refresh for its selected gateway.
type RadiusRelayAuthOptions struct {
	Input         *AuthInput
	Gateway       string
	CreateRuntime func() (*codingagent.RequestAuthRuntime, error)
}

// RadiusRelayAuthResolver rereads explicit or stored credentials for every connection attempt.
type RadiusRelayAuthResolver struct {
	options    RadiusRelayAuthOptions
	once       sync.Once
	runtime    *codingagent.RequestAuthRuntime
	runtimeErr error
	mu         sync.Mutex
}

// NewRadiusRelayAuthResolver creates an inert resolver. Gateway and the stored-auth runtime are caller-owned so construction cannot select a hosted service.
func NewRadiusRelayAuthResolver(options RadiusRelayAuthOptions) (*RadiusRelayAuthResolver, error) {
	// pig divergence (D64): Callers select the gateway and auth runtime; Stock does not select an Earendil service.
	if options.Gateway == "" {
		return nil, errors.New("Radius relay gateway must be supplied explicitly")
	}
	if options.Input == nil && options.CreateRuntime == nil {
		return nil, errors.New("Radius relay stored authentication requires a runtime factory")
	}
	if options.Input != nil {
		input := *options.Input
		if input.Type != "token" && input.Type != "file" {
			return nil, errors.New("Invalid Radius authentication input type")
		}
		options.Input = &input
	}
	options.Gateway = ai.NormalizeRadiusGatewayURL(options.Gateway)
	return &RadiusRelayAuthResolver{options: options}, nil
}

// Gateway returns the normalized gateway selected by the caller.
func (r *RadiusRelayAuthResolver) Gateway() string { return r.options.Gateway }

// Resolve checks cancellation, then PI_OFFLINE presence, before reading credentials. Required turns missing auth into an actionable error. Stored OAuth must have at least five minutes of validity.
func (r *RadiusRelayAuthResolver) Resolve(ctx context.Context, required bool) (*RadiusRelayAuth, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, offline := os.LookupEnv("PI_OFFLINE"); offline {
		if required {
			return nil, errors.New("Radius relay connections are unavailable in offline mode")
		}
		return nil, nil
	}
	token, err := r.token(ctx)
	if err != nil {
		return nil, err
	}
	if token != "" {
		return &RadiusRelayAuth{Gateway: r.Gateway(), Token: token}, nil
	}
	if required {
		return nil, errors.New("Radius authentication is required; start Pi and run /login radius, then retry")
	}
	return nil, nil
}

func (r *RadiusRelayAuthResolver) token(ctx context.Context) (string, error) {
	if input := r.options.Input; input != nil {
		value := input.Token
		if input.Type == "file" {
			path, err := authFilePath(input.Path)
			if err != nil {
				return "", err
			}
			data, err := readAuthFile(ctx, path)
			if err != nil {
				return "", err
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
			value = string(data)
		}
		token := strings.Trim(value, "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff")
		if token == "" {
			return "", errors.New("Radius authentication token must not be empty")
		}
		return token, nil
	}
	r.once.Do(func() { r.runtime, r.runtimeErr = r.options.CreateRuntime() })
	if r.runtimeErr != nil {
		return "", r.runtimeErr
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if r.runtime == nil {
		return "", errors.New("Radius authentication runtime is missing")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// upstream: packages/coding-agent/src/experimental/radius-auth.ts: RadiusRelayAuthResolver
	minimum := float64(5 * 60_000)
	auth, err := r.runtime.GetAuth(ctx, "radius", ai.AuthResolutionOverrides{MinOAuthValidityMs: &minimum})
	if err != nil {
		return "", err
	}
	return relayAuthCredential(auth), nil
}

var relayBearerPattern = regexp.MustCompile(`(?i)^Bearer[\t\n\v\f\r \x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+([^\n\r\x{2028}\x{2029}]+)$`)

func relayAuthCredential(auth *ai.AuthResult) string {
	if auth == nil {
		return ""
	}
	if auth.Auth.APIKey != "" {
		return auth.Auth.APIKey
	}
	for name, value := range auth.Auth.Headers {
		if strings.EqualFold(name, "authorization") && value != nil {
			if match := relayBearerPattern.FindStringSubmatch(*value); match != nil {
				return match[1]
			}
		}
	}
	return ""
}

func readAuthFile(ctx context.Context, path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = file.Close(); close(closed) })
	defer func() {
		if !stop() {
			<-closed
		}
		_ = file.Close()
	}()
	data, err := io.ReadAll(file)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return data, err
}

func authFilePath(path string) (string, error) {
	if strings.HasPrefix(path, "file://") {
		parsed, err := url.Parse(path)
		if err != nil {
			return "", err
		}
		if parsed.Host != "" && parsed.Host != "localhost" {
			return "", errors.New("File URL host must be localhost or empty")
		}
		if strings.Contains(strings.ToLower(parsed.EscapedPath()), "%2f") {
			return "", errors.New("File URL path must not include encoded / characters")
		}
		path = parsed.Path
	}
	return filepath.Abs(codingagent.ExpandTildePath(path))
}
