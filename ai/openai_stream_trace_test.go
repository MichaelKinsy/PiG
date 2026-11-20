package ai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func streamNoFinish(t *testing.T, body string, header http.Header) *AssistantMessage {
	t.Helper()
	return streamNoFinishCompat(t, body, header, nil)
}

func streamNoFinishCompat(t *testing.T, body string, header http.Header, compat *OpenAICompat) *AssistantMessage {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for name, values := range header {
			w.Header()[name] = values
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	provider := NewOpenAIProvider(OpenAIConfig{Model: "trace-model", ProviderID: "trace-fixture", APIKey: "test", BaseURL: server.URL, Compat: compat})
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return stream.Result()
}

func traceDiagnostic(t *testing.T, message *AssistantMessage) JsonObject {
	t.Helper()
	if message.StopReason != StopReasonError || message.ErrorMessage != "Stream ended without finish_reason" {
		t.Fatalf("stop=%v error=%q", message.StopReason, message.ErrorMessage)
	}
	for _, d := range message.Diagnostics {
		if d.Type == "stream_ended_without_finish_reason" {
			return d.Details
		}
	}
	t.Fatalf("no diagnostic in %#v", message.Diagnostics)
	return nil
}

func chunkLine(content string) string {
	return `data: {"id":"c1","model":"trace-model","choices":[{"index":0,"delta":{"content":` + traceJSON(content) + `}}]}` + "\n\n"
}

func traceJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestNoFinishReasonRecordsStreamEvidence(t *testing.T) {
	header := http.Header{}
	header.Set("X-Request-Id", "req-123")
	header.Set("X-GitHub-Request-Id", "ABCD:1234")
	header.Set("Set-Cookie", "session=secret")
	header.Set("X-Api-Key-Hint", "nope")
	body := chunkLine("a") + chunkLine("b") + chunkLine("c") + chunkLine("d") + "data: [DONE]\n\n"
	details := traceDiagnostic(t, streamNoFinish(t, body, header))
	headers := details["headers"].(JsonObject)
	if headers["x-request-id"] != "req-123" || headers["x-github-request-id"] != "ABCD:1234" {
		t.Fatalf("headers = %#v", headers)
	}
	if _, ok := headers["set-cookie"]; ok {
		t.Fatal("cookie kept")
	}
	if _, ok := headers["x-api-key-hint"]; ok {
		t.Fatal("key header kept")
	}
	if details["httpStatus"] != 200 || details["doneReceived"] != true || details["records"] != 4 {
		t.Fatalf("details = %#v", details)
	}
	last := details["lastRecords"].([]any)
	if len(last) != 3 || !strings.Contains(last[0].(string), `"content":"b"`) || !strings.Contains(last[2].(string), `"content":"d"`) {
		t.Fatalf("last = %#v", last)
	}
	if _, ok := details["msSinceRecord"].(int64); !ok {
		t.Fatalf("msSinceRecord = %#v", details["msSinceRecord"])
	}
}

func TestNoFinishReasonWithoutDoneBoundsAndRedacts(t *testing.T) {
	big := strings.Repeat("x", 3000)
	secret := "ghu_" + strings.Repeat("A", 30)
	body := chunkLine(big) + chunkLine("token "+secret) + `data: {"id":"c1","choices":[{"index":0,"delta":{}}],"authorization":"Bearer abcdefghijkl"}` + "\n\n"
	details := traceDiagnostic(t, streamNoFinish(t, body, nil))
	if details["doneReceived"] != false {
		t.Fatalf("done = %#v", details["doneReceived"])
	}
	total := 0
	for _, r := range details["lastRecords"].([]any) {
		s := r.(string)
		total += len(s)
		if strings.Contains(s, secret) || strings.Contains(s, "abcdefghijkl") {
			t.Fatalf("secret kept: %s", s)
		}
	}
	if total > openAIStreamTraceMaxBytes {
		t.Fatalf("records total %d bytes", total)
	}
}

func TestNoFinishReasonEmptyBodyAndDoneOnly(t *testing.T) {
	for _, test := range []struct {
		name, body string
		done       bool
	}{{"empty", "", false}, {"done-only", "data: [DONE]\n\n", true}} {
		t.Run(test.name, func(t *testing.T) {
			details := traceDiagnostic(t, streamNoFinish(t, test.body, nil))
			if details["records"] != 0 || details["bytes"] != 0 || details["doneReceived"] != test.done || details["msSinceRecord"] != nil || len(details["lastRecords"].([]any)) != 0 {
				t.Fatalf("empty trace = %#v", details)
			}
			if _, present := details["msToFirstRecord"]; present {
				t.Fatal("empty trace must not invent a first-record timing")
			}
		})
	}
}

func TestNoFinishReasonDisabledCompatHasNoDiagnostic(t *testing.T) {
	supports := false
	message := streamNoFinishCompat(t, chunkLine("ok"), nil, &OpenAICompat{SupportsFinishReason: &supports})
	if message.StopReason != StopReasonStop || len(message.Diagnostics) != 0 {
		t.Fatalf("disabled finish-reason contract changed: %#v", message)
	}
}

func TestNoFinishReasonTruncationKeepsUTF8AndIncludesMarkerInBudget(t *testing.T) {
	for _, limit := range []int{0, 1, 2, 3, 5, 32} {
		text := redactStreamTrace(strings.Repeat("界", 40), limit)
		if len(text) > limit || !utf8.ValidString(text) {
			t.Fatalf("limit=%d bytes=%d text=%q", limit, len(text), text)
		}
	}
}

func TestFinishedStreamHasNoTraceDiagnostic(t *testing.T) {
	body := chunkLine("ok") + `data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"
	message := streamNoFinish(t, body, nil)
	if message.StopReason != StopReasonStop {
		t.Fatalf("stop = %v %q", message.StopReason, message.ErrorMessage)
	}
	for _, d := range message.Diagnostics {
		if d.Type == "stream_ended_without_finish_reason" {
			t.Fatal("unexpected diagnostic")
		}
	}
}
