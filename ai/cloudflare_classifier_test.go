package ai

import (
	"context"
	"testing"
)

// providers/cloudflare-stream.ts:31-36 cloudflareClassifier: the wrapped classifier gets the model with `{CLOUDFLARE_ACCOUNT_ID}` and `{CLOUDFLARE_GATEWAY_ID}` in its base URL
// replaced from options.env (a placeholder without a value stays), the model itself when nothing changes (resolveCloudflareModel:15-16), a copy otherwise, and the
// request context and options unchanged; the wrapped classifier's result comes back.
func TestCloudflareClassifierResolvesTheBaseURLFromTheOptionsEnv(t *testing.T) {
	var seen *ClassifierModel
	var seenOptions ClassifierOptions
	inner := &ProviderClassifier{Classify: func(_ context.Context, model *ClassifierModel, _ ClassifierContext, options ClassifierOptions) (ClassifierResult, error) {
		seen, seenOptions = model, options
		return ClassifierResult{Model: "from-inner"}, nil
	}}
	wrapped := CloudflareClassifier(inner)
	const template = "https://gateway/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/x/{CLOUDFLARE_ACCOUNT_ID}"
	original := &ClassifierModel{ID: "m", BaseURL: template}

	result, err := wrapped.Classify(context.Background(), original, ClassifierContext{}, ClassifierOptions{APIKey: "key", Env: ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "acct", "CLOUDFLARE_GATEWAY_ID": "gw"}})
	if err != nil || result.Model != "from-inner" {
		t.Fatalf("result = %+v, %v; want the wrapped classifier's", result, err)
	}
	if seen == original || seen.BaseURL != "https://gateway/acct/gw/x/acct" || seen.ID != "m" || seenOptions.APIKey != "key" {
		t.Fatalf("the wrapped classifier got model %+v (same pointer %v) options %+v", seen, seen == original, seenOptions)
	}
	if original.BaseURL != template {
		t.Fatalf("the caller's model was changed: %q", original.BaseURL)
	}

	_, _ = wrapped.Classify(context.Background(), original, ClassifierContext{}, ClassifierOptions{Env: ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "acct"}})
	if seen.BaseURL != "https://gateway/acct/{CLOUDFLARE_GATEWAY_ID}/x/acct" {
		t.Fatalf("a placeholder without a value was replaced: %q", seen.BaseURL)
	}
	_, _ = wrapped.Classify(context.Background(), original, ClassifierContext{}, ClassifierOptions{})
	if seen != original {
		t.Fatal("without an env the model must reach the wrapped classifier unchanged")
	}
	_, _ = wrapped.Classify(context.Background(), original, ClassifierContext{}, ClassifierOptions{Env: ProviderEnv{"OTHER": "x"}})
	if seen != original {
		t.Fatal("an env that changes nothing must hand over the same model")
	}
}
