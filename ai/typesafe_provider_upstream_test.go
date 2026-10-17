package ai

import (
	"net/http"
	"reflect"
	"testing"
)

// Ports packages/ai/src/providers/typesafe.ts (typesafeProvider) and providers/typesafe.models.ts: the provider serves
// exactly the TYPESAFE_CLASSIFIER_MODELS shard and dispatches the "typesafe-system-one" classifier API.
func TestTypesafeProviderServesItsClassifierShard(t *testing.T) {
	provider := TypesafeProvider()
	if provider.ID != "typesafe" || provider.Name != "TypeSafe" {
		t.Fatalf("provider = %q %q", provider.ID, provider.Name)
	}
	if provider.Auth.APIKey == nil || provider.Auth.APIKey.Name != "TypeSafe API key" {
		t.Fatalf("api key auth = %+v", provider.Auth.APIKey)
	}
	models, err := provider.GetAllModels()
	if err != nil {
		t.Fatal(err)
	}
	jev := GetBuiltinClassifierModel("typesafe", "jev-latest")
	if jev == nil || !reflect.DeepEqual(models, []AnyModel{jev}) {
		t.Fatalf("models = %+v", models)
	}
	if provider.Classify == nil {
		t.Fatal("the provider has no classifier dispatch")
	}

	var requested string
	result, err := provider.Classify(t.Context(), jev, classifierTestContext(), ClassifierOptions{APIKey: "secret", APIKeySet: true, Fetch: clsClient(func(r *http.Request) (*http.Response, error) {
		requested = r.URL.String() + " " + r.Header.Get("authorization")
		return clsJSON(200, `{"answers":{"approved":{"type":"noul","noul":0.25}}}`), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	if requested != "https://api.typesafe.ai/v1/systemone Bearer secret" {
		t.Fatalf("request = %q", requested)
	}
	if result.StopReason != "stop" || clsAnswer(t, result.Answers, "approved") != (ClassifierBoolAnswer{Probability: 0.25}) {
		t.Fatalf("result = %+v", result)
	}
}
