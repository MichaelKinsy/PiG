package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"

	"github.com/MichaelKinsy/PiG/ai"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	dir, err := os.MkdirTemp("", "bedrock-config-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	credentials := "[explicit-profile]\naws_access_key_id = explicit-profile\naws_secret_access_key = profile-secret\n[scoped-profile]\naws_access_key_id = scoped-profile\naws_secret_access_key = profile-secret\n[ambient-profile]\naws_access_key_id = ambient-profile\naws_secret_access_key = profile-secret\n"
	if err := os.WriteFile(filepath.Join(dir, "credentials"), []byte(credentials), 0600); err != nil {
		return err
	}
	for key, value := range map[string]string{"AWS_CONFIG_FILE": filepath.Join(dir, "config"), "AWS_SHARED_CREDENTIALS_FILE": filepath.Join(dir, "credentials"), "AWS_PROFILE": "ambient-profile", "AWS_ACCESS_KEY_ID": "AKIAEXAMPLE", "AWS_SECRET_ACCESS_KEY": "secretexample", "AWS_REGION": "us-east-2", "AWS_EC2_METADATA_DISABLED": "true"} {
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	for _, key := range []string{"AWS_DEFAULT_PROFILE", "AWS_SESSION_TOKEN", "AWS_BEDROCK_SKIP_AUTH", "AWS_BEARER_TOKEN_BEDROCK", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"} {
		if err := os.Unsetenv(key); err != nil {
			return err
		}
	}
	headers := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		headers <- r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
	}))
	defer server.Close()
	provider := ai.NewBedrockProvider("us.anthropic.claude-opus-4-8", server.URL)
	defer func() { _ = provider.Close() }()
	scope := regexp.MustCompile(`Credential=([^/]+)/[^/]+/([^/]+)/`)
	for _, tc := range []struct {
		name string
		opts ai.StreamOptions
	}{
		{"explicit", ai.StreamOptions{Profile: "explicit-profile", Region: "eu-west-1", Env: ai.ProviderEnv{"AWS_ACCESS_KEY_ID": "AKIAEXAMPLE", "AWS_SECRET_ACCESS_KEY": "secretexample"}}},
		{"scoped", ai.StreamOptions{Env: ai.ProviderEnv{"AWS_PROFILE": "scoped-profile", "AWS_ACCESS_KEY_ID": "AKIAEXAMPLE", "AWS_SECRET_ACCESS_KEY": "secretexample"}}},
		{"ambient", ai.StreamOptions{}},
		{"bearer", ai.StreamOptions{APIKey: "bedrock-api-key"}},
	} {
		if tc.name == "ambient" {
			if err := os.Setenv("AWS_ACCESS_KEY_ID", "AKIAEXAMPLE"); err != nil {
				return err
			}
			if err := os.Setenv("AWS_SECRET_ACCESS_KEY", "secretexample"); err != nil {
				return err
			}
		}
		stream, err := provider.Stream(context.Background(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello"), Timestamp: 1}}}), tc.opts)
		if err != nil {
			return err
		}
		stream.Result()
		header := <-headers
		if matches := scope.FindStringSubmatch(header); matches != nil {
			fmt.Printf("%s auth=%s region=%s\n", tc.name, matches[1], matches[2])
		} else {
			fmt.Printf("%s auth=%s\n", tc.name, header)
		}
	}
	return nil
}
