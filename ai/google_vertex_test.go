package ai

import "testing"

func TestResolveVertexBaseURL(t *testing.T) {
	cases := []struct {
		name     string
		explicit string
		project  string
		location string
		want     string
	}{
		{
			name:     "explicit override",
			explicit: "https://custom.endpoint/v1/",
			want:     "https://custom.endpoint/v1",
		},
		{
			name:     "project+location",
			project:  "my-project",
			location: "us-east4",
			want:     "https://us-east4-aiplatform.googleapis.com/v1/projects/my-project/locations/us-east4/publishers/google",
		},
		{
			name:     "no project fallback",
			location: "europe-west1",
			want:     "https://europe-west1-aiplatform.googleapis.com/v1beta",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveVertexBaseURL(tc.explicit, tc.project, tc.location)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveVertexLocation(t *testing.T) {
	if got := resolveVertexLocation(""); got != "us-central1" {
		t.Fatalf("default location = %q, want us-central1", got)
	}
	t.Setenv("GOOGLE_CLOUD_LOCATION", "asia-east1")
	if got := resolveVertexLocation(""); got != "asia-east1" {
		t.Fatalf("env location = %q, want asia-east1", got)
	}
	if got := resolveVertexLocation("explicit"); got != "explicit" {
		t.Fatalf("explicit location = %q, want explicit", got)
	}
}

func TestNewGoogleVertexProvider(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	p := NewGoogleVertexProvider(GoogleVertexConfig{
		APIKey: "test-key",
		Model:  "gemini-2.5-flash",
	})
	gp, ok := p.(*googleProvider)
	if !ok {
		t.Fatalf("type = %T, want *googleProvider", p)
	}
	if gp.cfg.ProviderID != string(APIGoogleVertex) {
		t.Fatalf("ProviderID = %q", gp.cfg.ProviderID)
	}
	if gp.cfg.Model != "gemini-2.5-flash" {
		t.Fatalf("Model = %q", gp.cfg.Model)
	}
	wantBase := "https://us-central1-aiplatform.googleapis.com/v1/projects/test-project/locations/us-central1/publishers/google"
	if gp.cfg.BaseURL != wantBase {
		t.Fatalf("BaseURL = %q, want %q", gp.cfg.BaseURL, wantBase)
	}
}
