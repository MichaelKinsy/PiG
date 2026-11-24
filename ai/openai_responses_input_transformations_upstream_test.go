package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// Ports packages/ai/test/openai-responses-input-transformations.test.ts.
func TestOpenAIResponsesInputTransformationsUpstream(t *testing.T) {
	const (
		prefixMismatch       = `{"type":"reasoning","id":"rs_edited","action":"removed","reason":"prefix_mismatch"}`
		organizationMismatch = `{"type":"reasoning","id":"rs_org","action":"removed","reason":"organization_mismatch"}`
		turnBoundary         = `{"type":"reasoning","id":"rs_old_turn","action":"removed","reason":"turn_boundary"}`
		futureReason         = `{"type":"reasoning","id":"rs_future","action":"removed","reason":"future_reason"}`
	)

	processTerminal := func(t *testing.T, inputTransformations string, status string) (*AssistantMessage, int64) {
		t.Helper()
		eventType := "response.completed"
		extra := ""
		if status == "incomplete" {
			eventType = "response.incomplete"
			extra = `,"incomplete_details":{"reason":"max_output_tokens"}`
		}
		transformField := ""
		if inputTransformations != "" {
			transformField = `,"input_transformations":` + inputTransformations
		}
		sse := fmt.Sprintf("data: {\"type\":%q,\"sequence_number\":0,\"response\":{\"id\":\"resp_transformations\",\"status\":%q,\"output\":[]%s%s}}\n\n", eventType, status, extra, transformField)
		provider := NewOpenAIResponsesProvider(OpenAIResponsesConfig{ProviderID: "openai", Model: "gpt-6.2-sol"}).(*openAIResponsesProvider)
		defer func() {
			if err := provider.Close(); err != nil {
				t.Error(err)
			}
		}()
		builder := newAssistantStreamBuilder(t.Context(), APIOpenAIResponses, "openai", "gpt-6.2-sol")
		wantTimestamp := time.Now().UnixMilli()
		provider.parseResponsesSSE(t.Context(), strings.NewReader(sse), builder, nil)
		return builder.stream.Result(), wantTimestamp
	}

	t.Run("records removed reasoning items with their reasons", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			result, wantTimestamp := processTerminal(t, "["+prefixMismatch+","+turnBoundary+","+organizationMismatch+"]", "completed")
			if result.StopReason != StopReasonStop {
				t.Fatalf("stopReason=%s, want stop; error=%s", result.StopReason, result.ErrorMessage)
			}
			encoded, err := json.Marshal(result.Diagnostics)
			if err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf(`[{"type":"openai_input_transformations","timestamp":%d,"details":{"transformations":[%s,%s]}}]`, wantTimestamp, prefixMismatch, organizationMismatch)
			assertShapeJSON(t, encoded, want)
		})
	})

	t.Run("records removals on incomplete responses", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			result, wantTimestamp := processTerminal(t, "["+prefixMismatch+"]", "incomplete")
			if result.StopReason != StopReasonLength {
				t.Fatalf("stopReason=%s, want length; error=%s", result.StopReason, result.ErrorMessage)
			}
			encoded, err := json.Marshal(result.Diagnostics)
			if err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf(`[{"type":"openai_input_transformations","timestamp":%d,"details":{"transformations":[%s]}}]`, wantTimestamp, prefixMismatch)
			assertShapeJSON(t, encoded, want)
		})
	})

	t.Run("keeps reasons the SDK does not know yet", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			result, wantTimestamp := processTerminal(t, "["+futureReason+"]", "completed")
			if result.StopReason != StopReasonStop {
				t.Fatalf("stopReason=%s, want stop; error=%s", result.StopReason, result.ErrorMessage)
			}
			encoded, err := json.Marshal(result.Diagnostics)
			if err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf(`[{"type":"openai_input_transformations","timestamp":%d,"details":{"transformations":[%s]}}]`, wantTimestamp, futureReason)
			assertShapeJSON(t, encoded, want)
		})
	})

	t.Run("ignores turn boundaries, other transformations and malformed reports", func(t *testing.T) {
		reports := []string{
			"",
			"null",
			"[]",
			"[" + turnBoundary + "," + turnBoundary + "]",
			`{"malformed":true}`,
			`[null,"rs_text",{"type":"message","id":"msg_1","action":"removed","reason":"prefix_mismatch"}]`,
			`[{"type":"reasoning","id":"rs_changed","action":"changed","reason":"prefix_mismatch"}]`,
		}
		for _, report := range reports {
			synctest.Test(t, func(t *testing.T) {
				result, _ := processTerminal(t, report, "completed")
				if result.StopReason != StopReasonStop {
					t.Fatalf("report=%s stopReason=%s, want stop; error=%s", report, result.StopReason, result.ErrorMessage)
				}
				if len(result.Diagnostics) != 0 {
					t.Fatalf("report=%s diagnostics=%+v, want none", report, result.Diagnostics)
				}
			})
		}
	})

	t.Run("keeps explicit null id and reason and preserves Pi member order", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			const withNulls = `{"reason":null,"action":"removed","id":null,"type":"reasoning"}`
			result, wantTimestamp := processTerminal(t, "["+withNulls+"]", "completed")
			if result.StopReason != StopReasonStop {
				t.Fatalf("stopReason=%s, want stop; error=%s", result.StopReason, result.ErrorMessage)
			}
			encoded, err := json.Marshal(result.Diagnostics)
			if err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf(`[{"type":"openai_input_transformations","timestamp":%d,"details":{"transformations":[{"type":"reasoning","id":null,"action":"removed","reason":null}]}}]`, wantTimestamp)
			if string(encoded) != want {
				t.Fatalf("diagnostics = %s, want %s", encoded, want)
			}
		})
	})

	t.Run("records removals that Codex reports in response.done", func(t *testing.T) {
		sse := fmt.Sprintf("data: {\"type\":\"response.done\",\"response\":{\"id\":\"resp_codex\",\"status\":\"completed\",\"output\":[],\"input_transformations\":[%s,%s]}}\n\n", prefixMismatch, turnBoundary)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, sse)
		}))
		defer srv.Close()
		payload := base64.StdEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acc_test"}}`))
		provider := NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{
			ProviderID: "openai-codex",
			APIKey:     "aaa." + payload + ".bbb",
			BaseURL:    srv.URL,
			Model:      "gpt-6.2-sol",
		})
		defer func() {
			if err := provider.Close(); err != nil {
				t.Error(err)
			}
		}()
		stream, err := provider.Stream(context.Background(), NormalizeContext(Context{
			Messages: []Message{UserMessage{Content: UserText("Hello"), Timestamp: 1}},
		}), StreamOptions{Transport: TransportSSE})
		if err != nil {
			t.Fatal(err)
		}
		result := stream.Result()
		if result.StopReason != StopReasonStop {
			t.Fatalf("stopReason=%s, want stop; error=%s", result.StopReason, result.ErrorMessage)
		}
		if len(result.Diagnostics) != 1 {
			t.Fatalf("diagnostics=%+v, want 1", result.Diagnostics)
		}
		encoded, err := json.Marshal(result.Diagnostics)
		if err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf(`[{"type":"openai_input_transformations","timestamp":%d,"details":{"transformations":[%s]}}]`, result.Diagnostics[0].Timestamp, prefixMismatch)
		assertShapeJSON(t, encoded, want)
	})

	t.Run("records removals on Azure OpenAI Responses", func(t *testing.T) {
		t.Setenv("AZURE_OPENAI_BASE_URL", "")
		sse := fmt.Sprintf("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_azure\",\"status\":\"completed\",\"output\":[],\"input_transformations\":[%s,%s]}}\n\n", prefixMismatch, turnBoundary)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, sse)
		}))
		defer srv.Close()
		provider := NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{
			ProviderID: "azure-openai-responses",
			APIKey:     "azure-test-key",
			BaseURL:    srv.URL,
			Model:      "gpt-6.2-sol",
		})
		defer func() {
			if err := provider.Close(); err != nil {
				t.Error(err)
			}
		}()
		stream, err := provider.Stream(context.Background(), NormalizeContext(Context{
			Messages: []Message{UserMessage{Content: UserText("Hello"), Timestamp: 1}},
		}), StreamOptions{})
		if err != nil {
			t.Fatal(err)
		}
		result := stream.Result()
		if result.StopReason != StopReasonStop {
			t.Fatalf("stopReason=%s, want stop; error=%s", result.StopReason, result.ErrorMessage)
		}
		if len(result.Diagnostics) != 1 {
			t.Fatalf("diagnostics=%+v, want 1", result.Diagnostics)
		}
		encoded, err := json.Marshal(result.Diagnostics)
		if err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf(`[{"type":"openai_input_transformations","timestamp":%d,"details":{"transformations":[%s]}}]`, result.Diagnostics[0].Timestamp, prefixMismatch)
		assertShapeJSON(t, encoded, want)
	})
}
