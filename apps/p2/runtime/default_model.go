package runtime

import "errors"

// Ports apps/p2/src/runtime/default-model.ts: the default model from the environment (Pi Mobile's
// PI_MOBILE_PROVIDER/PI_MOBILE_MODEL, as P2_PROVIDER/P2_MODEL): after settings.json's defaultModel, before the first
// signed-in model.

// EnvModel is a provider and a model of it.
type EnvModel struct {
	Provider string
	ModelID  string
}

// EnvDefaultModel reads the default model from env (a lookup of environment variables): both or neither, a provider
// without a model (or the reverse) is a configuration error. Nil is no model.
func EnvDefaultModel(env func(string) (string, bool)) (*EnvModel, error) {
	pick := func(names ...string) string {
		for _, name := range names {
			if value, ok := env(name); ok {
				return value
			}
		}
		return ""
	}
	provider := pick("P2_PROVIDER", "PI_MOBILE_PROVIDER")
	modelID := pick("P2_MODEL", "PI_MOBILE_MODEL")
	if (provider != "") != (modelID != "") {
		return nil, errors.New("Set both P2_PROVIDER and P2_MODEL")
	}
	if provider == "" {
		return nil, nil
	}
	return &EnvModel{Provider: provider, ModelID: modelID}, nil
}
