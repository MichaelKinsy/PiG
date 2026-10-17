package ai

import (
	"context"
	"testing"
)

// cloudflare-stream.ts:31-36 cloudflareClassifier: the wrapped classifier receives the model with the account and gateway placeholders of its
// base URL replaced from options.env (every occurrence), the model itself when nothing changes, and the context, request and options unchanged.
func TestCloudflareClassifierResolvesTheEndpointPlaceholdersFromTheOptionsEnv(t *testing.T) {
	type call struct {
		model   *ClassifierModel
		request ClassifierContext
		options ClassifierOptions
	}
	var got call
	inner := &ProviderClassifier{Classify: func(_ context.Context, model *ClassifierModel, request ClassifierContext, options ClassifierOptions) (ClassifierResult, error) {
		got = call{model, request, options}
		return ClassifierResult{}, nil
	}}
	wrapped := CloudflareClassifier(inner)
	run := func(baseURL string, env ProviderEnv) (*ClassifierModel, *ClassifierModel) {
		t.Helper()
		model := &ClassifierModel{ID: "m", BaseURL: baseURL}
		request := ClassifierContext{State: JsonObject{"k": "v"}}
		options := ClassifierOptions{Env: env}
		if _, err := wrapped.Classify(context.Background(), model, request, options); err != nil {
			t.Fatal(err)
		}
		if got.request.State["k"] != "v" {
			t.Errorf("the request did not reach the classifier: %+v", got.request)
		}
		if got.options.Env == nil && env != nil {
			t.Errorf("the options did not reach the classifier")
		}
		return model, got.model
	}
	const template = "https://api.cloudflare.com/client/v4/accounts/{CLOUDFLARE_ACCOUNT_ID}/ai/run/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}"
	env := ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "acct", "CLOUDFLARE_GATEWAY_ID": "gw"}
	original, resolved := run(template, env)
	if want := "https://api.cloudflare.com/client/v4/accounts/acct/ai/run/acct/gw"; resolved.BaseURL != want {
		t.Errorf("resolved base URL = %q, want %q", resolved.BaseURL, want)
	}
	if original.BaseURL != template || resolved == original || resolved.ID != "m" {
		t.Errorf("the caller's model must stay unchanged and the copy keep its other fields: %+v", original)
	}
	if _, partial := run(template, ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "acct"}); partial.BaseURL != "https://api.cloudflare.com/client/v4/accounts/acct/ai/run/acct/{CLOUDFLARE_GATEWAY_ID}" {
		t.Errorf("a missing gateway id leaves its placeholder: %q", partial.BaseURL)
	}
	if original, same := run(template, nil); same != original || same.BaseURL != template {
		t.Errorf("without env the classifier receives the model itself, got %+v", same)
	}
	if original, same := run("https://example.test/v1", env); same != original {
		t.Errorf("an endpoint without placeholders is passed as the same model, got %+v", same)
	}
}
