//go:build !pig_strip_google_vertex

package ai

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

func BenchmarkGoogleVertexADCTokenResolution(b *testing.B) {
	client := &http.Client{Transport: openAITestRoundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"access_token":"adc-access","token_type":"Bearer","expires_in":3600}`))}, nil
	})}
	ctx, options := vertexADCTestContext(b, client)
	b.ReportAllocs()
	for b.Loop() {
		if token, err := vertexAccessToken(ctx, options.Env); err != nil || token != "adc-access" {
			b.Fatalf("token=%q error=%v", token, err)
		}
	}
}

func TestGoogleVertexADCAuthenticatesModelRequest(t *testing.T) {
	tokenCalls := 0
	tokenClient := &http.Client{Transport: openAITestRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		tokenCalls++
		if r.URL.Host != "oauth2.googleapis.com" || r.Method != "POST" {
			t.Errorf("token request=%s %s", r.Method, r.URL)
		}
		if err := r.ParseForm(); err != nil {
			return nil, err
		}
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "test-refresh" {
			t.Errorf("token form=%v", r.Form)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"access_token":"adc-access","token_type":"Bearer","expires_in":3600}`))}, nil
	})}
	ctx, options := vertexADCTestContext(t, tokenClient)
	provider := NewGoogleVertexProvider(GoogleVertexConfig{APIKey: "<authenticated>", Model: "gemini-3-flash-preview", Project: "test-project", Location: "us-central1", BaseURL: "https://proxy.example.com"}).(*googleVertexProvider)
	apiCalls := 0
	provider.client = &http.Client{Transport: openAITestRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		apiCalls++
		if tokenCalls != 1 || r.Header.Get("Authorization") != "Bearer adc-access" || r.Header.Get("x-goog-api-key") != "" {
			t.Errorf("token calls=%d headers=%v", tokenCalls, r.Header)
		}
		if r.URL.String() != "https://proxy.example.com/v1/publishers/google/models/gemini-3-flash-preview:streamGenerateContent?alt=sse" {
			t.Errorf("URL=%s", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\n"))}, nil
	})}
	stream, err := provider.Stream(ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), options)
	if err != nil {
		t.Fatal(err)
	}
	result := stream.Result()
	if result.API != APIGoogleVertex || result.StopReason != StopReasonStop || apiCalls != 1 {
		t.Fatalf("result=%#v calls=%d", result, apiCalls)
	}
}

func TestGoogleVertexADCFailurePreventsModelRequest(t *testing.T) {
	client := &http.Client{Transport: openAITestRoundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 401, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":"invalid_grant","error_description":"expired ADC refresh token"}`))}, nil
	})}
	ctx, options := vertexADCTestContext(t, client)
	provider := NewGoogleVertexProvider(GoogleVertexConfig{Model: "gemini-3-flash-preview", Project: "test-project", Location: "us-central1"}).(*googleVertexProvider)
	provider.client = &http.Client{Transport: openAITestRoundTripperFunc(func(*http.Request) (*http.Response, error) {
		t.Error("model request after ADC failure")
		return nil, errors.New("unexpected model request")
	})}
	stream, err := provider.Stream(ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), options)
	if stream != nil || err == nil || !strings.Contains(err.Error(), "expired ADC refresh token") {
		t.Fatalf("stream=%v err=%v", stream, err)
	}
}

func TestGoogleVertexADCRequestCancellation(t *testing.T) {
	started := make(chan struct{})
	client := &http.Client{Transport: openAITestRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	base, options := vertexADCTestContext(t, client)
	ctx, cancel := context.WithCancel(base)
	defer cancel()
	provider := NewGoogleVertexProvider(GoogleVertexConfig{Model: "gemini-3-flash-preview", Project: "test-project", Location: "us-central1"})
	completed := make(chan error, 1)
	go func() {
		_, err := provider.Stream(ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), options)
		completed <- err
	}()
	<-started
	cancel()
	if err := <-completed; !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

func TestGoogleVertexRejectsMissingADCConfiguration(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GCLOUD_PROJECT", "")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "")
	for _, tc := range []struct{ project, want string }{{"", "Vertex AI requires a project ID. Set GOOGLE_CLOUD_PROJECT/GCLOUD_PROJECT or pass project in options."}, {"test-project", "Vertex AI requires a location. Set GOOGLE_CLOUD_LOCATION or pass location in options."}} {
		provider := NewGoogleVertexProvider(GoogleVertexConfig{Model: "gemini-3-flash-preview", Project: tc.project})
		_, err := provider.Stream(t.Context(), NormalizeContext(Context{}), StreamOptions{})
		if err == nil || err.Error() != tc.want {
			t.Fatalf("error=%v want=%s", err, tc.want)
		}
	}
}

// Pi hands GOOGLE_APPLICATION_CREDENTIALS to google-auth-library GoogleAuth, whose fromJSON accepts service-account, user, external-account and impersonation files. Each accepted file type must reach its own token exchange.
func TestGoogleVertexADCFileTypes(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	subjectToken := filepath.Join(t.TempDir(), "subject-token")
	if err := os.WriteFile(subjectToken, []byte("subject"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		file  map[string]any
		grant string
	}{
		{"service_account", map[string]any{"type": "service_account", "client_email": "sa@test-project.iam.gserviceaccount.com", "private_key": keyPEM, "private_key_id": "key-1", "token_uri": "https://oauth2.googleapis.com/token"}, "urn:ietf:params:oauth:grant-type:jwt-bearer"},
		{"untyped file uses the service account client", map[string]any{"client_email": "sa@test-project.iam.gserviceaccount.com", "private_key": keyPEM, "private_key_id": "key-1", "token_uri": "https://oauth2.googleapis.com/token"}, "urn:ietf:params:oauth:grant-type:jwt-bearer"},
		{"authorized_user", map[string]any{"type": "authorized_user", "client_id": "test-client", "client_secret": "test-secret", "refresh_token": "test-refresh"}, "refresh_token"},
		{"external_account", map[string]any{"type": "external_account", "audience": "//iam.googleapis.com/projects/1/locations/global/workloadIdentityPools/pool/providers/provider", "subject_token_type": "urn:ietf:params:oauth:token-type:jwt", "token_url": "https://sts.googleapis.com/v1/token", "credential_source": map[string]any{"file": subjectToken}}, "urn:ietf:params:oauth:grant-type:token-exchange"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "adc.json")
			data, err := json.Marshal(tc.file)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			grants := []string{}
			client := &http.Client{Transport: openAITestRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if err := r.ParseForm(); err != nil {
					return nil, err
				}
				grants = append(grants, r.Form.Get("grant_type"))
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"access_token":"adc-access","token_type":"Bearer","issued_token_type":"urn:ietf:params:oauth:token-type:access_token","expires_in":3600}`))}, nil
			})}
			ctx := context.WithValue(t.Context(), oauth2.HTTPClient, client)
			token, err := vertexAccessToken(ctx, ProviderEnv{"GOOGLE_APPLICATION_CREDENTIALS": path})
			if err != nil || token != "adc-access" || len(grants) != 1 || grants[0] != tc.grant {
				t.Fatalf("token=%q err=%v grants=%v", token, err, grants)
			}
		})
	}
	for _, tc := range []struct{ name, file, want string }{
		{"unsupported type", `{"type":"gdch_service_account"}`, `unsupported credentials type "gdch_service_account"`},
		{"malformed", `{`, "parse credentials file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "adc.json")
			if err := os.WriteFile(path, []byte(tc.file), 0o600); err != nil {
				t.Fatal(err)
			}
			client := &http.Client{Transport: openAITestRoundTripperFunc(func(*http.Request) (*http.Response, error) {
				t.Error("token request for rejected credentials file")
				return nil, errors.New("unexpected token request")
			})}
			ctx := context.WithValue(t.Context(), oauth2.HTTPClient, client)
			if _, err := vertexAccessToken(ctx, ProviderEnv{"GOOGLE_APPLICATION_CREDENTIALS": path}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v want %q", err, tc.want)
			}
		})
	}
}

// Ports google-vertex.ts resolveProject/resolveLocation: GoogleVertexOptions.project and .location satisfy the
// requirement without provider configuration or environment values.
func TestGoogleVertexProjectAndLocationFromStreamOptions(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GCLOUD_PROJECT", "")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "")
	provider := NewGoogleVertexProvider(GoogleVertexConfig{Model: "gemini-3-flash-preview"})
	_, err := provider.Stream(t.Context(), NormalizeContext(Context{}), StreamOptions{Project: "opt-project"})
	if err == nil || err.Error() != "Vertex AI requires a location. Set GOOGLE_CLOUD_LOCATION or pass location in options." {
		t.Fatalf("project from options not accepted: %v", err)
	}
	_, err = provider.Stream(t.Context(), NormalizeContext(Context{}), StreamOptions{Project: "opt-project", Location: "us-central1"})
	if err != nil && (strings.Contains(err.Error(), "requires a project ID") || strings.Contains(err.Error(), "requires a location")) {
		t.Fatalf("project and location from options not accepted: %v", err)
	}
}
