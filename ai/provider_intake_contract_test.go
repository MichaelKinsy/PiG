package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// The integration's open credential union and this lane's typed account field must survive the same refresh/store conversion.
func TestIntakeOAuthAccountAndProviderFieldsShareCredentialRoundTrip(t *testing.T) {
	var original Credential
	if err := json.Unmarshal([]byte(`{"type":"oauth","refresh":"refresh","access":"old","expires":1,"accountId":"acct-typed","scope":"scope-typed","availableModelIds":["model-one"],"providerOwned":{"tenant":"tenant-one"}}`), &original); err != nil {
		t.Fatal(err)
	}
	oauth := credentialToOAuth(original)
	if oauth.AccountID != "acct-typed" || oauth.Scope != "scope-typed" || string(oauth.Extra["providerOwned"]) != `{"tenant":"tenant-one"}` {
		t.Fatalf("OAuth=%#v", oauth)
	}
	oauth.Access = "refreshed"
	refreshed, err := credentialFromOAuth(oauth)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewAuthStorage(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("custom", refreshed); err != nil {
		t.Fatal(err)
	}
	stored, err := store.Read(t.Context(), "custom")
	if err != nil {
		t.Fatal(err)
	}
	if stored.AccountID != "acct-typed" || stored.Access != "refreshed" || stored.Scope != "scope-typed" {
		t.Fatalf("stored=%#v", stored)
	}
	assertShapeJSON(t, stored.AvailableModelIDs, `["model-one"]`)
	assertShapeJSON(t, stored.Extra["providerOwned"], `{"tenant":"tenant-one"}`)
}

func TestIntakeGoogleNativeThinkingOverridesLogicalMap(t *testing.T) {
	provider := NewGoogleProvider(GoogleConfig{APIKey: "test", ProviderID: "google", Model: "gemini-3.1-pro-preview", ThinkingLevelMap: ThinkingLevelMap{ThinkingHigh: new("invalid-logical-mapping")}})
	defer func() { _ = provider.Close() }()
	captured := errors.New("captured before network")
	var payload struct {
		Config struct {
			ThinkingConfig struct {
				ThinkingLevel   string
				IncludeThoughts bool
			}
		}
	}
	_, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{Thinking: ThinkingHigh, IsReasoning: true, GoogleThinking: &GoogleThinkingOptions{Enabled: true, Level: GoogleThinkingLevelMedium}, OnPayload: func(value any, _ *Model) (any, error) {
		data, e := json.Marshal(value)
		if e != nil {
			return nil, e
		}
		if e = json.Unmarshal(data, &payload); e != nil {
			return nil, e
		}
		return nil, captured
	}})
	if !errors.Is(err, captured) || payload.Config.ThinkingConfig.ThinkingLevel != "MEDIUM" || !payload.Config.ThinkingConfig.IncludeThoughts {
		t.Fatalf("payload=%#v err=%v", payload, err)
	}
}

func TestIntakeCodexSourceOptionsOwnTimeoutRetryAndResponseObservation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		retries, limit := ConfiguredProviderRetry()
		if err := ConfigureProviderRetry(0, 1000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ConfigureProviderRetry(retries, limit) })
		requests := 0
		var statuses []int
		raw := codexRoundTripper(func(r *http.Request) (*http.Response, error) {
			requests++
			select {
			case <-r.Context().Done():
				return nil, r.Context().Err()
			case <-time.After(2 * time.Millisecond):
			}
			if requests == 1 {
				return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"2"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`))}, nil
			}
			return codexUpstreamHTTP(codexUpstreamSSE("completed", nil)), nil
		})
		provider := codexUpstreamProvider(t, "gpt-5.1-codex", &retryTransport{base: &providerRequestTransport{base: raw}})
		start := time.Now()
		stream, err := provider.Stream(t.Context(), codexUpstreamContext(), StreamOptions{Transport: TransportSSE, TimeoutMs: new(0), MaxRetries: new(1), MaxRetryDelayMs: new(4000), OnResponse: func(_ context.Context, response ProviderResponse, model *Model) error {
			statuses = append(statuses, response.Status)
			if model.ProviderMeta.API != APIOpenAICodexResponses {
				t.Error(model.ProviderMeta.API)
			}
			return nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		result := stream.Result()
		if result.StopReason != StopReasonStop || requests != 2 || !reflect.DeepEqual(statuses, []int{429, 200}) || time.Since(start) != 2*time.Second+4*time.Millisecond {
			t.Fatalf("result=%#v requests=%d statuses=%v elapsed=%s", result, requests, statuses, time.Since(start))
		}
	})
}
