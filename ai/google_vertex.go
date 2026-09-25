package ai

import (
	"os"
	"strings"
)

// GoogleVertexConfig configures the Google Vertex AI provider.
// Mirrors upstream providers/google-vertex.ts. Vertex uses the same
// Gemini wire format but routes through a Vertex-specific endpoint
// that accepts either an API key or Application Default Credentials.
type GoogleVertexConfig struct {
	APIKey     string
	Model      string
	ProviderID string
	BaseURL    string
	Project    string
	Location   string
	Headers    map[string]string
}

// NewGoogleVertexProvider creates a Google Vertex AI provider.
// Mirrors upstream providers/google-vertex.ts.
func NewGoogleVertexProvider(cfg GoogleVertexConfig) Provider {
	providerID := cfg.ProviderID
	if providerID == "" {
		providerID = string(APIGoogleVertex)
	}
	project := resolveVertexProject(cfg.Project)
	location := resolveVertexLocation(cfg.Location)
	baseURL := resolveVertexBaseURL(cfg.BaseURL, project, location)

	return NewGoogleProvider(GoogleConfig{
		APIKey:       cfg.APIKey,
		Model:        cfg.Model,
		ProviderID:   providerID,
		BaseURL:      baseURL,
		APIVersion:   "", // Vertex URL already includes the version path
		ExtraHeaders: cfg.Headers,
	})
}

func resolveVertexProject(explicit string) string {
	return firstNonEmptyString(
		explicit,
		os.Getenv("GOOGLE_CLOUD_PROJECT"),
		os.Getenv("GCP_PROJECT"),
		os.Getenv("GCLOUD_PROJECT"),
	)
}

func resolveVertexLocation(explicit string) string {
	loc := firstNonEmptyString(
		explicit,
		os.Getenv("GOOGLE_CLOUD_LOCATION"),
		os.Getenv("GCP_LOCATION"),
	)
	if loc == "" {
		return "us-central1"
	}
	return loc
}

func resolveVertexBaseURL(explicit, project, location string) string {
	if custom := resolveCustomBaseURL(explicit); custom != "" {
		return strings.TrimRight(custom, "/")
	}
	// Standard Vertex AI endpoint pattern.
	// Mirrors upstream google-vertex.ts createClient / createClientWithApiKey.
	if project != "" {
		return "https://" + location + "-aiplatform.googleapis.com/v1/projects/" +
			project + "/locations/" + location + "/publishers/google"
	}
	// Fallback: regional endpoint without project (requires API key auth).
	return "https://" + location + "-aiplatform.googleapis.com/v1beta"
}

func resolveCustomBaseURL(baseURL string) string {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" || strings.Contains(trimmed, "{location}") {
		return ""
	}
	return trimmed
}
