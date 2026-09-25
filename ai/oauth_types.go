package ai

import "context"

// Mirrors upstream .upstream/current/packages/ai/src/utils/oauth/types.ts.

// OAuthCredentials holds the tokens returned by an OAuth flow.
type OAuthCredentials struct {
	Refresh string `json:"refresh"`
	Access  string `json:"access"`
	Expires int64  `json:"expires"` // Unix millis
	// ProjectID is used by Google Cloud Code Assist / Antigravity OAuth.
	// Upstream OAuthCredentials is open-ended (`[key: string]: unknown`);
	// pig models the only currently-used extra field explicitly so
	// auth.json round-trips the Google credential shape.
	ProjectID string `json:"projectId,omitempty"`
	// Scope is the granted scope returned by flows that report one (Radius).
	Scope string `json:"scope,omitempty"`
}

// OAuthPrompt describes an interactive prompt during the OAuth flow.
type OAuthPrompt struct {
	Message     string
	Placeholder string
	AllowEmpty  bool
}

// OAuthAuthInfo is the URL + instructions presented to the user.
type OAuthAuthInfo struct {
	URL          string
	Instructions string
}

// OAuthDeviceCodeInfo is the user code + verification URL presented during a
// device-code (RFC 8628) login flow. Mirrors upstream OAuthDeviceCodeInfo
// (types.ts:26-31).
type OAuthDeviceCodeInfo struct {
	UserCode         string
	VerificationURI  string
	IntervalSeconds  float64
	ExpiresInSeconds float64
}

// OAuthSelectOption is one selectable choice in an OAuth flow.
type OAuthSelectOption struct {
	ID    string
	Label string
}

// OAuthSelectPrompt describes an interactive selection prompt during the OAuth flow.
type OAuthSelectPrompt struct {
	Message string
	Options []OAuthSelectOption
}

// OAuthLoginCallbacks groups the callbacks used during an OAuth login flow.
type OAuthLoginCallbacks struct {
	OnAuth                   func(info OAuthAuthInfo)
	OnDeviceCode             func(info OAuthDeviceCodeInfo)
	OnPrompt                 func(prompt OAuthPrompt) (string, error)
	OnPromptContext          func(context.Context, OAuthPrompt) (string, error)
	OnProgress               func(message string)
	OnManualCodeInput        func() (string, error)
	OnManualCodeInputContext func(context.Context) (string, error)
	OnSelect                 func(prompt OAuthSelectPrompt) (string, error)
	OnSelectContext          func(context.Context, OAuthSelectPrompt) (string, error)
}

// OAuthCredentialStatus describes a stored credential owned by a registered
// OAuth provider outside auth.json. AuthType is "oauth" or "api_key" and Source
// is rendered by the login selector (for example "stored").
type OAuthCredentialStatus struct {
	AuthType string
	Source   string
}

// OAuthCredentialStore is an optional extension hook for OAuth providers whose
// credentials live outside auth.json. Built-in upstream providers do not need
// it; product extensions can implement it to keep their existing credential
// stores while still participating in the generic /login and /logout surfaces.
type OAuthCredentialStore interface {
	OAuthCredentialStatus() (OAuthCredentialStatus, bool)
	StoreOAuthCredentials(creds OAuthCredentials) (path string, err error)
	DeleteOAuthCredentials() (deleted bool, err error)
}

// OAuthProviderInterface is the contract for an OAuth provider.
type OAuthProviderInterface interface {
	// ID returns the provider identifier (e.g. "anthropic").
	ID() string
	// Name returns the human-readable provider name.
	Name() string
	// UsesCallbackServer returns true if the flow uses a local HTTP callback.
	UsesCallbackServer() bool
	// Login runs the OAuth authorization flow.
	Login(callbacks OAuthLoginCallbacks) (OAuthCredentials, error)
	// RefreshToken refreshes expired credentials.
	RefreshToken(creds OAuthCredentials) (OAuthCredentials, error)
	// GetAPIKey extracts the bearer token from credentials.
	GetAPIKey(creds OAuthCredentials) string
}

// OAuthSubscriptionProvider marks an OAuth login billed by a subscription.
// Mirrors upstream OAuth provider isSubscription.
type OAuthSubscriptionProvider interface {
	IsSubscription() bool
}

// IsOAuthSubscriptionProvider reports whether the provider's OAuth login is a
// subscription login. Mirrors upstream ModelRuntime.isUsingSubscription's
// auth.oauth?.isSubscription === true test.
func IsOAuthSubscriptionProvider(providerID string) bool {
	provider, ok := GetOAuthProvider(providerID)
	if !ok {
		return false
	}
	subscription, ok := provider.(OAuthSubscriptionProvider)
	return ok && subscription.IsSubscription()
}
