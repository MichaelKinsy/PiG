package modelgen

// Ports packages/ai/catalog/sources/index.ts and the Radius gateway config reader of packages/ai/src/providers/radius-config.ts.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	_ "github.com/MichaelKinsy/PiG/internal/tlsdistrust" // hardens http.DefaultClient (pig additive (D191))
)

// NvidiaBaseURL is the NVIDIA NIM endpoint.
const NvidiaBaseURL = "https://integrate.api.nvidia.com/v1"

// DefaultRadiusGateway is the unauthenticated public Radius gateway.
const DefaultRadiusGateway = "https://radius.pi.dev"

// CatalogSources is everything fetched from upstream before the catalog is built.
type CatalogSources struct {
	ModelsDev          *Obj
	ModelsDevDecisions map[string]ModelsDevDecisionMetadata
	OpenRouter         OpenRouterCatalog
	AIGateway          AiGatewayCatalog
	// NvidiaModelIDs are the live NVIDIA NIM ids, keyed by both the raw and the normalized id.
	NvidiaModelIDs map[string]string
	Radius         []*Obj
}

func fetchBody(url string, headers map[string]string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	return http.DefaultClient.Do(request)
}

func fetchJSON(url, label string) (any, error) {
	response, err := fetchBody(url, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode > 299 { // fetch response.ok
		return nil, fmt.Errorf("%s returned %d", label, response.StatusCode)
	}
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	return ParseJSON(data)
}

// NormalizeNvidiaModelID is the id form models.dev lists NIM models under.
func NormalizeNvidiaModelID(modelID string) string {
	return strings.ReplaceAll(strings.ToLower(modelID), "_", ".")
}

func fetchNvidiaModelIDs() (map[string]string, error) {
	data, err := fetchJSON(NvidiaBaseURL+"/models", "NVIDIA NIM API")
	if err != nil {
		return nil, err
	}
	ids := map[string]string{}
	for _, item := range listOf(objectOf(data).member("data")) {
		id := objectOf(item).Str("id")
		ids[id] = id
		ids[NormalizeNvidiaModelID(id)] = id
	}
	return ids, nil
}

// isRadiusGatewayModel is radius-config.ts isRadiusGatewayModel.
func isRadiusGatewayModel(value any) bool {
	model, ok := value.(*Obj)
	if !ok {
		return false
	}
	_, id := model.member("id").(string)
	_, name := model.member("name").(string)
	_, reasoning := model.member("reasoning").(bool)
	_, input := model.member("input").([]any)
	_, cost := model.member("cost").(*Obj)
	_, contextWindow := model.member("contextWindow").(float64)
	_, maxTokens := model.member("maxTokens").(float64)
	return id && name && reasoning && input && cost && contextWindow && maxTokens
}

func truncateHTTPBody(body string) string {
	trimmed := strings.Trim(body, jsWhitespace)
	if units := utf16Length(trimmed); units > 512 {
		return ownedSlice(trimmed, 512) + "…"
	}
	return trimmed
}

func fetchRadiusModels() ([]*Obj, error) {
	gateway := DefaultRadiusGateway
	response, err := fetchBody(gateway+"/v1/config", map[string]string{"accept": "application/json"})
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, fmt.Errorf("Could not load Radius config from %s: %d: %s", gateway, response.StatusCode, truncateHTTPBody(string(data)))
	}
	parsed, err := ParseJSON(data)
	if err != nil {
		return nil, err
	}
	config := objectOf(parsed)
	baseURL, hasBaseURL := config.member("baseUrl").(string)
	listed, hasModels := config.member("models").([]any)
	if !hasBaseURL || !hasModels {
		return nil, fmt.Errorf("Invalid Radius config from %s", gateway)
	}
	models := []*Obj{}
	for _, item := range listed {
		if isRadiusGatewayModel(item) {
			models = append(models, Spread(item.(*Obj), O("api", "pi-messages", "provider", "radius", "baseUrl", baseURL)))
		}
	}
	if len(models) == 0 {
		return nil, errors.New("Radius API returned no models")
	}
	return models, nil
}

// LoadCatalogSources fetches every upstream source. In strict mode any failure aborts generation; otherwise a failed source
// contributes nothing.
func LoadCatalogSources(strict bool) (*CatalogSources, error) {
	load := func(label string, fetch func() error) error {
		fmt.Printf("Fetching %s...\n", label)
		err := fetch()
		if err == nil {
			return nil
		}
		fmt.Fprintf(os.Stderr, "Failed to fetch %s: Error: %v\n", label, err)
		if strict {
			return err
		}
		return nil
	}

	sources := &CatalogSources{
		ModelsDev:          NewObj(),
		ModelsDevDecisions: map[string]ModelsDevDecisionMetadata{},
		OpenRouter:         OpenRouterCatalog{Chat: []*Obj{}, Images: []*Obj{}, Classifiers: []*Obj{}},
		AIGateway:          AiGatewayCatalog{Chat: []*Obj{}, Classifiers: []*Obj{}},
		NvidiaModelIDs:     map[string]string{},
	}
	if err := load("models.dev catalog", func() error {
		data, err := fetchJSON(ModelsDevURL, "models.dev API")
		if err != nil {
			return err
		}
		catalog, ok := data.(*Obj)
		if !ok {
			return errors.New("models.dev API returned a catalog that is not an object")
		}
		sources.ModelsDev = catalog
		return nil
	}); err != nil {
		return nil, err
	}
	if err := load("models.dev decision models", func() error {
		data, err := fetchJSON(ModelsDevDecisionsURL, "models.dev decision API")
		if err != nil {
			return err
		}
		decisions := objectOf(data)
		for _, key := range decisions.Keys() {
			entry := objectOf(decisions.vals[key])
			sources.ModelsDevDecisions[key] = ModelsDevDecisionMetadata{
				ID: entry.Str("id"), Type: entry.Str("type"), Name: entry.Str("name"),
				ContextLimit: objectOf(entry.member("limit")).Num("context"),
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if objectOf(sources.ModelsDev.member("nvidia")).member("models") != nil {
		if err := load("NVIDIA NIM models", func() error {
			ids, err := fetchNvidiaModelIDs()
			if err == nil {
				sources.NvidiaModelIDs = ids
			}
			return err
		}); err != nil {
			return nil, err
		}
	}
	if err := load("OpenRouter models", func() error {
		catalog, err := FetchOpenRouterCatalog()
		if err == nil {
			sources.OpenRouter = catalog
		}
		return err
	}); err != nil {
		return nil, err
	}
	if strict && len(sources.OpenRouter.Images) == 0 {
		return nil, errors.New("OpenRouter API returned no usable image models")
	}
	if err := load("Vercel AI Gateway models", func() error {
		catalog, err := FetchAiGatewayCatalog()
		if err == nil {
			sources.AIGateway = catalog
		}
		return err
	}); err != nil {
		return nil, err
	}
	if err := load("Radius models", func() error {
		models, err := fetchRadiusModels()
		if err == nil {
			sources.Radius = models
		}
		return err
	}); err != nil {
		return nil, err
	}
	return sources, nil
}
