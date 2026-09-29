package subprocess

import "github.com/MichaelKinsy/PiG/coding/extension"

// pig additive (D19): only the subprocess boundary projects native model pointers into JSON metadata; the Session and native Context retain the original scope identities.
type scopedModelSnapshot struct {
	Model         map[string]any `json:"model"`
	ThinkingLevel string         `json:"thinkingLevel,omitempty"`
}

func snapshotScopedModels(models []extension.ScopedModel) []scopedModelSnapshot {
	result := make([]scopedModelSnapshot, len(models))
	for i, scoped := range models {
		result[i] = scopedModelSnapshot{Model: extension.ModelInfo(scoped.Model), ThinkingLevel: string(scoped.ThinkingLevel)}
	}
	return result
}
