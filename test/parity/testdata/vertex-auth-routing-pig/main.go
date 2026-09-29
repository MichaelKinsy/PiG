package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/oauth2"

	"github.com/MichaelKinsy/PiG/ai"
)

type testCase struct{ Name, Key, EnvKey, UserAgent, Path string }
type capture struct {
	Case      string `json:"case"`
	Path      string `json:"path"`
	Key       string `json:"key"`
	Auth      string `json:"auth"`
	UserAgent string `json:"userAgent"`
	API       ai.API `json:"api"`
}
type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	data, err := os.ReadFile("test/parity/testdata/vertex-auth-routing.json")
	if err != nil {
		return err
	}
	var cases []testCase
	if err = json.Unmarshal(data, &cases); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "pig-vertex-adc-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	credentials := filepath.Join(dir, "adc.json")
	if err = os.WriteFile(credentials, []byte(`{"type":"authorized_user","client_id":"test-client","client_secret":"test-secret","refresh_token":"test-refresh"}`), 0o600); err != nil {
		return err
	}
	tokenClient := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"access_token":"adc-fixture","token_type":"Bearer","expires_in":3600}`))}, nil
	})}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, tokenClient)
	for _, test := range cases {
		if err = probe(ctx, credentials, test); err != nil {
			return err
		}
	}
	return nil
}
func probe(ctx context.Context, credentials string, test testCase) error {
	if err := os.Setenv("GOOGLE_CLOUD_API_KEY", test.EnvKey); err != nil {
		return err
	}
	requests := make(chan capture, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userAgent := ""
		if test.UserAgent != "" {
			userAgent = r.Header.Get("User-Agent")
		}
		requests <- capture{Case: test.Name, Path: r.URL.Path, Key: r.Header.Get("x-goog-api-key"), Auth: r.Header.Get("Authorization"), UserAgent: userAgent}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\n")
	}))
	defer server.Close()
	provider := ai.NewGoogleVertexProvider(ai.GoogleVertexConfig{APIKey: test.Key, Model: "gemini-3-flash-preview", Project: "test-project", Location: "us-central1", BaseURL: server.URL + test.Path})
	defer func() { _ = provider.Close() }()
	options := ai.StreamOptions{Env: ai.ProviderEnv{"GOOGLE_APPLICATION_CREDENTIALS": credentials}}
	if test.UserAgent != "" {
		options.Headers = ai.ProviderHeadersFromStrings(map[string]string{"User-Agent": test.UserAgent})
	}
	stream, err := provider.Stream(ctx, ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}), options)
	if err != nil {
		return err
	}
	result := stream.Result()
	if result.StopReason != ai.StopReasonStop {
		return fmt.Errorf("result=%+v", result)
	}
	got := <-requests
	got.API = result.API
	return json.NewEncoder(os.Stdout).Encode(got)
}
