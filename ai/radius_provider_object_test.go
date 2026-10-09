package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

// radius.ts radiusProvider returns the Pi Provider<"pi-messages"> object (models.ts:150-215): id, name, auth, getModels, refreshModels, stream and streamSimple.
func TestRadiusProviderIsThePiProviderObject(t *testing.T) {
	var value any = NewRadiusProvider(RadiusProviderOptions{})
	provider, ok := value.(*ModelsProvider)
	if !ok {
		t.Fatalf("NewRadiusProvider returns %T, want the Pi Provider object *ModelsProvider", value)
	}
	if provider.ID != "radius" || provider.Name != "Radius" {
		t.Fatalf("id/name = %q/%q", provider.ID, provider.Name)
	}
	if provider.Auth.APIKey == nil || provider.Auth.OAuth == nil || provider.Auth.OAuth.Name != "Radius" {
		t.Fatalf("auth = %+v", provider.Auth)
	}
	if provider.GetModels == nil || provider.RefreshModels == nil || provider.Stream == nil || provider.StreamSimple == nil {
		t.Fatalf("missing members: getModels=%t refreshModels=%t stream=%t streamSimple=%t", provider.GetModels != nil, provider.RefreshModels != nil, provider.Stream != nil, provider.StreamSimple != nil)
	}
	models, err := provider.GetModels()
	if err != nil {
		t.Fatal(err)
	}
	balanced, found := findPiMessagesModel(models, "balanced")
	if !found || balanced.ProviderMeta.ProviderID != "radius" || balanced.ProviderMeta.API != APIPiMessages {
		t.Fatalf("baseline = %+v, %t", balanced, found)
	}
}

// radius.ts: the baseline is the published catalog only for the default gateway, and `{ ...model, provider: id }` rebinds it to the provider id.
func TestRadiusProviderObjectBaselineFollowsGatewayAndID(t *testing.T) {
	var custom any = NewRadiusProvider(RadiusProviderOptions{ID: "radius-dev", Name: "Dev", Gateway: "http://localhost:8788"})
	provider, ok := custom.(*ModelsProvider)
	if !ok {
		t.Fatalf("NewRadiusProvider returns %T", custom)
	}
	if models, _ := provider.GetModels(); len(models) != 0 || provider.ID != "radius-dev" || provider.Name != "Dev" {
		t.Fatalf("custom gateway: id=%q name=%q models=%d", provider.ID, provider.Name, len(models))
	}
	var rebound any = NewRadiusProvider(RadiusProviderOptions{ID: "radius-two"})
	provider, ok = rebound.(*ModelsProvider)
	if !ok {
		t.Fatalf("NewRadiusProvider returns %T", rebound)
	}
	models, _ := provider.GetModels()
	if len(models) == 0 {
		t.Fatal("default gateway has no baseline")
	}
	for _, model := range models {
		if model.ProviderMeta.ProviderID != "radius-two" {
			t.Fatalf("model %s provider = %q", model.ID, model.ProviderMeta.ProviderID)
		}
	}
}

// The Pi object registered in Models is the real path: refreshModels publishes the account's catalog through Models.refresh, and getModels then reports it instead of the baseline (radius.ts:29-31,57-77).
func TestRadiusProviderObjectRefreshesThroughModels(t *testing.T) {
	requests := routeRadiusGateway(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("content-type", "application/json")
		_, _ = writer.Write([]byte(radiusTestConfig))
	})
	credentials := NewInMemoryCredentialStore()
	if err := setRadiusKey(credentials); err != nil {
		t.Fatal(err)
	}
	store := NewInMemoryModelsStore()
	models := CreateModels(CreateModelsOptions{Credentials: credentials, ModelsStore: store})
	var value any = NewRadiusProvider(RadiusProviderOptions{})
	provider, ok := value.(*ModelsProvider)
	if !ok {
		t.Fatalf("NewRadiusProvider returns %T", value)
	}
	models.SetProvider(provider)
	if len(models.GetModels("radius")) < 3 {
		t.Fatalf("baseline catalog not visible through Models: %d", len(models.GetModels("radius")))
	}

	if result := models.Refresh(context.Background()); len(result.Errors) != 0 || result.Aborted {
		t.Fatalf("refresh = %+v", result)
	}

	var ids []string
	for _, model := range models.GetModels("radius") {
		ids = append(ids, model.ID)
	}
	if !slices.Equal(ids, []string{"balanced", "organization-only"}) {
		t.Fatalf("models after refresh = %v", ids)
	}
	if recorded := requests(); len(recorded) != 1 || recorded[0].Authorization != "Bearer radius-key" {
		t.Fatalf("requests = %+v", recorded)
	}
	if stored, _ := store.Read(context.Background(), "radius"); stored == nil || len(stored.Models) != 2 {
		t.Fatalf("stored = %+v", stored)
	}
}

// radius.ts stream/streamSimple are piMessagesApi() streams: a request through Models reaches the pi-messages gateway instead of failing with "no API implementation".
func TestRadiusProviderObjectStreamsPiMessages(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.Error(writer, "gateway down", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	credentials := NewInMemoryCredentialStore()
	if err := setRadiusKey(credentials); err != nil {
		t.Fatal(err)
	}
	models := CreateModels(CreateModelsOptions{Credentials: credentials})
	var value any = NewRadiusProvider(RadiusProviderOptions{})
	provider, ok := value.(*ModelsProvider)
	if !ok {
		t.Fatalf("NewRadiusProvider returns %T", value)
	}
	models.SetProvider(provider)
	model := models.GetModel("radius", "balanced")
	if model == nil {
		t.Fatal("no balanced model")
	}
	pointed := *model
	pointed.ProviderMeta.BaseURL = server.URL
	for name, run := range map[string]func() *AssistantMessageEventStream{
		"stream": func() *AssistantMessageEventStream {
			return models.Stream(context.Background(), &pointed, Context{Messages: []Message{UserMessage{Content: UserText("hi")}}})
		},
		"streamSimple": func() *AssistantMessageEventStream {
			return models.StreamSimple(context.Background(), &pointed, Context{Messages: []Message{UserMessage{Content: UserText("hi")}}})
		},
	} {
		before := hits.Load()
		result, err := run().ResultContext(context.Background())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(result.ErrorMessage, "no API implementation") || hits.Load() == before {
			t.Fatalf("%s did not reach the pi-messages gateway: hits=%d error=%q", name, hits.Load()-before, result.ErrorMessage)
		}
	}
}

func setRadiusKey(store *InMemoryCredentialStore) error {
	_, err := store.Modify(context.Background(), "radius", func(*Credential) (*Credential, error) {
		return &Credential{Type: CredentialAPIKey, Key: "radius-key"}, nil
	})
	return err
}

// radius.ts:44: a stored catalog restores only the models whose provider is this provider's id.
func TestRadiusProviderObjectRestoresOnlyItsOwnStoredModels(t *testing.T) {
	routeRadiusGateway(t, func(http.ResponseWriter, *http.Request) { t.Error("offline refresh contacted the gateway") })
	own := GetRadiusModelsFromConfig("radius", radiusTestGatewayConfig(t))
	foreign := GetRadiusModelsFromConfig("other", radiusTestGatewayConfig(t))
	stored := radiusStoreEntry(append(foreign[:1:1], own[1]))
	var value any = NewRadiusProvider(RadiusProviderOptions{})
	provider, ok := value.(*ModelsProvider)
	if !ok {
		t.Fatalf("NewRadiusProvider returns %T", value)
	}
	published := false
	err := provider.RefreshModels(RefreshModelsContext{Stored: stored, Signal: context.Background(), Publish: func(publication ModelsPublication) (bool, error) {
		publication.Update()
		published = true
		return true, nil
	}})
	if err != nil || !published {
		t.Fatalf("refresh = %v, published %t", err, published)
	}
	models, _ := provider.GetModels()
	if len(models) != 1 || models[0].ID != own[1].ID {
		t.Fatalf("restored models = %v, want only %s", len(models), own[1].ID)
	}
}
