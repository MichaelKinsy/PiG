package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync"
	"testing"
)

const radiusTestConfig = `{"baseUrl":"https://radius.example/v1","models":[
	{"id":"balanced","name":"Fresh Balanced","reasoning":true,"input":["text"],"cost":{"input":1,"output":2,"cacheRead":0.1,"cacheWrite":0},"contextWindow":424242,"maxTokens":32000},
	{"id":"organization-only","name":"Organization Only","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":16000}
]}`

func radiusTestGatewayConfig(t *testing.T) RadiusGatewayConfig {
	t.Helper()
	config, ok := sanitizeRadiusGatewayConfig(json.RawMessage(radiusTestConfig))
	if !ok {
		t.Fatal("invalid test config")
	}
	return config
}

// radiusGatewayRequest is one request seen by routeRadiusGateway.
type radiusGatewayRequest struct {
	URL           string
	Authorization string
}

// routeRadiusGateway sends every Radius gateway request (including the default
// https://radius.pi.dev) to handler, recording the original URL. It mirrors
// upstream tests that stub globalThis.fetch.
func routeRadiusGateway(t *testing.T, handler http.HandlerFunc) func() []radiusGatewayRequest {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	var mu sync.Mutex
	var requests []radiusGatewayRequest
	original := radiusHTTPClient
	radiusHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		requests = append(requests, radiusGatewayRequest{URL: request.URL.String(), Authorization: request.Header.Get("authorization")})
		mu.Unlock()
		routed := request.Clone(request.Context())
		routed.URL.Scheme, routed.URL.Host, routed.Host = target.Scheme, target.Host, ""
		return http.DefaultTransport.RoundTrip(routed)
	})}
	t.Cleanup(func() { radiusHTTPClient = original })
	return func() []radiusGatewayRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]radiusGatewayRequest(nil), requests...)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

// refreshRadiusForTest runs the two upstream Models.refresh phases against an
// in-memory store: restore offline, then refresh with credential if allowed.
func refreshRadiusForTest(t *testing.T, provider *RadiusProvider, store ModelsStore, credential *Credential, allowNetwork bool) {
	t.Helper()
	ctx := context.Background()
	publish := func(publication ModelsPublication) (bool, error) {
		if publication.Persist != nil {
			if err := store.Write(ctx, provider.ID(), *publication.Persist); err != nil {
				return false, err
			}
		}
		publication.Update()
		return true, nil
	}
	for _, network := range []bool{false, allowNetwork} {
		stored, err := store.Read(ctx, provider.ID())
		if err != nil {
			t.Fatal(err)
		}
		if err := provider.RefreshModels(ctx, RefreshModelsContext{Credential: credential, Stored: stored, AllowNetwork: network, Publish: publish}); err != nil {
			t.Fatal(err)
		}
	}
}

func findPiMessagesModel(models []*Model, id string) (*Model, bool) {
	for _, model := range models {
		if model.ID == id {
			return model, true
		}
	}
	return &Model{}, false
}

// Ported from radius-provider.test.ts "ships a static public catalog for the default gateway".
func TestRadiusProviderShipsPublicCatalogForDefaultGateway(t *testing.T) {
	provider := NewRadiusGatewayProvider(RadiusProviderOptions{})
	balanced, ok := findPiMessagesModel(provider.GetModels(), "balanced")
	if !ok || balanced.ProviderMeta.ProviderID != "radius" || balanced.ProviderMeta.API != APIPiMessages || provider.Name() != "Radius" || provider.Gateway() != DefaultRadiusGateway {
		t.Fatalf("balanced = %+v, %t", balanced, ok)
	}
}

// Ported from radius-provider.test.ts "does not apply the public Radius catalog to custom gateways".
func TestRadiusProviderCustomGatewayHasNoPublicCatalog(t *testing.T) {
	provider := NewRadiusGatewayProvider(RadiusProviderOptions{ID: "radius-dev", Gateway: "http://localhost:8788"})
	if models := provider.GetModels(); len(models) != 0 {
		t.Fatalf("custom gateway models = %+v", models)
	}
	if provider.OAuth().ID() != "radius-dev" || provider.OAuth().Gateway() != "http://localhost:8788" {
		t.Fatalf("custom gateway OAuth = %s %s", provider.OAuth().ID(), provider.OAuth().Gateway())
	}
}

// Ported from radius-provider.test.ts "replaces the static public catalog with refreshed models" (Pi 1.1.0: the account's catalog replaces the shipped baseline, radius.ts getModels).
func TestRadiusProviderOverlaysRefreshedModels(t *testing.T) {
	requests := routeRadiusGateway(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("content-type", "application/json")
		_, _ = writer.Write([]byte(radiusTestConfig))
	})
	provider := NewRadiusGatewayProvider(RadiusProviderOptions{})
	store := NewInMemoryModelsStore()

	refreshRadiusForTest(t, provider, store, &Credential{Type: CredentialAPIKey, Key: "radius-key"}, true)

	balanced, _ := findPiMessagesModel(provider.GetModels(), "balanced")
	if balanced.DisplayName != "Fresh Balanced" || balanced.ProviderMeta.BaseURL != "https://radius.example/v1" || balanced.Capabilities.ContextWindow != 424242 {
		t.Fatalf("balanced = %+v", balanced)
	}
	if _, ok := findPiMessagesModel(provider.GetModels(), "organization-only"); !ok {
		t.Fatal("organization-only missing")
	}
	var ids []string
	for _, model := range provider.GetModels() {
		ids = append(ids, model.ID)
	}
	if !slices.Equal(ids, []string{"balanced", "organization-only"}) {
		t.Fatalf("models = %v, want the refreshed catalog only", ids)
	}
	recorded := requests()
	if len(recorded) != 1 || recorded[0].URL != "https://radius.pi.dev/v1/config" || recorded[0].Authorization != "Bearer radius-key" {
		t.Fatalf("requests = %+v", recorded)
	}
	if stored, _ := store.Read(context.Background(), "radius"); stored == nil || len(stored.Models) != 2 || stored.CheckedAt == nil {
		t.Fatalf("stored = %+v", stored)
	}
}

// Ported from radius-provider.test.ts "exposes no models when the organization disabled all of them": an empty effective catalog replaces the shipped one.
func TestRadiusProviderExposesNoModelsWhenTheOrganizationDisabledAll(t *testing.T) {
	routeRadiusGateway(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("content-type", "application/json")
		_, _ = writer.Write([]byte(`{"baseUrl":"https://radius.example/v1","models":[]}`))
	})
	provider := NewRadiusGatewayProvider(RadiusProviderOptions{})
	store := NewInMemoryModelsStore()
	if len(provider.GetModels()) == 0 {
		t.Fatal("the shipped catalog is empty before the refresh")
	}

	refreshRadiusForTest(t, provider, store, &Credential{Type: CredentialAPIKey, Key: "radius-key"}, true)

	if models := provider.GetModels(); len(models) != 0 {
		t.Fatalf("models after the organization disabled all of them = %d, want 0", len(models))
	}
}

// Ported from radius-provider.test.ts "replaces the static public catalog with a cached catalog without network access".
func TestRadiusProviderRestoresStoredCatalogOffline(t *testing.T) {
	routeRadiusGateway(t, func(http.ResponseWriter, *http.Request) { t.Error("offline refresh contacted the gateway") })
	store := NewInMemoryModelsStore()
	if err := store.Write(context.Background(), "radius", *radiusStoreEntry(GetRadiusModelsFromConfig("radius", radiusTestGatewayConfig(t)))); err != nil {
		t.Fatal(err)
	}
	provider := NewRadiusGatewayProvider(RadiusProviderOptions{})

	refreshRadiusForTest(t, provider, store, nil, false)

	if balanced, _ := findPiMessagesModel(provider.GetModels(), "balanced"); balanced.DisplayName != "Fresh Balanced" {
		t.Fatalf("balanced = %+v", balanced)
	}
	if _, ok := findPiMessagesModel(provider.GetModels(), "organization-only"); !ok {
		t.Fatal("organization-only missing")
	}
}

func TestRadiusProviderImportsLegacyCredentialCatalogOnce(t *testing.T) {
	store := NewInMemoryModelsStore()
	provider := NewRadiusGatewayProvider(RadiusProviderOptions{})
	credential := &Credential{Type: CredentialOAuth, Access: "a", GatewayConfig: json.RawMessage(radiusTestConfig)}

	refreshRadiusForTest(t, provider, store, credential, false)

	stored, _ := store.Read(context.Background(), "radius")
	if stored == nil || len(stored.Models) != 2 {
		t.Fatalf("legacy catalog was not persisted: %+v", stored)
	}
	if model, _ := findPiMessagesModel(provider.GetModels(), "organization-only"); model.ProviderMeta.BaseURL != "https://radius.example/v1" {
		t.Fatalf("legacy model = %+v", model)
	}
}

func TestRadiusProviderStopsWhenPublicationIsSuperseded(t *testing.T) {
	routeRadiusGateway(t, func(http.ResponseWriter, *http.Request) { t.Error("superseded refresh contacted the gateway") })
	provider := NewRadiusGatewayProvider(RadiusProviderOptions{})
	stored := radiusStoreEntry(GetRadiusModelsFromConfig("radius", radiusTestGatewayConfig(t)))
	err := provider.RefreshModels(context.Background(), RefreshModelsContext{
		Credential: &Credential{Type: CredentialAPIKey, Key: "k"}, Stored: stored, AllowNetwork: true,
		Publish: func(ModelsPublication) (bool, error) { return false, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findPiMessagesModel(provider.GetModels(), "organization-only"); ok {
		t.Fatal("a superseded publication updated the catalog")
	}
}

// radius.ts:19-90 radiusProvider returns the Provider that providers/all.ts lists: id, name, auth, getModels, refreshModels, stream and
// streamSimple. The built-in provider list carries it, so Models restores and refreshes the Radius catalog through it.
func TestRadiusModelsProviderIsTheListedRadiusProvider(t *testing.T) {
	var listed *ModelsProvider
	for _, provider := range BuiltinProviders() {
		if provider.ID == RadiusProviderID {
			listed = provider
		}
	}
	if listed == nil || listed.Name != "Radius" || listed.Stream == nil || listed.StreamSimple == nil || listed.RefreshModels == nil || listed.Auth.APIKey == nil || listed.Auth.OAuth == nil || listed.Auth.OAuth.Name != "Radius" {
		t.Fatalf("listed radius provider = %+v", listed)
	}
	models, err := listed.GetModels()
	if balanced, ok := findPiMessagesModel(models, "balanced"); err != nil || !ok || balanced.ProviderMeta.API != APIPiMessages {
		t.Fatalf("baseline models = %d, %v", len(models), err)
	}

	routeRadiusGateway(t, func(http.ResponseWriter, *http.Request) { t.Error("offline refresh contacted the gateway") })
	ctx := context.Background()
	store := NewInMemoryModelsStore()
	if err := store.Write(ctx, "radius", *radiusStoreEntry(GetRadiusModelsFromConfig("radius", radiusTestGatewayConfig(t)))); err != nil {
		t.Fatal(err)
	}
	stored, err := store.Read(ctx, "radius")
	if err != nil {
		t.Fatal(err)
	}
	err = listed.RefreshModels(RefreshModelsContext{Stored: stored, Signal: ctx, Publish: func(publication ModelsPublication) (bool, error) {
		publication.Update()
		return true, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	models, _ = listed.GetModels()
	if balanced, _ := findPiMessagesModel(models, "balanced"); balanced.DisplayName != "Fresh Balanced" {
		t.Fatalf("restored balanced = %+v", balanced)
	}
	if _, ok := findPiMessagesModel(models, "organization-only"); !ok {
		t.Fatal("the stored catalog replaces the baseline: organization-only is missing")
	}

	custom := NewRadiusProvider(RadiusProviderOptions{ID: "radius-dev", Name: "Dev", Gateway: "http://localhost:8788"})
	if models, _ := custom.GetModels(); custom.ID != "radius-dev" || custom.Name != "Dev" || len(models) != 0 || custom.Auth.OAuth.Name != "Dev" {
		t.Fatalf("custom gateway provider = %+v with %d models", custom, len(models))
	}
}
