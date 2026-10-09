package ai

// GoogleVertexConfig configures Vertex API-key or Application Default Credentials requests.
type GoogleVertexConfig struct {
	APIKey           string
	Model            string
	ProviderID       string
	BaseURL          string
	Project          string
	Location         string
	Headers          map[string]string
	ThinkingLevelMap ThinkingLevelMap
	// ModelMetadata is the selected model, which an OnProviderStreamEvent observer receives. Nil hands the observer the configured identity only.
	ModelMetadata *Model
}
