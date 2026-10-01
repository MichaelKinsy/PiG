package coding

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Ports .upstream/v0.99.2/packages/coding-agent/test/model-catalog-protocol.test.ts (1 case, regression test for #9099).
//
// Upstream imports the catalog selection that pi.dev performs from scripts/model-catalog-protocol.ts, a file copied verbatim between
// the pi and pi.dev repositories. PiG has no pi.dev server, so the functions the stand-in server uses are ported below as test
// support, with the same regular expressions and ordering rules (scripts/model-catalog-protocol.ts:30-260).

const (
	modelCatalogSchemaVersion = 1
	modelCatalogPrefix        = "models/v1"
	modelCatalogIndexKey      = modelCatalogPrefix + "/index.json"
)

var (
	modelCatalogRevisionRE = regexp.MustCompile(`^sha256-[0-9a-f]{64}$`)
	piVersionRE            = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$`)
	piUserAgentRE          = regexp.MustCompile(`(?i)^pi/([^\s()]+)(?: \([^;()]+(?:;\s*[^;()]+(?:;\s*[^()]+)?)?\))?$`)
	modelTypeRE            = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	digitsRE               = regexp.MustCompile(`^\d+$`)
)

type modelCatalogIndexEntry struct {
	MinimumPiVersion string `json:"minimumPiVersion"`
	Revision         string `json:"revision"`
}

type modelCatalogIndex struct {
	SchemaVersion   int                      `json:"schemaVersion"`
	DefaultRevision string                   `json:"defaultRevision"`
	Catalogs        []modelCatalogIndexEntry `json:"catalogs"`
}

type modelCatalogRequest struct {
	kind           string // "catalog", "redirect" or "invalid"
	piVersion      *string
	representation string
	location       string
	err            string
}

func getModelCatalogProviderKey(revision, provider, representation string) string {
	suffix := ".json"
	if representation == "typed" {
		suffix = ".all.json"
	}
	return modelCatalogPrefix + "/revisions/" + revision + "/providers/" + provider + suffix
}

type parsedPiVersion struct {
	numbers    [3]int
	prerelease string
}

func parsePiVersion(version string) (parsedPiVersion, bool) {
	match := piVersionRE.FindStringSubmatch(strings.TrimSpace(version))
	if match == nil {
		return parsedPiVersion{}, false
	}
	var parsed parsedPiVersion
	for i := range 3 {
		number, err := strconv.Atoi(match[i+1])
		if err != nil || number > 1<<53-1 {
			return parsedPiVersion{}, false
		}
		parsed.numbers[i] = number
	}
	parsed.prerelease = match[4]
	return parsed, true
}

func comparePiVersions(left, right parsedPiVersion) int {
	for i := range 3 {
		if difference := left.numbers[i] - right.numbers[i]; difference != 0 {
			return difference
		}
	}
	if left.prerelease == right.prerelease {
		return 0
	}
	if left.prerelease == "" {
		return 1
	}
	if right.prerelease == "" {
		return -1
	}
	leftParts, rightParts := strings.Split(left.prerelease, "."), strings.Split(right.prerelease, ".")
	for i := range max(len(leftParts), len(rightParts)) {
		if i >= len(leftParts) {
			return -1
		}
		if i >= len(rightParts) {
			return 1
		}
		if leftParts[i] == rightParts[i] {
			continue
		}
		leftNumber, leftIsNumber := 0, digitsRE.MatchString(leftParts[i])
		rightNumber, rightIsNumber := 0, digitsRE.MatchString(rightParts[i])
		if leftIsNumber {
			leftNumber, _ = strconv.Atoi(leftParts[i])
		}
		if rightIsNumber {
			rightNumber, _ = strconv.Atoi(rightParts[i])
		}
		switch {
		case leftIsNumber && rightIsNumber:
			return leftNumber - rightNumber
		case leftIsNumber:
			return -1
		case rightIsNumber:
			return 1
		}
		return cmp.Compare(leftParts[i], rightParts[i])
	}
	return 0
}

// selectModelCatalog picks the entry with the highest minimum version that does not exceed piVersion (protocol.ts:selectModelCatalog).
func selectModelCatalog(index modelCatalogIndex, piVersion *string) *modelCatalogIndexEntry {
	if piVersion == nil {
		for i := range index.Catalogs {
			if index.Catalogs[i].Revision == index.DefaultRevision {
				return &index.Catalogs[i]
			}
		}
		return nil
	}
	requested, ok := parsePiVersion(*piVersion)
	if !ok {
		return nil
	}
	var selected *modelCatalogIndexEntry
	var selectedVersion parsedPiVersion
	for i := range index.Catalogs {
		minimum, ok := parsePiVersion(index.Catalogs[i].MinimumPiVersion)
		if !ok {
			panic("Invalid Pi version: " + index.Catalogs[i].MinimumPiVersion)
		}
		if comparePiVersions(minimum, requested) <= 0 && (selected == nil || comparePiVersions(minimum, selectedVersion) > 0) {
			selected, selectedVersion = &index.Catalogs[i], minimum
		}
	}
	return selected
}

// parseModelCatalogIndex validates an index read from storage (protocol.ts:parseModelCatalogIndex).
func parseModelCatalogIndex(value any) (modelCatalogIndex, error) {
	invalid := fmt.Errorf("Model catalog index is invalid: %s", modelCatalogIndexKey)
	data, err := json.Marshal(value)
	if err != nil {
		return modelCatalogIndex{}, err
	}
	var index modelCatalogIndex
	if err := json.Unmarshal(data, &index); err != nil || index.SchemaVersion != modelCatalogSchemaVersion || !modelCatalogRevisionRE.MatchString(index.DefaultRevision) || len(index.Catalogs) == 0 {
		return modelCatalogIndex{}, invalid
	}
	hasDefault := false
	for _, catalog := range index.Catalogs {
		if _, ok := parsePiVersion(catalog.MinimumPiVersion); !ok || !modelCatalogRevisionRE.MatchString(catalog.Revision) {
			return modelCatalogIndex{}, invalid
		}
		hasDefault = hasDefault || catalog.Revision == index.DefaultRevision
	}
	if !hasDefault {
		return modelCatalogIndex{}, invalid
	}
	return index, nil
}

// parseModelCatalogRequest decides how to answer a catalog request from its URL and User-Agent (protocol.ts:parseModelCatalogRequest).
func parseModelCatalogRequest(requestURL *url.URL, userAgent string) modelCatalogRequest {
	query := requestURL.Query()
	representation := "legacy"
	if types, present := query["types"]; present {
		if types[0] == "" || slices.ContainsFunc(strings.Split(types[0], ","), func(modelType string) bool { return !modelTypeRE.MatchString(modelType) }) {
			return modelCatalogRequest{kind: "invalid", err: "Invalid model types."}
		}
		representation = "typed"
	}
	piVersion, present := query["pi-version"]
	if !present {
		if match := piUserAgentRE.FindStringSubmatch(userAgent); match != nil {
			if _, ok := parsePiVersion(match[1]); ok {
				query.Set("pi-version", match[1])
				redirect := *requestURL
				redirect.RawQuery = query.Encode()
				return modelCatalogRequest{kind: "redirect", location: redirect.String()}
			}
		}
		return modelCatalogRequest{kind: "catalog", representation: representation}
	}
	if _, ok := parsePiVersion(piVersion[0]); !ok {
		return modelCatalogRequest{kind: "invalid", err: "Invalid Pi version."}
	}
	return modelCatalogRequest{kind: "catalog", piVersion: &piVersion[0], representation: representation}
}

// Ports .upstream/v0.99.2/packages/coding-agent/test/model-catalog-protocol.test.ts:17-53.
var (
	catalogLegacyRevision   = "sha256-" + strings.Repeat("a", 64)
	catalogMixedAPIRevision = "sha256-" + strings.Repeat("b", 64)
)

const catalogModelID = "anthropic/claude-sonnet-5"

func catalogProtocolModels() (legacy, mixedAPI map[string]any) {
	common := func() map[string]any {
		return map[string]any{
			"id": catalogModelID, "name": "Claude Sonnet 5", "provider": "openrouter", "reasoning": true, "input": []string{"text"},
			"cost":          map[string]any{"input": 3, "output": 15, "cacheRead": 0.3, "cacheWrite": 3.75},
			"contextWindow": 200_000, "maxTokens": 64_000,
		}
	}
	legacy = common()
	legacy["api"], legacy["baseUrl"] = "openai-completions", "https://openrouter.ai/api/v1"
	mixedAPI = common()
	mixedAPI["api"], mixedAPI["baseUrl"] = "anthropic-messages", "https://openrouter.ai/api"
	return legacy, mixedAPI
}

type catalogRoundTripper func(*http.Request) (*http.Response, error)

func (f catalogRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestModelCatalogProtocolWithTheCurrentClient(t *testing.T) {
	legacyModel, mixedAPIModel := catalogProtocolModels()
	index := modelCatalogIndex{
		SchemaVersion:   1,
		DefaultRevision: catalogMixedAPIRevision,
		Catalogs: []modelCatalogIndexEntry{
			{MinimumPiVersion: "0.80.7", Revision: catalogLegacyRevision},
			{MinimumPiVersion: "0.85.0", Revision: catalogMixedAPIRevision},
		},
	}
	// The legacy revision predates typed shards, so typed requests fall back to the chat-only shard (test.ts:45-50).
	typedMixedAPIModel := maps.Clone(mixedAPIModel)
	typedMixedAPIModel["type"] = "chat"
	objects := map[string]any{
		modelCatalogIndexKey: index,
		getModelCatalogProviderKey(catalogLegacyRevision, "openrouter", "legacy"):   map[string]any{catalogModelID: legacyModel},
		getModelCatalogProviderKey(catalogMixedAPIRevision, "openrouter", "legacy"): map[string]any{catalogModelID: mixedAPIModel},
		getModelCatalogProviderKey(catalogMixedAPIRevision, "openrouter", "typed"):  []any{typedMixedAPIModel},
	}
	// Newer than the bundled catalog, so the client applies the remote overlay (test.ts:14-15).
	const lastModified = "Thu, 01 Jan 2099 00:00:00 GMT"

	var mu sync.Mutex
	var requests []string
	// The stand-in for pi.dev's /api/models/providers/:provider route (test.ts:56-93).
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL.Path+"?"+r.URL.RawQuery)
		mu.Unlock()
		provider := ""
		if rest, ok := strings.CutPrefix(r.URL.Path, "/api/models/providers/"); ok && rest != "" && !strings.Contains(rest, "/") {
			provider = rest
		}
		catalogRequest := parseModelCatalogRequest(r.URL, r.UserAgent())
		switch {
		case provider == "":
			w.WriteHeader(http.StatusNotFound)
		case catalogRequest.kind == "redirect":
			w.Header().Set("location", catalogRequest.location)
			w.Header().Set("cache-control", "no-store")
			w.WriteHeader(http.StatusTemporaryRedirect)
		case catalogRequest.kind == "invalid":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(catalogRequest.err))
		default:
			parsed, err := parseModelCatalogIndex(objects[modelCatalogIndexKey])
			if err != nil {
				t.Error(err)
			}
			catalog := selectModelCatalog(parsed, catalogRequest.piVersion)
			var body any
			if catalog != nil {
				body = objects[getModelCatalogProviderKey(catalog.Revision, provider, catalogRequest.representation)]
				if body == nil {
					body = objects[getModelCatalogProviderKey(catalog.Revision, provider, "legacy")]
				}
			}
			if catalog == nil || body == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("content-type", "application/json")
			w.Header().Set("last-modified", lastModified)
			w.Header().Set("x-pi-model-catalog-revision", catalog.Revision)
			_ = json.NewEncoder(w).Encode(body)
		}
	}))
	t.Cleanup(func() {
		server.CloseClientConnections()
		server.Close()
	})

	// model-catalog-protocol.test.ts:107 "negotiates the catalog for its version and reaches the OpenRouter API"
	credentials := ai.NewInMemoryCredentialStore()
	// Upstream calls runtime.setRuntimeApiKey("openrouter", "test-key"); ModelRuntime has no such method here, so the key is stored.
	if _, err := credentials.Modify(t.Context(), "openrouter", func(*ai.Credential) (*ai.Credential, error) {
		return &ai.Credential{Type: ai.CredentialAPIKey, Key: "test-key"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PI_OFFLINE", "")
	runtime, err := CreateModelRuntime(t.Context(), CreateModelRuntimeOptions{
		Credentials: credentials, ModelsPath: new((*string)(nil)), CatalogBaseURL: server.URL, RefreshOnCreate: new(false),
		ModelsStore: ai.NewInMemoryModelsStore(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	refresh := runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(true), Force: new(true), Providers: []string{"openrouter"}})
	if len(refresh.Errors) != 0 {
		t.Fatalf("refresh errors = %v", refresh.Errors)
	}

	catalogURL := "/api/models/providers/openrouter?types=chat%2Cimage%2Cclassifier"
	// pig divergence (D65): Pi's client identifies as pi/<VERSION>, which the stand-in redirects to a URL carrying pi-version
	// (upstream :110 expects [catalogUrl, catalogUrl&pi-version=VERSION]). PiG identifies as pig/<version>, which the catalog
	// protocol does not recognize as Pi, so the request is answered without a redirect.
	mu.Lock()
	gotRequests := slices.Clone(requests)
	mu.Unlock()
	if !slices.Equal(gotRequests, []string{catalogURL}) {
		t.Fatalf("catalog requests = %v, want [%s]", gotRequests, catalogURL)
	}

	// selectModelCatalog(index, VERSION): PiG's version is below every minimum, so the default (mixed API) revision applies.
	version := Version
	expected := mixedAPIModel
	if selected := selectModelCatalog(index, &version); selected != nil && selected.Revision == catalogLegacyRevision {
		expected = legacyModel
	}
	model := runtime.GetModel("openrouter", catalogModelID)
	if model == nil {
		t.Fatalf("missing model: openrouter/%s", catalogModelID)
	}
	if string(model.ProviderMeta.API) != expected["api"] || model.ProviderMeta.BaseURL != expected["baseUrl"] {
		t.Fatalf("model api/baseUrl = %s/%s, want %v/%v", model.ProviderMeta.API, model.ProviderMeta.BaseURL, expected["api"], expected["baseUrl"])
	}

	// Capture the provider request instead of sending it (upstream stubs globalThis.fetch for any origin but openrouter.ai).
	var providerURL *url.URL
	captured := errors.New("Provider request captured")
	_ = runtime.CompleteSimple(t.Context(), model,
		ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Hello")}}},
		ai.StreamOptions{APIKey: "test-key", MaxRetries: new(0), Fetch: &http.Client{Transport: catalogRoundTripper(func(request *http.Request) (*http.Response, error) {
			providerURL = request.URL
			return nil, captured
		})}})
	want := "/api/v1/chat/completions"
	if expected["api"] == "anthropic-messages" {
		want = "/api/v1/messages"
	}
	if providerURL == nil || providerURL.Path != want {
		t.Fatalf("provider request URL = %v, want path %s", providerURL, want)
	}
}
