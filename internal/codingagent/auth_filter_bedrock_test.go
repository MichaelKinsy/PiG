package codingagent

import (
	"os"
	"path/filepath"
	"testing"
)

// Pi's Bedrock auth reads only env signals (env-api-keys.ts amazon-bedrock, amazon-bedrock.ts bedrockAuth.resolve); a shared
// ~/.aws/credentials or ~/.aws/config file alone does not make the provider available, so `--list-models` shows no Bedrock models.
func TestBedrockSharedAWSFilesAloneAreNotConfiguredAuthLikePi(t *testing.T) {
	for _, name := range []string{"AWS_PROFILE", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_BEARER_TOKEN_BEDROCK", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_WEB_IDENTITY_TOKEN_FILE"} {
		t.Setenv(name, "")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, ".aws"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"credentials", "config"} {
		if err := os.WriteFile(filepath.Join(home, ".aws", name), []byte("[default]\naws_access_key_id=A\naws_secret_access_key=B\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if AuthenticatedProviders(t.TempDir())["amazon-bedrock"] || AuthenticatedProviders("")["amazon-bedrock"] {
		t.Fatal("shared AWS files alone made amazon-bedrock available")
	}
	t.Setenv("AWS_PROFILE", "default")
	if !AuthenticatedProviders(t.TempDir())["amazon-bedrock"] || !AuthenticatedProviders("")["amazon-bedrock"] {
		t.Fatal("AWS_PROFILE did not make amazon-bedrock available")
	}
}
