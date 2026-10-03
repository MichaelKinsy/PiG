package evals

// Ports packages/evals/test/acme-server.test.ts.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type acmeFixture struct {
	mode                AcmeServerMode
	url                 func(*AcmeServer) string
	headers             map[string]string
	unauthorizedHeaders map[string]string
	model               string
	prompt              string
	response            string
	contentType         string
	unauthorized        string
	invalid             string
}

var acmeFixtures = []acmeFixture{
	{
		mode:                AcmeServerModeOpenAI,
		url:                 func(server *AcmeServer) string { return server.BaseURL() + "/chat/completions" },
		headers:             map[string]string{"authorization": "Bearer resolved-acme-key"},
		unauthorizedHeaders: map[string]string{"authorization": "Bearer wrong-key"},
		model:               OpenAIModelID,
		prompt:              OpenAIProbePrompt,
		response:            OpenAIProbeResponse,
		contentType:         "text/event-stream",
		unauthorized:        "Invalid Acme credential",
		invalid:             "Invalid OpenAI-compatible request",
	},
	{
		mode:                AcmeServerModeStream,
		url:                 func(server *AcmeServer) string { return server.Origin() + "/generate" },
		headers:             map[string]string{"x-acme-key": "resolved-stream-key"},
		unauthorizedHeaders: map[string]string{"x-acme-key": "wrong-key"},
		model:               StreamModelID,
		prompt:              StreamProbePrompt,
		response:            StreamProbeResponse,
		contentType:         "application/x-ndjson",
		unauthorized:        "Invalid Acme Stream credential",
		invalid:             "Invalid Acme Stream request",
	},
}

func acmeBody(model, prompt string, stream bool) map[string]any {
	return map[string]any{"model": model, "messages": []any{map[string]any{"role": "user", "content": prompt}}, "stream": stream}
}

func acmePost(t *testing.T, url string, headers map[string]string, payload any) (*http.Response, string) {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("content-type", "application/json")
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, string(body)
}

func streamedText(t *testing.T, mode AcmeServerMode, raw string) string {
	t.Helper()
	var text strings.Builder
	for line := range strings.SplitSeq(raw, "\n") {
		if line == "" {
			continue
		}
		if mode == AcmeServerModeStream {
			var event struct{ Type, Text string }
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				t.Fatal(err)
			}
			if event.Type == "text_delta" {
				text.WriteString(event.Text)
			}
			continue
		}
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok || data == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct{ Content string } `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			t.Fatal(err)
		}
		if len(chunk.Choices) > 0 {
			text.WriteString(chunk.Choices[0].Delta.Content)
		}
	}
	return text.String()
}

// TestAcmeServerUpstream ports packages/evals/test/acme-server.test.ts. Each subtest names one upstream case.
func TestAcmeServerUpstream(t *testing.T) {
	for _, fixture := range acmeFixtures {
		server := CreateAcmeServer(fixture.mode)
		if err := server.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := server.Stop(context.Background()); err != nil {
				t.Error(err)
			}
		})
		prefix := string(fixture.mode) + " fixture › "

		t.Run(prefix+"accepts the probe request and records it", func(t *testing.T) {
			// upstream: packages/evals/test/acme-server.test.ts:90
			server.Reset()
			response, body := acmePost(t, fixture.url(server), fixture.headers, acmeBody(fixture.model, fixture.prompt, true))
			if response.StatusCode != http.StatusOK {
				t.Fatalf("status = %d", response.StatusCode)
			}
			if contentType := response.Header.Get("content-type"); !strings.Contains(contentType, fixture.contentType) {
				t.Fatalf("content-type = %q", contentType)
			}
			if text := streamedText(t, fixture.mode, body); text != fixture.response {
				t.Fatalf("streamed text = %q, want %q", text, fixture.response)
			}
			if !server.ValidRequestReceived() {
				t.Fatal("probe was not recorded")
			}
		})

		t.Run(prefix+"rejects a bad credential with 401 and does not record a probe", func(t *testing.T) {
			// upstream: packages/evals/test/acme-server.test.ts:98
			server.Reset()
			response, body := acmePost(t, fixture.url(server), fixture.unauthorizedHeaders, acmeBody(fixture.model, fixture.prompt, true))
			if response.StatusCode != http.StatusUnauthorized || body != fixture.unauthorized {
				t.Fatalf("response = %d %q", response.StatusCode, body)
			}
			if server.ValidRequestReceived() {
				t.Fatal("rejected request was recorded as the probe")
			}
		})

		t.Run(prefix+"rejects a malformed request with 422 and does not record a probe", func(t *testing.T) {
			// upstream: packages/evals/test/acme-server.test.ts:109
			server.Reset()
			response, body := acmePost(t, fixture.url(server), fixture.headers, acmeBody(fixture.model, fixture.prompt, false))
			if response.StatusCode != http.StatusUnprocessableEntity || body != fixture.invalid {
				t.Fatalf("response = %d %q", response.StatusCode, body)
			}
			if server.ValidRequestReceived() {
				t.Fatal("rejected request was recorded as the probe")
			}
		})

		t.Run(prefix+"does not treat a non-probe success as the probe", func(t *testing.T) {
			// upstream: packages/evals/test/acme-server.test.ts:116
			server.Reset()
			response, _ := acmePost(t, fixture.url(server), fixture.headers, acmeBody(fixture.model, "hello", true))
			if response.StatusCode != http.StatusOK {
				t.Fatalf("status = %d", response.StatusCode)
			}
			if server.ValidRequestReceived() {
				t.Fatal("non-probe request was recorded as the probe")
			}
		})

		t.Run(prefix+"clears the probe flag on reset", func(t *testing.T) {
			// upstream: packages/evals/test/acme-server.test.ts:122
			server.Reset()
			acmePost(t, fixture.url(server), fixture.headers, acmeBody(fixture.model, fixture.prompt, true))
			if !server.ValidRequestReceived() {
				t.Fatal("probe was not recorded")
			}
			server.Reset()
			if server.ValidRequestReceived() {
				t.Fatal("reset kept the probe flag")
			}
		})
	}
}

// TestAcmeServerStreamsUpstreamBytes pins the fixture bodies byte for byte as upstream's acme-server.ts writes them
// (JSON.stringify keeps the authored key order), measured with Node v24.
func TestAcmeServerStreamsUpstreamBytes(t *testing.T) {
	for _, fixture := range acmeFixtures {
		server := CreateAcmeServer(fixture.mode)
		if err := server.Start(); err != nil {
			t.Fatal(err)
		}
		_, body := acmePost(t, fixture.url(server), fixture.headers, acmeBody(fixture.model, fixture.prompt, true))
		if err := server.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		want := "{\"type\":\"text_delta\",\"text\":\"ACME_\"}\n{\"type\":\"text_delta\",\"text\":\"STREAM_OK\"}\n" +
			"{\"type\":\"usage\",\"input_tokens\":4,\"output_tokens\":3}\n{\"type\":\"done\",\"reason\":\"stop\"}\n"
		if fixture.mode == AcmeServerModeOpenAI {
			want = "data: {\"id\":\"chatcmpl-acme\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"acme-chat\"," +
				"\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ACME_OK\"},\"finish_reason\":null}]}\n\n" +
				"data: {\"id\":\"chatcmpl-acme\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"acme-chat\"," +
				"\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2}}\n\n" +
				"data: [DONE]\n\n"
		}
		if body != want {
			t.Fatalf("%s body:\n got %q\nwant %q", fixture.mode, body, want)
		}
	}
}

// TestAcmeServerRejectsLikeUpstream pins acme-server.ts's rejection order and messages, measured with Node v24.
func TestAcmeServerRejectsLikeUpstream(t *testing.T) {
	server := CreateAcmeServer(AcmeServerModeOpenAI)
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Stop(context.Background()) })
	const path = "/v1/chat/completions"
	jsonAuth := map[string]string{"content-type": "application/json", "authorization": "Bearer resolved-acme-key"}
	for _, testCase := range []struct {
		name, path, method string
		headers            map[string]string
		body               string
		status             int
		message            string
	}{
		{"user content is not a string", path, http.MethodPost, jsonAuth, `{"model":"acme-chat","messages":[{"role":"system","content":"x"},{"role":"user","content":5}],"stream":true}`, 422, "Invalid OpenAI-compatible request"},
		{"payload is not an object", path, http.MethodPost, jsonAuth, `[1]`, 422, "Expected a JSON object"},
		{"payload is not JSON", path, http.MethodPost, jsonAuth, `{`, 400, "Invalid JSON"},
		{"content type is not JSON", path, http.MethodPost, map[string]string{"content-type": "text/plain", "authorization": "Bearer resolved-acme-key"}, `{}`, 415, "Expected application/json"},
		{"method is not POST", path, http.MethodGet, map[string]string{"authorization": "Bearer resolved-acme-key"}, ``, 405, "Expected POST"},
		{"path has a query", path + "?x=1", http.MethodPost, jsonAuth, `{}`, 404, "Unknown endpoint"},
		{"credential is missing", path, http.MethodPost, map[string]string{"content-type": "application/json; charset=utf-8"}, `{}`, 401, "Invalid Acme credential"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var body io.Reader
			if testCase.body != "" {
				body = strings.NewReader(testCase.body)
			}
			request, err := http.NewRequestWithContext(t.Context(), testCase.method, server.Origin()+testCase.path, body)
			if err != nil {
				t.Fatal(err)
			}
			for name, value := range testCase.headers {
				request.Header.Set(name, value)
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = response.Body.Close() }()
			content, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != testCase.status || response.Header.Get("content-type") != "text/plain" || string(content) != testCase.message {
				t.Fatalf("response = %d %q %q, want %d text/plain %q", response.StatusCode, response.Header.Get("content-type"), content, testCase.status, testCase.message)
			}
		})
	}
}
