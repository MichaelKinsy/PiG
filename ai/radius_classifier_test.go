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
				"classifiers": []map[string]any{
					{
						"id": "cls-1", "name": "Classifier 1",
						"input": []string{"text", "image"}, "cost": map[string]any{"input": 0.05, "output": 0, "cacheRead": 0, "cacheWrite": 0},
						"contextWindow": 32768,
					},
					{
						"id": 123, // invalid, should be filtered out
					},
				},
			})
		case "/v1/decision":
			decisionCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"answers":{"ok":{"type":"bool","probability":0.75}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	provider := NewRadiusProvider(RadiusProviderOptions{ID: "radius-test", Name: "Radius Test", Gateway: srv.URL})
	var published func()
	err := provider.RefreshModels(RefreshModelsContext{
		Signal:       context.Background(),
		AllowNetwork: true,
		Credential:   &Credential{Type: CredentialAPIKey, Key: "tok"},
		Publish: func(pub ModelsPublication) (bool, error) {
			published = pub.Update
			if published != nil {
				published()
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
	if provider.Classify == nil {
		t.Fatal("expected Classify registered on Radius provider")
	}
	res, err := provider.Classify(context.Background(), cls, ClassifierContext{
		State:     JsonObject{"a": 1},
		Questions: ClassifierQuestions{{ID: "ok", Question: ClassifierBoolQuestion{Instructions: "ok?", Criteria: ClassifierBoolCriteria{True: "y", False: "n"}}}},
	}, ClassifierOptions{APIKey: "tok", APIKeySet: true})
	if err != nil || res.StopReason != ClassifierStopReasonStop || decisionCalls != 1 {
		t.Fatalf("classify = %+v, err=%v, calls=%d", res, err, decisionCalls)
	}
}
