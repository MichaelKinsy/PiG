package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRadiusClassifierSupport(t *testing.T) {
	var decisionCalls int
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/config":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"baseUrl": "http://" + r.Host + "/v1",
				"models": []map[string]any{
					{
						"id": "chat-1", "name": "Chat 1", "reasoning": false,
						"input": []string{"text"}, "cost": map[string]any{"input": 1, "output": 2, "cacheRead": 0, "cacheWrite": 0},
						"contextWindow": 128000, "maxTokens": 4096,
					},
				},
				"decision": []map[string]any{
					{
						"id": "cls-1", "name": "Classifier 1",
						"input":         []string{"text", "image"},
						"inputLimits":   map[string]any{"maxRequestBytes": 1048576},
						"cost":          map[string]any{"input": 0.05, "output": 0, "cacheRead": 0, "cacheWrite": 0},
						"contextWindow": 32768,
						"headers":       map[string]string{"X-Gateway-Role": "decision"},
						"compat":        map[string]any{"imageInput": "assets", "sendImageOptInHeader": true},
					},
					{
						"id": 123, // invalid, should be filtered out
					},
				},
			})
		case "/v1/decision":
			decisionCalls++
			gotHeader = r.Header.Get("X-Gateway-Role")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"answers":{"ok":{"type":"bool","probability":0.75}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	provider := NewRadiusProvider(RadiusProviderOptions{ID: "radius-test", Name: "Radius Test", Gateway: srv.URL})
	var persisted *ModelsStoreEntry
	err := provider.RefreshModels(RefreshModelsContext{
		Signal:       context.Background(),
		AllowNetwork: true,
		Credential:   &Credential{Type: CredentialAPIKey, Key: "tok"},
		Publish: func(pub ModelsPublication) (bool, error) {
			persisted = pub.Persist
			if pub.Update != nil {
				pub.Update()
			}
			return true, nil
		},
	})
	if err != nil {
		t.Fatalf("RefreshModels: %v", err)
	}
	chatModels, err := provider.GetModels()
	if err != nil || len(chatModels) != 1 || chatModels[0].ID != "chat-1" {
		t.Fatalf("GetModels = %+v, %v", chatModels, err)
	}
	allModels, err := provider.GetAllModels()
	if err != nil || len(allModels) != 2 {
		t.Fatalf("GetAllModels = %+v, %v", allModels, err)
	}
	cls, ok := allModels[1].(*ClassifierModel)
	if !ok || cls.ID != "cls-1" || cls.API != ClassifierAPIPiDecision || cls.Provider != "radius-test" {
		t.Fatalf("classifier = %#v", allModels[1])
	}
	// M6: Headers, InputLimits, and Compat survive sanitization and binding.
	if cls.Headers["X-Gateway-Role"] != "decision" {
		t.Fatalf("expected Headers to survive, got %v", cls.Headers)
	}
	if cls.InputLimits == nil || cls.InputLimits.MaxRequestBytes != 1048576 {
		t.Fatalf("expected InputLimits to survive, got %+v", cls.InputLimits)
	}
	if cls.Compat == nil || cls.Compat.ImageInput != SystemOneImageInputAssets || cls.Compat.SendImageOptInHeader == nil || !*cls.Compat.SendImageOptInHeader {
		t.Fatalf("expected Compat to survive, got %+v", cls.Compat)
	}

	if provider.Classify == nil {
		t.Fatal("expected Classify registered on Radius provider")
	}
	res, err := provider.Classify(context.Background(), cls, ClassifierContext{
		State:     JsonObject{"a": 1},
		Questions: ClassifierQuestions{{ID: "ok", Question: ClassifierBoolQuestion{Instructions: "ok?", Criteria: ClassifierBoolCriteria{True: "y", False: "n"}}}},
	}, ClassifierOptions{APIKey: "tok", APIKeySet: true})
	if err != nil || res.StopReason != ClassifierStopReasonStop || decisionCalls != 1 || gotHeader != "decision" {
		t.Fatalf("classify = %+v, err=%v, calls=%d, header=%q", res, err, decisionCalls, gotHeader)
	}

	// M8: Offline restore through storedRadiusAllModels preserves both chat and classifier models.
	if persisted == nil || len(persisted.Models) != 2 {
		t.Fatalf("persisted = %+v", persisted)
	}
	restoredProvider := NewRadiusProvider(RadiusProviderOptions{ID: "radius-test", Name: "Radius Test", Gateway: srv.URL})
	err = restoredProvider.RefreshModels(RefreshModelsContext{
		Signal:       context.Background(),
		AllowNetwork: false,
		Stored:       persisted,
		Publish: func(pub ModelsPublication) (bool, error) {
			if pub.Update != nil {
				pub.Update()
			}
			return true, nil
		},
	})
	if err != nil {
		t.Fatalf("offline restore RefreshModels: %v", err)
	}
	restoredAll, err := restoredProvider.GetAllModels()
	if err != nil || len(restoredAll) != 2 {
		t.Fatalf("restored GetAllModels = %+v, %v", restoredAll, err)
	}
	if _, ok := restoredAll[1].(*ClassifierModel); !ok || restoredAll[1].ModelID() != "cls-1" {
		t.Fatalf("restored classifier = %#v", restoredAll[1])
	}

	// M5: Legacy gatewayConfig import only imports chat models (GetRadiusModels), matching Pi f25aecaa1.
	legacyCred := &Credential{
		Type: CredentialOAuth,
		GatewayConfig: json.RawMessage(`{
			"baseUrl": "https://legacy.radius.test/v1",
			"models": [{"id":"legacy-chat","name":"Legacy","reasoning":false,"input":["text"],"cost":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0},"contextWindow":8192,"maxTokens":1024}],
			"decision": [{"id":"legacy-cls","name":"Legacy Cls","input":["text"],"cost":{"input":0.1,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":8192}]
		}`),
	}
	legacyProvider := NewRadiusProvider(RadiusProviderOptions{ID: "radius-legacy", Name: "Radius Legacy", Gateway: "https://legacy.radius.test"})
	err = legacyProvider.RefreshModels(RefreshModelsContext{
		Signal:       context.Background(),
		AllowNetwork: false,
		Credential:   legacyCred,
		Publish: func(pub ModelsPublication) (bool, error) {
			if pub.Update != nil {
				pub.Update()
			}
			return true, nil
		},
	})
	if err != nil {
		t.Fatalf("legacy RefreshModels: %v", err)
	}
	legacyAll, err := legacyProvider.GetAllModels()
	if err != nil || len(legacyAll) != 1 || legacyAll[0].ModelID() != "legacy-chat" {
		t.Fatalf("legacy import should import chat models only, got %+v, %v", legacyAll, err)
	}
}
