package codingagent

// Ports packages/coding-agent/src/core/remote-catalog-provider.ts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsonparse"
	"github.com/MichaelKinsy/PiG/internal/managementhttp"
)

// DefaultCatalogBaseURL is the catalog endpoint of the built-in providers.
// pig divergence (D64): PiG reads the remote catalog overlay from its own pi-in-go.dev instead of Pi's https://pi.dev
// (remote-catalog-provider.ts:DEFAULT_CATALOG_BASE_URL). An endpoint that does not serve the catalog (404 or 501) leaves the bundled catalog.
const DefaultCatalogBaseURL = "https://pi-in-go.dev"

const remoteCatalogAttemptTimeout = 4 * time.Second

// RemoteCatalogRefreshInterval is how long a checked catalog stays fresh (remote-catalog-provider.ts:REMOTE_CATALOG_REFRESH_INTERVAL_MS).
const RemoteCatalogRefreshInterval = 4 * time.Hour

// RemoteCatalogModelTypes are the model types this client can consume. They are sent as `?types=` so the catalog server returns the
// full-type shard instead of the chat-only one served to clients that predate model types. A server that ignores the parameter still
// returns the chat-only shard, which this client handles unchanged.
// upstream: remote-catalog-provider.ts:14-19 (REMOTE_CATALOG_MODEL_TYPES)
var RemoteCatalogModelTypes = []ai.ModelType{ai.ModelTypeChat, ai.ModelTypeImage, ai.ModelTypeClassifier}

// builtinModelDataGeneratedAt is the generation time, in Unix milliseconds, of the bundled catalogs, or nil when the data manifest records none: a stored remote catalog applies only when its `Last-Modified` is later.
// upstream: packages/coding-agent/src/core/model-runtime.ts:225 (builtinProviderCatalog.getBuiltinModelDataGeneratedAt)
func builtinModelDataGeneratedAt() *float64 { return ai.GetBuiltinModelDataGeneratedAt() }

// remoteCatalogOverlay holds the models the remote catalog adds to one provider. The registry reads it for providers it composes itself.
type remoteCatalogOverlay struct {
	mu     sync.Mutex
	models []ai.AnyModel
}

func (o *remoteCatalogOverlay) set(models []ai.AnyModel) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.models = models
}

// snapshot returns the overlay models of every type.
func (o *remoteCatalogOverlay) snapshot() []ai.AnyModel {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.models)
}

func (o *remoteCatalogOverlay) chat() []ai.AnyModel {
	return slices.DeleteFunc(o.snapshot(), func(model ai.AnyModel) bool { return !ai.IsModelType(model, ai.ModelTypeChat) })
}

// mergeRemoteCatalogModels lets a dynamic model replace the baseline model of the same type and id in place, and appends the rest.
// upstream: remote-catalog-provider.ts:mergeModels
func mergeRemoteCatalogModels(baseline, dynamic []ai.AnyModel) []ai.AnyModel {
	type key struct {
		modelType ai.ModelType
		id        string
	}
	merged := make([]ai.AnyModel, 0, len(baseline)+len(dynamic))
	index := make(map[key]int, len(baseline)+len(dynamic))
	for _, model := range slices.Concat(baseline, dynamic) {
		k := key{ai.GetModelType(model), model.ModelID()}
		if at, ok := index[k]; ok {
			merged[at] = model
			continue
		}
		index[k] = len(merged)
		merged = append(merged, model)
	}
	return merged
}

func supportedRemoteModelType(raw json.RawMessage) bool {
	if raw == nil {
		return true
	}
	var modelType string
	if json.Unmarshal(raw, &modelType) != nil {
		return false
	}
	return slices.Contains(RemoteCatalogModelTypes, ai.ModelType(modelType))
}

// orderedObjectValues returns the values of a JSON object in document order.
func orderedObjectValues(data json.RawMessage) ([]json.RawMessage, map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if _, err := decoder.Token(); err != nil {
		return nil, nil, err
	}
	var values []json.RawMessage
	byKey := map[string]json.RawMessage{}
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			return nil, nil, err
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, nil, err
		}
		values = append(values, value)
		byKey[name.(string)] = value
	}
	return values, byKey, nil
}

// parseRemoteCatalog reads a catalog given as an array, as `{models: [...]}`, or as an object keyed by model id, keeps the entries with an id
// and a supported model type, and sets each entry's provider. Entries stay raw JSON so the store holds what the server sent.
// upstream: remote-catalog-provider.ts:parseCatalog
func parseRemoteCatalog(providerID string, body []byte) ([]json.RawMessage, error) {
	invalid := fmt.Errorf("Invalid model catalog for provider %q", providerID)
	// response.json() rejects with JSON.parse's SyntaxError before parseCatalog runs.
	if !json.Valid(body) {
		if err := jsonparse.Validate(body); err != nil {
			return nil, err
		}
		return nil, errors.New("Unexpected end of JSON input")
	}
	var trimmed = bytes.TrimSpace(body)
	var entries []json.RawMessage
	switch {
	case bytes.HasPrefix(trimmed, []byte("[")):
		if err := json.Unmarshal(trimmed, &entries); err != nil {
			return nil, err
		}
	case bytes.HasPrefix(trimmed, []byte("{")):
		values, byKey, err := orderedObjectValues(trimmed)
		if err != nil {
			return nil, err
		}
		if models, ok := byKey["models"]; ok && bytes.HasPrefix(bytes.TrimSpace(models), []byte("[")) {
			if err := json.Unmarshal(models, &entries); err != nil {
				return nil, err
			}
		} else {
			entries = values
		}
	default:
		return nil, invalid
	}
	provider, err := json.Marshal(providerID)
	if err != nil {
		return nil, err
	}
	out := make([]json.RawMessage, 0, len(entries))
	for _, entry := range entries {
		var fields map[string]json.RawMessage
		if json.Unmarshal(entry, &fields) != nil || fields == nil {
			continue
		}
		if _, hasID := fields["id"]; !hasID || !supportedRemoteModelType(fields["type"]) {
			continue
		}
		fields["provider"] = provider
		encoded, err := json.Marshal(fields)
		if err != nil {
			return nil, err
		}
		out = append(out, encoded)
	}
	return out, nil
}

// encodeURIComponent escapes every byte of the UTF-8 encoding except A-Z a-z 0-9 - _ . ! ~ * ' ( ), as JavaScript's encodeURIComponent does.
func encodeURIComponent(value string) string {
	const upperhex = "0123456789ABCDEF"
	var encoded strings.Builder
	for i := range len(value) {
		c := value[i]
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte("-_.!~*'()", c) >= 0 {
			encoded.WriteByte(c)
			continue
		}
		encoded.WriteByte('%')
		encoded.WriteByte(upperhex[c>>4])
		encoded.WriteByte(upperhex[c&15])
	}
	return encoded.String()
}

// remoteCatalogModels are the stored overlay models, or none when the bundled catalog is at least as new as the stored catalog.
// upstream: remote-catalog-provider.ts:remoteModels
func remoteCatalogModels(entry *ai.ModelsStoreEntry, providerID string, localGeneratedAt *float64) ([]ai.AnyModel, error) {
	if entry == nil {
		return nil, nil
	}
	if localGeneratedAt != nil && (entry.LastModified == nil || *entry.LastModified <= *localGeneratedAt) {
		return nil, nil
	}
	return ai.DecodeModelsCatalog(entry.Models, providerID)
}

// WithRemoteCatalog adds a persisted catalog overlay to a static built-in provider. localGeneratedAt is the generation time, in Unix
// milliseconds, of the bundled catalog; nil means a stored catalog is always newer.
// upstream: remote-catalog-provider.ts:62-168 (withRemoteCatalog)
func WithRemoteCatalog(provider *ai.ModelsProvider, catalogBaseURL string, localGeneratedAt *float64) *ai.ModelsProvider {
	return withRemoteCatalogOverlay(provider, catalogBaseURL, localGeneratedAt, &remoteCatalogOverlay{})
}

func withRemoteCatalogOverlay(provider *ai.ModelsProvider, catalogBaseURL string, localGeneratedAt *float64, overlay *remoteCatalogOverlay) *ai.ModelsProvider {
	if catalogBaseURL == "" {
		catalogBaseURL = DefaultCatalogBaseURL
	}
	wrapped := *provider
	wrapped.GetModels = func() ([]*ai.Model, error) {
		baseline, err := provider.GetModels()
		if err != nil {
			return nil, err
		}
		merged := mergeRemoteCatalogModels(ai.AnyModels(baseline), overlay.chat())
		models := make([]*ai.Model, 0, len(merged))
		for _, model := range merged {
			models = append(models, model.(*ai.Model))
		}
		return models, nil
	}
	wrapped.GetAllModels = func() ([]ai.AnyModel, error) {
		var baseline []ai.AnyModel
		if provider.GetAllModels != nil {
			all, err := provider.GetAllModels()
			if err != nil {
				return nil, err
			}
			baseline = all
		} else {
			chat, err := provider.GetModels()
			if err != nil {
				return nil, err
			}
			baseline = ai.AnyModels(chat)
		}
		return mergeRemoteCatalogModels(baseline, overlay.snapshot()), nil
	}
	wrapped.RefreshModels = func(refresh ai.RefreshModelsContext) error {
		return refreshRemoteCatalog(provider.ID, catalogBaseURL, localGeneratedAt, overlay, refresh)
	}
	return &wrapped
}

func refreshRemoteCatalog(providerID, catalogBaseURL string, localGeneratedAt *float64, overlay *remoteCatalogOverlay, refresh ai.RefreshModelsContext) error {
	stored := refresh.Stored
	// A stored overlay that no longer decodes is dropped: the network refresh below replaces it.
	restored, _ := remoteCatalogModels(stored, providerID, localGeneratedAt)
	published, err := refresh.Publish(ai.ModelsPublication{Update: func() { overlay.set(restored) }})
	if err != nil || !published {
		return err
	}
	signal := refresh.Signal
	if signal == nil {
		signal = context.Background()
	}
	if !refresh.AllowNetwork || signal.Err() != nil {
		return nil
	}
	now := func() float64 { return float64(time.Now().UnixMilli()) }
	forced := refresh.Force != nil && *refresh.Force
	if !forced && stored != nil && stored.CheckedAt != nil && stored.LastModified != nil &&
		now()-*stored.CheckedAt < float64(RemoteCatalogRefreshInterval.Milliseconds()) {
		return nil
	}

	// Only revalidate when a cached body backs the validator, so a 304 can never leave the overlay empty.
	validator := ""
	if stored != nil && len(stored.Models) > 0 {
		validator = stored.ETag
	}
	// new URL("/api/models/providers/<id>", catalogBaseUrl): the absolute route replaces the base URL's path and query.
	baseURL, err := url.Parse(catalogBaseURL)
	if err != nil {
		return err
	}
	route, err := url.Parse("/api/models/providers/" + encodeURIComponent(providerID))
	if err != nil {
		return err
	}
	requestURL := baseURL.ResolveReference(route)
	types := make([]string, 0, len(RemoteCatalogModelTypes))
	for _, modelType := range RemoteCatalogModelTypes {
		types = append(types, string(modelType))
	}
	query := requestURL.Query()
	query.Set("types", strings.Join(types, ","))
	requestURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(signal, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return err
	}
	request.Header.Set("accept", "application/json")
	// pig divergence (D65): PiG identifies as pig/<version>, where Pi sends pi/<version> (remote-catalog-provider.ts:getPiUserAgent).
	request.Header.Set("User-Agent", ai.PiUserAgent())
	if validator != "" {
		request.Header.Set("if-none-match", validator)
	}
	response, err := managementhttp.FetchWithRetry(nil, request, managementhttp.FetchRetryOptions{AttemptTimeout: remoteCatalogAttemptTimeout})
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if signal.Err() != nil {
		return nil
	}
	checkedAt := now()
	withStored := func() ai.ModelsStoreEntry {
		if stored == nil {
			return ai.ModelsStoreEntry{Models: []ai.AnyModel{}}
		}
		return stored.Clone()
	}
	// Unchanged: the overlay already holds the stored catalog, so only the freshness window moves.
	if response.StatusCode == http.StatusNotModified && stored != nil {
		entry := withStored()
		entry.CheckedAt = &checkedAt
		_, err := refresh.Publish(ai.ModelsPublication{Persist: &entry})
		return err
	}
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusNotImplemented {
		entry := withStored()
		entry.CheckedAt, entry.LastModified, entry.ETag = &checkedAt, new(float64), ""
		_, err := refresh.Publish(ai.ModelsPublication{Persist: &entry})
		return err
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		// Transient failure: the cached body and its validator stay valid, so keep the etag and let the next refresh revalidate
		// instead of downloading the catalog.
		entry := withStored()
		entry.CheckedAt = &checkedAt
		if _, err := refresh.Publish(ai.ModelsPublication{Persist: &entry}); err != nil {
			return err
		}
		return fmt.Errorf("Model catalog request failed for %s: %d", providerID, response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	records, err := parseRemoteCatalog(providerID, body)
	if err != nil {
		return err
	}
	models, err := ai.DecodeStoredModels(records)
	if err != nil {
		return err
	}
	// Date.parse(response.headers.get("last-modified") ?? ""), NaN stored as 0: any date text V8 reads counts, not only HTTP-date forms.
	lastModified := ai.DateParse(response.Header.Get("last-modified"))
	if math.IsNaN(lastModified) {
		lastModified = 0
	}
	if signal.Err() != nil {
		return nil
	}
	entry := ai.ModelsStoreEntry{Models: models, CheckedAt: &checkedAt, LastModified: &lastModified, ETag: response.Header.Get("etag")}
	overlayModels, err := remoteCatalogModels(&entry, providerID, localGeneratedAt)
	if err != nil {
		return err
	}
	_, err = refresh.Publish(ai.ModelsPublication{Persist: &entry, Update: func() { overlay.set(overlayModels) }})
	return err
}
