package coding

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// H1: Coding-level test proving ModelRuntime.GetAvailableOfType("classifier", "github-copilot")
// filters out microsoft-decision-1 when the stored Copilot OAuth credential's
// availableClassifierModelIds excludes it, and keeps all classifiers for legacy credentials.
func TestModelRuntimeCopilotClassifierFiltering(t *testing.T) {
	t.Run("filters classifiers by availableClassifierModelIds", func(t *testing.T) {
		services, _ := nativeCompatServices(t, "", map[string]ai.Credential{
			"github-copilot": {
				Type:                        ai.CredentialOAuth,
				Access:                      "tid=test;exp=9999999999;proxy-ep=proxy.business.githubcopilot.com;",
				Refresh:                     "refresh",
				Expires:                     time.Now().UnixMilli() + 3_600_000,
				AvailableModelIDs:           json.RawMessage(`["gpt-6-luna"]`),
				AvailableClassifierModelIDs: json.RawMessage(`["gpt-6-luna-decisions"]`),
			},
		})
		runtime := services.ModelRuntime()
		avail, err := runtime.GetAvailableOfType(t.Context(), ai.ModelTypeClassifier, "github-copilot")
		if err != nil {
			t.Fatalf("GetAvailableOfType: %v", err)
		}
		for _, m := range avail {
			if m.ModelID() == "microsoft-decision-1" {
				t.Fatalf("expected microsoft-decision-1 filtered out by availableClassifierModelIds, got %v", avail)
			}
		}
	})

	t.Run("keeps microsoft-decision-1 when availableClassifierModelIds is omitted (legacy login)", func(t *testing.T) {
		services, _ := nativeCompatServices(t, "", map[string]ai.Credential{
			"github-copilot": {
				Type:              ai.CredentialOAuth,
				Access:            "tid=test;exp=9999999999;proxy-ep=proxy.enterprise.githubcopilot.com;",
				Refresh:           "refresh",
				Expires:           time.Now().UnixMilli() + 3_600_000,
				AvailableModelIDs: json.RawMessage(`["gpt-6-luna"]`),
			},
		})
		runtime := services.ModelRuntime()
		avail, err := runtime.GetAvailableOfType(t.Context(), ai.ModelTypeClassifier, "github-copilot")
		if err != nil {
			t.Fatalf("GetAvailableOfType: %v", err)
		}
		found := false
		for _, m := range avail {
			if m.ModelID() == "microsoft-decision-1" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected legacy credential without availableClassifierModelIds to keep microsoft-decision-1, got %v", avail)
		}
	})
}

// H2: Coding-level test proving Radius classifier models refreshed from a Radius gateway
// are listed via ModelRuntime.GetModelsOfType / GetAvailableOfType and callable through ModelRuntime.Classify.
func TestModelRuntimeRadiusClassifierListingAndClassify(t *testing.T) {
	var decisionCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/config":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"baseUrl": "http://" + r.Host + "/v1",
				"models": []map[string]any{
					{
						"id": "radius-chat-1", "name": "Radius Chat 1", "reasoning": false,
						"input": []string{"text"}, "cost": map[string]any{"input": 1, "output": 2, "cacheRead": 0, "cacheWrite": 0},
						"contextWindow": 128000, "maxTokens": 4096,
					},
				},
				"classifiers": []map[string]any{
					{
						"id": "radius-decider-1", "name": "Radius Decider 1",
						"input": []string{"text"}, "cost": map[string]any{"input": 0.05, "output": 0, "cacheRead": 0, "cacheWrite": 0},
						"contextWindow": 32768,
					},
				},
			})
		case "/v1/decision":
			decisionCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"responseId":"rad-dec-1","answers":{"approved":{"type":"bool","probability":0.88}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	modelsJSON := `{"providers":{"radius-gw":{"baseUrl":"` + srv.URL + `","oauth":"radius"}}}`
	services, _ := nativeCompatServices(t, modelsJSON, map[string]ai.Credential{
		"radius-gw": {Type: ai.CredentialAPIKey, Key: "rad-key"},
	})
	runtime := services.ModelRuntime()
	if res := runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{
		Providers:    []string{"radius-gw"},
		AllowNetwork: new(true),
	}); len(res.Errors) > 0 {
		t.Fatalf("Refresh errors: %v", res.Errors)
	}

	cls, _ := runtime.GetModelOfType(ai.ModelTypeClassifier, "radius-gw", "radius-decider-1").(*ai.ClassifierModel)
	if cls == nil || cls.API != ai.ClassifierAPIPiDecision {
		t.Fatalf("expected Radius classifier model listed in coding runtime, got %#v (all=%v)", cls, runtime.GetAllModels("radius-gw"))
	}
	avail, err := runtime.GetAvailableOfType(t.Context(), ai.ModelTypeClassifier, "radius-gw")
	if err != nil || len(avail) != 1 || avail[0].ModelID() != "radius-decider-1" {
		t.Fatalf("GetAvailableOfType = %v, err=%v", avail, err)
	}
	res := runtime.Classify(t.Context(), cls, classifiersTestContext)
	if res.StopReason != ai.ClassifierStopReasonStop || decisionCalls != 1 || res.ResponseID != "rad-dec-1" {
		t.Fatalf("Classify = %+v, calls=%d", res, decisionCalls)
	}
	if len(res.Answers) != 1 || res.Answers[0].Answer != (ai.ClassifierBoolAnswer{Probability: 0.88}) {
		t.Fatalf("unexpected answers: %+v", res.Answers)
	}
}
