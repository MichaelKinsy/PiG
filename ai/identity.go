package ai

// Ports packages/ai/src/utils/identity.ts.

import (
	"encoding/json"
	"errors"
	"net/url"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// UserAgentMode is the `User-Agent` sent to providers: UserAgentDetailed, UserAgentShort, or UserAgentCustom(value) (upstream's `"short" | "detailed" | { custom: string }`). The zero value is not a mode; ClientIdentity.UserAgent is nil when the mode is omitted.
//
// pig divergence (D65): detailed is `pig/<version> (<platform> <release>; <arch>)` and short is `pig`, where upstream sends `pi/<version> (<platform>; <runtime>; <arch>)` and `pi`.
type UserAgentMode struct {
	kind   userAgentKind
	custom string
}

type userAgentKind uint8

const (
	userAgentKindDetailed userAgentKind = iota
	userAgentKindShort
	userAgentKindCustom
)

var (
	// UserAgentDetailed is the version and platform form: the `"detailed"` mode.
	UserAgentDetailed = UserAgentMode{kind: userAgentKindDetailed}
	// UserAgentShort is the product name alone: the `"short"` mode.
	UserAgentShort = UserAgentMode{kind: userAgentKindShort}
)

// UserAgentCustom is the `{ custom }` mode: the given value is the `User-Agent`.
func UserAgentCustom(value string) UserAgentMode {
	return UserAgentMode{kind: userAgentKindCustom, custom: value}
}

// Custom reports the value of a custom mode.
func (mode UserAgentMode) Custom() (string, bool) {
	return mode.custom, mode.kind == userAgentKindCustom
}

// MarshalJSON writes `"short"`, `"detailed"` or `{"custom":"..."}`.
func (mode UserAgentMode) MarshalJSON() ([]byte, error) {
	switch mode.kind {
	case userAgentKindShort:
		return []byte(`"short"`), nil
	case userAgentKindCustom:
		return json.Marshal(struct { //perf:ok a persisted identity is encoded once per save, not per request
			Custom string `json:"custom"`
		}{mode.custom})
	}
	return []byte(`"detailed"`), nil
}

// UnmarshalJSON reads the forms MarshalJSON writes.
func (mode *UserAgentMode) UnmarshalJSON(data []byte) error {
	var name string
	if json.Unmarshal(data, &name) == nil {
		switch name {
		case "short":
			*mode = UserAgentShort
		case "detailed":
			*mode = UserAgentDetailed
		default:
			return errors.New(`ai: user agent mode must be "short", "detailed" or { "custom": string }`)
		}
		return nil
	}
	var object struct {
		Custom *string `json:"custom"`
	}
	if err := json.Unmarshal(data, &object); err != nil || object.Custom == nil {
		return errors.New(`ai: user agent mode must be "short", "detailed" or { "custom": string }`)
	}
	*mode = UserAgentCustom(*object.Custom)
	return nil
}

// ClientAttribution is who requests are attributed to. GetAttributionHeaders maps the fields to each provider's headers.
type ClientAttribution struct {
	// Name is the app name: OpenRouter app title, NVIDIA invoke origin (first letter upper-cased), OpenCode client.
	Name string `json:"name"`
	// URL is the app URL: OpenRouter `HTTP-Referer`, the app's identity in OpenRouter's rankings, grouped by origin.
	URL string `json:"url"`
	// Categories are the app categories, e.g. `["cli-agent"]`: OpenRouter `X-OpenRouter-Categories`, comma-separated.
	Categories []string `json:"categories,omitzero"`
}

// PiAttribution is the attribution used by the agent loop, durable Harness and coding agent (upstream PI_ATTRIBUTION).
//
// pig divergence (D26): the values are PiG's brand (internal/coding/pigidentity), not pi's `{ name: "pi", url: "https://pi.dev", categories: ["cli-agent"] }`.
var PiAttribution = ClientAttribution{Name: pigidentity.AttributionName, URL: pigidentity.AttributionURL, Categories: []string{pigidentity.AttributionCategory}}

// ClientIdentity is how requests identify the calling app to providers. Every field is optional; absent fields use the defaults: a detailed `User-Agent` with this package's version and no attribution headers. It encodes as plain JSON.
type ClientIdentity struct {
	// UserAgent is the `User-Agent` mode. Nil is `"detailed"`.
	UserAgent *UserAgentMode `json:"userAgent,omitempty"`
	// Attribution is the provider attribution. Nil with AttributionNull false sends none; AttributionNull is upstream's `null`, which also turns off attribution set by a merged identity.
	Attribution *ClientAttribution `json:"attribution,omitempty"`
	// AttributionNull is `attribution: null`.
	AttributionNull bool `json:"-"`
	// Version is the version in the detailed `User-Agent`. Nil is ProductVersion.
	Version *string `json:"version,omitempty"`
}

// MarshalJSON writes an explicit null attribution as `"attribution": null`.
func (identity ClientIdentity) MarshalJSON() ([]byte, error) {
	type plain ClientIdentity
	if !identity.AttributionNull {
		return json.Marshal(plain(identity)) //perf:ok a persisted identity is encoded once per save, not per request
	}
	return json.Marshal(struct { //perf:ok a persisted identity is encoded once per save, not per request
		plain
		Attribution *ClientAttribution `json:"attribution"`
	}{plain: plain(identity)})
}

// UnmarshalJSON reads an explicit `"attribution": null` as AttributionNull.
func (identity *ClientIdentity) UnmarshalJSON(data []byte) error {
	type plain ClientIdentity
	var decoded struct {
		plain
		Attribution json.RawMessage `json:"attribution"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*identity = ClientIdentity(decoded.plain)
	switch trimmed := strings.TrimSpace(string(decoded.Attribution)); trimmed {
	case "":
	case "null":
		identity.AttributionNull = true
	default:
		identity.Attribution = new(ClientAttribution)
		return json.Unmarshal(decoded.Attribution, identity.Attribution)
	}
	return nil
}

// ClientIdentityTarget is the provider and endpoint a request goes to.
type ClientIdentityTarget struct {
	Provider string
	BaseURL  string
}

const (
	nvidiaNIMHost = "integrate.api.nvidia.com"
	openCodeHost  = "opencode.ai"
)

// MergeClientIdentity merges identities field by field; later sources win and omitted fields are skipped. It returns nil when every source is nil.
func MergeClientIdentity(sources ...*ClientIdentity) *ClientIdentity {
	var merged *ClientIdentity
	for _, source := range sources {
		if source == nil {
			continue
		}
		next := ClientIdentity{}
		if merged != nil {
			next = *merged
		}
		if source.UserAgent != nil {
			next.UserAgent = source.UserAgent
		}
		if source.Attribution != nil || source.AttributionNull {
			next.Attribution, next.AttributionNull = source.Attribution, source.AttributionNull
		}
		if source.Version != nil {
			next.Version = source.Version
		}
		merged = &next
	}
	return merged
}

// GetClientVersion is the version in the detailed `User-Agent`.
func GetClientVersion(identity *ClientIdentity) string {
	if identity != nil && identity.Version != nil {
		return *identity.Version
	}
	return ProductVersion
}

// FormatUserAgent is the `User-Agent` value of an identity.
//
// pig divergence (D65): see UserAgentMode.
func FormatUserAgent(identity *ClientIdentity) string {
	mode := UserAgentDetailed
	if identity != nil && identity.UserAgent != nil {
		mode = *identity.UserAgent
	}
	switch mode.kind {
	case userAgentKindCustom:
		return mode.custom
	case userAgentKindShort:
		return pigidentity.UserAgentProduct
	}
	return pigidentity.UserAgentProduct + "/" + GetClientVersion(identity) + " (" + piUserAgentPlatform(runtime.GOOS) + " " + userAgentRelease.get() + "; " + piUserAgentArch(runtime.GOARCH) + ")"
}

// matchesHost is `new URL(baseUrl).hostname === expectedHost`; a URL the WHATWG parser rejects never matches.
func matchesHost(baseURL, expectedHost string) bool {
	input := nodeurl.PrepareInput(baseURL)
	scheme, rest, ok := strings.Cut(input, ":")
	if !ok {
		return false
	}
	switch strings.ToLower(scheme) {
	case "http", "https":
	case "ws", "wss", "ftp":
		// The other special schemes with a network host parse their host as http does.
		input = "https:" + rest
	default:
		// A non-special scheme keeps its opaque host as written, letter case included.
		parsed, err := url.Parse(input)
		return err == nil && parsed.Hostname() == expectedHost
	}
	parsed, err := nodeurl.ParseHTTPURL(input)
	return err == nil && parsed.Hostname == expectedHost
}

// isOpenRouter matches the base URL by substring, not host.
func isOpenRouter(target ClientIdentityTarget) bool {
	return target.Provider == "openrouter" || strings.Contains(target.BaseURL, "openrouter.ai") //portlint:allow providerliteral identity.ts isOpenRouter matches the provider id and the base URL as Pi does
}

func isNvidiaNIM(target ClientIdentityTarget) bool {
	return target.Provider == "nvidia" || matchesHost(target.BaseURL, nvidiaNIMHost) //portlint:allow providerliteral identity.ts isNvidiaNim matches the provider id and the host as Pi does
}

func isOpenCode(target ClientIdentityTarget) bool {
	return target.Provider == "opencode" || target.Provider == "opencode-go" || matchesHost(target.BaseURL, openCodeHost) //portlint:allow providerliteral identity.ts isOpenCode matches the provider ids and the host as Pi does
}

// GetAttributionHeaders are the provider attribution headers of an identity for a request target. Empty when attribution is off.
func GetAttributionHeaders(target ClientIdentityTarget, identity *ClientIdentity) map[string]string {
	pairs := attributionPairs(target, identity)
	headers := make(map[string]string, len(pairs)+1)
	for _, pair := range pairs {
		headers[pair[0]] = pair[1]
	}
	return headers
}

// attributionPairs is GetAttributionHeaders in the member order of upstream's object literals.
func attributionPairs(target ClientIdentityTarget, identity *ClientIdentity) [][2]string {
	if identity == nil || identity.Attribution == nil {
		return nil
	}
	attribution := identity.Attribution
	switch {
	case isOpenRouter(target):
		pairs := [][2]string{{"HTTP-Referer", attribution.URL}, {"X-OpenRouter-Title", attribution.Name}}
		if len(attribution.Categories) > 0 {
			pairs = append(pairs, [2]string{"X-OpenRouter-Categories", strings.Join(attribution.Categories, ",")})
		}
		return pairs
	case isNvidiaNIM(target):
		// NVIDIA expects a capitalized origin, such as `Pi`.
		return [][2]string{{"X-BILLING-INVOKE-ORIGIN", upperFirstUnit(attribution.Name)}}
	case isOpenCode(target):
		return [][2]string{{"x-opencode-client", attribution.Name}}
	}
	return nil
}

// GetOpenCodeSessionHeaders is OpenCode's per-conversation routing header for requests to OpenCode, including custom providers pointing at it. Nil without a session or for other targets.
func GetOpenCodeSessionHeaders(target ClientIdentityTarget, sessionID string) ProviderHeaders {
	if sessionID == "" || !isOpenCode(target) {
		return nil
	}
	return ProviderHeaders{openCodeSessionHeader: new(sessionID)}
}

// GetIdentityHeaders are the default headers an identity adds to a request: `User-Agent` and provider attribution. APIs apply them below model, provider, and request headers, so configured headers override them and null request headers remove them.
func GetIdentityHeaders(target ClientIdentityTarget, identity *ClientIdentity) map[string]string {
	headers := GetAttributionHeaders(target, identity)
	headers["User-Agent"] = FormatUserAgent(identity)
	return headers
}

// identityHeaderPairs are the GetIdentityHeaders members in upstream's order: `User-Agent`, then the attribution headers.
func identityHeaderPairs(target ClientIdentityTarget, identity *ClientIdentity) [][2]string {
	return append([][2]string{{"User-Agent", FormatUserAgent(identity)}}, attributionPairs(target, identity)...)
}

// applyIdentityHeaders sets the identity's default headers of a request to target before model, provider and request headers.
func applyIdentityHeaders(header interface{ Set(key, value string) }, target ClientIdentityTarget, identity *ClientIdentity) {
	for name, value := range GetIdentityHeaders(target, identity) {
		header.Set(name, value)
	}
}

// upperFirstUnit is `name.charAt(0).toUpperCase() + name.slice(1)`: a first character outside the basic plane is a lone surrogate to JavaScript, which upper-casing leaves unchanged.
func upperFirstUnit(name string) string {
	first, size := utf8.DecodeRuneInString(name)
	if size == 0 || first > 0xFFFF || first == utf8.RuneError {
		return name
	}
	return jsstring.ToUpper(string(first)) + name[size:] //portlint:allow utf16units size is the byte length of the first whole rune, which is a BMP character (one UTF-16 unit), so the slice starts at the unit after it
}

// identityProviderHeaders are an identity's default headers as the lowest source of a ProviderHeaders merge.
func identityProviderHeaders(target ClientIdentityTarget, identity *ClientIdentity) ProviderHeaders {
	return ProviderHeadersFromStrings(GetIdentityHeaders(target, identity))
}
