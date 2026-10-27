package sdk

import (
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// NewModelRegistry is Pi's `new ModelRegistry(runtime)` (model-registry.ts:48-52): the compatibility facade over a ModelRuntime the extension built. Every member forwards to the runtime as Pi's does, so it answers from that runtime and not from the Session's registry. The members that take a provider configuration take the runtime's: no functions (see [ModelRuntime.RegisterProvider]).
func NewModelRegistry(runtime *ModelRuntime) ModelRegistry {
	return ModelRegistry{context: runtime.object.context, runtime: runtime}
}

// modelProvider is `model.provider` of a model object.
func modelProvider(model map[string]any) string {
	provider, _ := model["provider"].(string)
	return provider
}

// authResultMap is an AuthResult as the JSON object Pi's value is.
func authResultMap(result *AuthResult) (map[string]any, error) {
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// providerDisplayName is `runtime.getProvider(provider)?.name ?? provider` (model-registry.ts getProviderDisplayName), asked of the host without a Provider object, which a builtin provider has no carrier for here.
func (r *ModelRuntime) providerDisplayName(provider string) (string, error) {
	name, err := graphMethodCall[*string](r.object, "providerName", provider)
	if err != nil {
		return "", err
	}
	if name == nil {
		return provider, nil
	}
	return *name, nil
}

// requestAuth is Pi's getApiKeyAndHeaders over a runtime (model-registry.ts:80-119): `{ok: true, apiKey?, headers?, baseUrl?, env?}` or `{ok: false, error}`; it never fails.
func (r *ModelRuntime) requestAuth(model map[string]any) map[string]any {
	provider := modelProvider(model)
	failed := func(err error) map[string]any {
		message := err.Error()
		if message == "authHeader requires a resolved API key" {
			message = `No API key found for "` + provider + `"`
		}
		return map[string]any{"ok": false, "error": message}
	}
	resolution, err := r.GetAuth(model, nil)
	if err != nil {
		return failed(err)
	}
	if resolution == nil {
		compat, err := r.GetCompatibilityRequestConfig(model)
		if err != nil {
			return failed(err)
		}
		if header, _ := compat["authHeader"].(bool); header {
			return map[string]any{"ok": false, "error": `No API key found for "` + provider + `"`}
		}
		out := map[string]any{"ok": true}
		if headers, present := compat["headers"]; present && headers != nil {
			out["headers"] = headers
		}
		return out
	}
	out := map[string]any{"ok": true}
	for _, key := range []string{"apiKey", "headers"} {
		if value, present := resolution.Auth[key]; present && value != nil {
			out[key] = value
		}
	}
	if baseURL, _ := resolution.Auth["baseUrl"].(string); baseURL != "" {
		out["baseUrl"] = baseURL
	}
	if resolution.Env != nil {
		out["env"] = resolution.Env
	}
	return out
}
