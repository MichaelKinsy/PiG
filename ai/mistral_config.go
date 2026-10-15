package ai

// MistralConfig configures the Mistral API provider.
type MistralConfig struct {
	// ModelMetadata retains the selected model's input capabilities, identity and thinking map.
	ModelMetadata *Model
	APIKey        string
	Model         string
	ProviderID    string
	BaseURL       string
	ExtraHeaders  map[string]string
	// SessionID for x-affinity header (KV-cache reuse).
	SessionID string
	// Reasoning indicates whether the model supports extended reasoning.
	Reasoning bool
}
