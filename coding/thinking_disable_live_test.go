//go:build live

package coding

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestThinkingDisableLiveUpstream(t *testing.T) {
	for _, tc := range thinkingDisableCases() {
		t.Run(tc.provider+"/"+tc.name+"/live-only", func(t *testing.T) {
			key, env := thinkingDisableLiveAuth(t, tc)
			tc.env = env
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			runThinkingDisable(t, ctx, tc, key, "")
		})
	}
}

func thinkingDisableLiveAuth(t *testing.T, tc thinkingDisableCase) (string, ai.ProviderEnv) {
	t.Helper()
	t.Logf("live provider: %s", tc.provider)
	// Use the provider catalog for configured keys; the case label supplies the canonical missing-key diagnostic without changing the hermetic cases.
	for _, name := range ai.FindEnvKeys(tc.provider, nil) {
		if name != ai.AnthropicAuthTokenEnv {
			return testenv.RequireLiveEnv(t, name), nil
		}
	}
	if tc.provider == "google-vertex" {
		// upstream: packages/ai/test/google-thinking-disable.test.ts:124-131 selects an API key first, otherwise project/location with Application Default Credentials.
		projectEnv := "GOOGLE_CLOUD_PROJECT"
		if os.Getenv(projectEnv) == "" && os.Getenv("GCLOUD_PROJECT") != "" {
			projectEnv = "GCLOUD_PROJECT"
		}
		names := []string{projectEnv, "GOOGLE_CLOUD_LOCATION"}
		if os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(home, ".config", "gcloud", "application_default_credentials.json")); os.IsNotExist(err) {
				names = append(names, "GOOGLE_APPLICATION_CREDENTIALS")
			}
		}
		t.Log("Vertex requires GOOGLE_CLOUD_API_KEY or GOOGLE_CLOUD_PROJECT/GCLOUD_PROJECT plus GOOGLE_CLOUD_LOCATION and Application Default Credentials")
		testenv.RequireLiveEnv(t, names...)
		// The Model Runtime owns ADC resolution, including errors from invalid supplied credentials.
		return "", ai.ProviderEnv{"GOOGLE_CLOUD_PROJECT": os.Getenv(projectEnv), "GOOGLE_CLOUD_LOCATION": os.Getenv("GOOGLE_CLOUD_LOCATION")}
	}
	return testenv.RequireLiveEnv(t, strings.TrimPrefix(tc.keyEnv, "PIG_LIVE_")), nil
}
