package ai

import (
	"context"
	"errors"
	"testing"
)

// providers/cloudflare-stream.ts cloudflareClassifier: classify is called with resolveCloudflareModel(model, options.env), and with
// the context and options unchanged. The account and gateway placeholders of the base URL come from the resolved env; a placeholder with
// no env value stays; with no env, or no placeholder, the classifier receives the model itself; the caller's model is never modified;
// the result and the error come back as the wrapped classifier produced them.
func TestCloudflareClassifierMaterializesEndpointPlaceholdersBeforeDispatch(t *testing.T) {
	const template = "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/workers-ai"
	var gotModel *ClassifierModel
	var gotOptions ClassifierOptions
	failure := errors.New("classifier failed")
	wrapped := CloudflareClassifier(&ProviderClassifier{Classify: func(_ context.Context, model *ClassifierModel, _ ClassifierContext, options ClassifierOptions) (ClassifierResult, error) {
		gotModel, gotOptions = model, options
		if options.APIKey == "fail" {
			return ClassifierResult{}, failure
		}
		return ClassifierResult{Model: "result"}, nil
	}})

	model := &ClassifierModel{ID: "c", BaseURL: template}
	request := ClassifierContext{}
	options := ClassifierOptions{APIKey: "key", Env: ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "acct", "CLOUDFLARE_GATEWAY_ID": "gw"}}
	result, err := wrapped.Classify(context.Background(), model, request, options)
	if err != nil || result.Model != "result" {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if want := "https://gateway.ai.cloudflare.com/v1/acct/gw/workers-ai"; gotModel.BaseURL != want || gotModel.ID != "c" {
		t.Fatalf("classifier saw %+v, want base URL %q", gotModel, want)
	}
	if model.BaseURL != template {
		t.Fatalf("the caller's model was modified: %q", model.BaseURL)
	}
	if gotOptions.APIKey != "key" || gotOptions.Env["CLOUDFLARE_GATEWAY_ID"] != "gw" {
		t.Fatalf("options changed in transit: %+v", gotOptions)
	}

	// A placeholder without an env value stays as it is.
	if _, err := wrapped.Classify(context.Background(), model, request, ClassifierOptions{Env: ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "acct"}}); err != nil {
		t.Fatal(err)
	}
	if want := "https://gateway.ai.cloudflare.com/v1/acct/{CLOUDFLARE_GATEWAY_ID}/workers-ai"; gotModel.BaseURL != want {
		t.Fatalf("partial env: %q, want %q", gotModel.BaseURL, want)
	}

	// With no env, or nothing to replace, the classifier gets the model itself.
	if _, err := wrapped.Classify(context.Background(), model, request, ClassifierOptions{}); err != nil || gotModel != model {
		t.Fatalf("no env must pass the model through (err %v)", err)
	}
	plain := &ClassifierModel{ID: "p", BaseURL: "https://example.test/v1"}
	if _, err := wrapped.Classify(context.Background(), plain, request, ClassifierOptions{Env: ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "acct"}}); err != nil || gotModel != plain {
		t.Fatalf("a model without placeholders must pass through (err %v)", err)
	}

	if _, err := wrapped.Classify(context.Background(), model, request, ClassifierOptions{APIKey: "fail"}); !errors.Is(err, failure) {
		t.Fatalf("err = %v", err)
	}
}
