package ai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// fetchCredentialsError is the TypeError WHATWG fetch throws for a URL with a username or password.
type fetchCredentialsError struct{ href string }

func (e *fetchCredentialsError) Error() string {
	return "Request cannot be constructed from a URL that includes credentials: " + e.href
}

// errSDKConnection is the Stainless SDKs' APIConnectionError message, which they report for any fetch rejection, including the TypeError fetch throws for a URL with a username or password.
var errSDKConnection = errors.New("Connection error.")

// newSDKFetchRequest is newFetchRequest for the Stainless SDKs (openai, @anthropic-ai/sdk), whose client reports a fetch rejection as its connection error.
func newSDKFetchRequest(ctx context.Context, method, href string, body io.Reader, customFetch bool) (*http.Request, error) {
	request, err := newFetchRequest(ctx, method, href, body, customFetch)
	if err != nil {
		return nil, errSDKConnection
	}
	return request, nil
}

// sdkQueryParam is a single default query entry passed to the Stainless SDK's buildURL (`defaultQuery`).
type sdkQueryParam struct {
	name, value string
}

// sdkBuildURL is `client.buildURL(path)` of `@anthropic-ai/sdk` (0.129.0, client.mjs:386-400) for a client built with baseURL:
// `new URL(baseURL + path)` with one slash dropped where both meet, then, when the URL carries a query, the pairs of
// `Object.fromEntries(url.searchParams)` written back through qs. The error is the constructor's TypeError message.
func sdkBuildURL(baseURL, path string) (string, error) {
	if strings.HasSuffix(baseURL, "/") && strings.HasPrefix(path, "/") {
		path = path[1:]
	}
	parsed, err := nodeurl.ParseURL(baseURL + path)
	if err != nil {
		return "", err
	}
	pairs := nodeurl.SearchPairs(parsed.Query)
	if len(pairs) == 0 {
		return parsed.Href(), nil
	}
	members := make([]sdkQueryParam, 0, len(pairs))
	for _, pair := range pairs {
		members = setSDKQueryParam(members, pair[0], pair[1])
	}
	return parsed.WithSearch(stringifySDKQuery(members)), nil
}

// openaiBuildURL is `client.buildURL(path)` of openai 7.59.0 (client.mjs:502-554) without defaultQuery.
func openaiBuildURL(baseURL, path string) (string, error) {
	return openaiBuildURLWithDefaultQuery(baseURL, path)
}

// openaiBuildURLWithDefaultQuery is `client.buildURL(path)` of openai 7.59.0 (client.mjs:502-554, azure.mjs:41):
// when baseURL contains '?', parse baseURL first, capture its search params, clear search and hash before appending path
// (dropping one slash where base.pathname and path meet), then merge `{ ...baseQuery, ...pathQuery, ...defaultQuery }`
// through qs and restore repeated baseQuery keys (not overridden by pathQuery or defaultQuery) via URLSearchParams.
func openaiBuildURLWithDefaultQuery(baseURL, path string, defaultQuery ...sdkQueryParam) (string, error) {
	var (
		parsed     nodeurl.URL
		baseParams [][2]string
		hasBase    bool
		err        error
	)
	if strings.Contains(baseURL, "?") {
		base, err := nodeurl.ParseURL(baseURL)
		if err != nil {
			return "", err
		}
		baseParams = nodeurl.SearchPairs(base.Query)
		hasBase = true
		cleanBase := base.WithoutQuery().WithoutHash()
		joinedPath := path
		if strings.HasSuffix(base.Pathname, "/") && strings.HasPrefix(joinedPath, "/") {
			joinedPath = joinedPath[1:]
		}
		parsed, err = nodeurl.ParseURL(cleanBase.Href() + joinedPath)
		if err != nil {
			return "", err
		}
	} else {
		joinedPath := path
		if strings.HasSuffix(baseURL, "/") && strings.HasPrefix(joinedPath, "/") {
			joinedPath = joinedPath[1:]
		}
		parsed, err = nodeurl.ParseURL(baseURL + joinedPath)
		if err != nil {
			return "", err
		}
	}

	pathPairs := nodeurl.SearchPairs(parsed.Query)
	if len(baseParams) == 0 && len(pathPairs) == 0 && len(defaultQuery) == 0 {
		return parsed.Href(), nil
	}

	// overridingQuery = { ...pathQuery, ...defaultQuery }
	// query = { ...baseQuery, ...overridingQuery }
	// Object.fromEntries keeps the last value of a repeated name at the first one's position; spreading overrides in place and appends new names at the end; JS object enumeration puts integer-like keys first.
	overridingKeys := make(map[string]struct{}, len(pathPairs)+len(defaultQuery))
	for _, pair := range pathPairs {
		overridingKeys[pair[0]] = struct{}{}
	}
	for _, param := range defaultQuery {
		overridingKeys[param.name] = struct{}{}
	}

	members := make([]sdkQueryParam, 0, len(baseParams)+len(pathPairs)+len(defaultQuery))
	for _, pair := range baseParams {
		members = setSDKQueryParam(members, pair[0], pair[1])
	}
	for _, pair := range pathPairs {
		members = setSDKQueryParam(members, pair[0], pair[1])
	}
	for _, param := range defaultQuery {
		members = setSDKQueryParam(members, param.name, param.value)
	}
	qsText := stringifySDKQuery(members)

	if hasBase && len(baseParams) > 1 {
		seen := make(map[string]struct{}, len(baseParams))
		repeated := make(map[string]struct{})
		for _, pair := range baseParams {
			key := pair[0]
			if _, wasSeen := seen[key]; wasSeen {
				if _, overridden := overridingKeys[key]; !overridden {
					repeated[key] = struct{}{}
				}
			}
			seen[key] = struct{}{}
		}
		if len(repeated) > 0 {
			// url.search = this.stringifyQuery(query); then iterate url.searchParams skipping repeated keys,
			// then append repeated keys from baseParams, serialized via URLSearchParams.toString() (FormURLEncode).
			withQS, err := nodeurl.ParseURL(parsed.WithSearch(qsText))
			if err != nil {
				return "", err
			}
			var merged strings.Builder
			writePair := func(k, v string) {
				if merged.Len() > 0 {
					merged.WriteByte('&')
				}
				merged.WriteString(nodeurl.FormURLEncode(k))
				merged.WriteByte('=')
				merged.WriteString(nodeurl.FormURLEncode(v))
			}
			for _, pair := range nodeurl.SearchPairs(withQS.Query) {
				if _, isRep := repeated[pair[0]]; !isRep {
					writePair(pair[0], pair[1])
				}
			}
			for _, pair := range baseParams {
				if _, isRep := repeated[pair[0]]; isRep {
					writePair(pair[0], pair[1])
				}
			}
			return withQS.WithSearch(merged.String()), nil
		}
	}

	return parsed.WithSearch(qsText), nil
}

func stringifySDKQuery(members []sdkQueryParam) string {
	jsstring.SortOwnKeys(members, func(m sdkQueryParam) string { return m.name })
	var query strings.Builder
	for i, m := range members {
		if i > 0 {
			query.WriteByte('&')
		}
		query.WriteString(qsEncode(m.name) + "=" + qsEncode(m.value))
	}
	return query.String()
}

func setSDKQueryParam(members []sdkQueryParam, name, value string) []sdkQueryParam {
	for i := range members {
		if members[i].name == name {
			members[i].value = value
			return members
		}
	}
	return append(members, sdkQueryParam{name: name, value: value})
}

// qsEncode is the openai SDK's qs encoder (openai/internal/qs/utils.mjs encode) for a string: UTF-8 percent-encoding of every byte but the RFC 3986 unreserved ones.
func qsEncode(value string) string {
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c == '-' || c == '.' || c == '_' || c == '~' || '0' <= c && c <= '9' || 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' {
			out.WriteByte(c)
			continue
		}
		out.WriteByte('%')
		out.WriteByte(hex[c>>4])
		out.WriteByte(hex[c&15])
	}
	return out.String()
}

// newFetchRequest is the Request global fetch builds for input (undici's Request constructor): the URL parser reads input, the serialized href is kept as written (the request target leaves out the fragment), and a URL with credentials is refused, unless a custom fetch (customFetch) receives the URL as is. Its errors are fetch's TypeError messages; newSDKFetchRequest reports any of them as errSDKConnection.
func newFetchRequest(ctx context.Context, method, input string, body io.Reader, customFetch bool) (*http.Request, error) {
	parsed, err := nodeurl.ParseURL(input)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse URL from %s", input)
	}
	if !customFetch && (parsed.Username != "" || parsed.Password != "") {
		return nil, &fetchCredentialsError{input}
	}
	href := parsed.Href()
	requestURL, err := nodeurl.RequestURL(href)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, method, "", body)
	if err != nil {
		return nil, err
	}
	request.URL, request.Host = requestURL, requestURL.Host
	return request, nil
}

// googleSDKURL is the URL @google/genai requests for path (ApiClient.constructUrl and requestStream): the base without one trailing slash, the API version when set, and path joined with "/" into new URL(...), then `searchParams.set("alt", "sse")` unless the first alt is already "sse", which rewrites the whole query through the form serializer. The error is the constructor's TypeError message.
func googleSDKURL(baseURL, apiVersion, path string) (string, error) {
	elements := []string{strings.TrimSuffix(baseURL, "/")}
	if apiVersion != "" {
		elements = append(elements, apiVersion)
	}
	if path != "" {
		elements = append(elements, path)
	}
	parsed, err := nodeurl.ParseURL(strings.Join(elements, "/"))
	if err != nil {
		return "", err
	}
	for _, pair := range nodeurl.SearchPairs(parsed.Query) {
		if pair[0] == "alt" {
			if pair[1] == "sse" {
				return parsed.Href(), nil
			}
			break
		}
	}
	return parsed.WithSearchParam("alt", "sse"), nil
}
