package ai_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/ai/src/api/openrouter-images.ts generateImages and parseUsage

// Hand-written chat completion bodies for OpenRouter image generation: empty and missing choices, content of every type, images as strings and objects,
// image URLs that are not data URLs or fail the `^data:([^;]+);base64,(.+)$` pattern, usage with absent, null and cached counts, and bodies that are not
// JSON objects. The pinned pi-ai and PiG must produce equal output, usage, stop reason and error message.
//
// Not covered: a response id that is not a non-empty string, and fractional or string token counts (the Go result holds a string id and integer counts).
func TestOpenRouterImagesEdgeResponsesMatchPi(t *testing.T) {
	img := func(url string) string { return `{"image_url":` + url + `}` }
	choice := func(message string) string { return `{"id":"gen-1","choices":[{"message":` + message + `}]}` }
	withImages := func(images ...string) string {
		return choice(`{"content":null,"images":[` + strings.Join(images, ",") + `]}`)
	}
	usage := func(u string) string {
		return `{"id":"gen-1","choices":[{"message":{"content":"x"}}],"usage":` + u + `}`
	}
	bodies := []string{
		choice(`{"content":"hello"}`),
		choice(`{"content":""}`),
		choice(`{"content":null}`),
		choice(`{}`),
		choice(`{"content":5}`),
		choice(`{"content":["a"]}`),
		choice(`{"content":{"x":1}}`),
		choice(`{"content":"text","images":[]}`),
		choice(`{"content":"text","images":null}`),
		withImages(img(`"data:image/png;base64,AAAA"`)),
		withImages(img(`{"url":"data:image/png;base64,AAAA"}`)),
		withImages(img(`"data:image/png;base64,AAAA"`), img(`"data:image/jpeg;base64,BBBB"`)),
		withImages(img(`"https://example.com/a.png"`), img(`"data:image/png;base64,CCCC"`)),
		withImages(img(`"data:image/png,AAAA"`)),
		withImages(img(`"data:image/png;charset=x;base64,AAAA"`)),
		withImages(img(`"data:;base64,AAAA"`)),
		withImages(img(`"data:image/png;base64,"`)),
		withImages(img(`"data:image/png;base64,AA\nAA"`)),
		withImages(img(`"data:image/png;base64,AA\rAA"`)),
		withImages(img(`"data:image/png;base64,AA\u2028AA"`)),
		withImages(img(`"data:image/png;base64,AA AA=="`)),
		withImages(img(`"DATA:image/png;base64,AAAA"`)),
		withImages(img(`"data:image/png;BASE64,AAAA"`)),
		withImages(img(`"data:image/svg+xml;base64,AAAA"`)),
		withImages(img(`{"url":5}`)),
		withImages(img(`{"url":null}`)),
		withImages(img(`{}`)),
		withImages(img(`null`)),
		withImages(img(`5`)),
		withImages(`{}`),
		withImages(`null`),
		withImages(`"data:image/png;base64,AAAA"`),
		withImages(`5`),
		choice(`{"content":"t","images":{"image_url":"data:image/png;base64,AAAA"}}`),
		choice(`{"content":"t","images":"data:image/png;base64,AAAA"}`),
		choice(`{"content":"t","images":5}`),
		choice(`{"content":"t","images":0}`),
		choice(`{"content":"t","images":true}`),
		choice(`{"content":"t","images":false}`),
		choice(`{"content":"t","images":[{"image_url":0}]}`),
		choice(`{"content":"t","images":[{"image_url":{"url":false}}]}`),
		choice(`{"content":"t","images":[{"image_url":{"url":""}}]}`),
		`{"choices":"abc"}`,
		`{"choices":[0]}`,
		`{"choices":[5]}`,
		`{"id":"gen-1","choices":[]}`,
		`{"id":"gen-1"}`,
		`{"id":"gen-1","choices":null}`,
		`{"id":"gen-1","choices":[null]}`,
		`{"id":"gen-1","choices":[{}]}`,
		`{"id":"gen-1","choices":[{"message":null}]}`,
		`{"id":"gen-1","choices":{"message":{"content":"x"}}}`,
		`{"id":"gen-1","choices":[{"message":{"content":"first"}},{"message":{"content":"second"}}]}`,
		`{"choices":[{"message":{"content":"no id"}}]}`,
		usage(`{"prompt_tokens":10,"completion_tokens":5}`),
		usage(`{"prompt_tokens":10,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":4}}`),
		usage(`{"prompt_tokens":10,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":4,"cache_write_tokens":3}}`),
		usage(`{"prompt_tokens":10,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":2,"cache_write_tokens":3}}`),
		usage(`{"prompt_tokens":10,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":4,"cache_write_tokens":0}}`),
		usage(`{"prompt_tokens":10,"completion_tokens":5,"prompt_tokens_details":null}`),
		usage(`{"prompt_tokens":10,"completion_tokens":5,"prompt_tokens_details":{}}`),
		usage(`{"prompt_tokens":3,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":4}}`),
		usage(`{"prompt_tokens":null,"completion_tokens":null}`),
		usage(`{}`),
		usage(`null`),
		usage(`0`),
		usage(`{"prompt_tokens":0,"completion_tokens":0}`),
		usage(`{"prompt_tokens":1000000,"completion_tokens":2000000}`),
		usage(`{"prompt_tokens":-5,"completion_tokens":-2}`),
		usage(`5`),
		usage(`"x"`),
		usage(`[]`),
		`{"id":"gen-1","usage":{"prompt_tokens":10,"completion_tokens":5}}`,
		`{"id":"gen-1","choices":[{"message":{"content":"x","images":[{"image_url":"data:image/png;base64,AAAA"}]}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
		`{}`,
		`[]`,
		`null`,
		`5`,
		`"str"`,
		`true`,
		``,
		`not json`,
		`{"error":{"message":"boom"}}`,
		`{"error":"plain"}`,
	}
	type probe struct {
		Status int    `json:"status,omitempty"`
		Body   string `json:"body"`
		Text   bool   `json:"text"`
	}
	var probes []probe
	for _, body := range bodies {
		probes = append(probes, probe{Body: body, Text: true}, probe{Body: body})
	}
	for _, status := range []int{400, 401, 429, 500} {
		for _, body := range []string{`{"error":{"message":"bad","code":"x"}}`, `plain text`, ``, `{"error":{"message":"m","metadata":{"raw":"rawinfo"}}}`} {
			probes = append(probes, probe{Status: status, Body: body})
		}
	}
	input, _ := json.Marshal(probes)
	cmd := exec.CommandContext(t.Context(), "node", "testdata/openrouter_images_response.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []map[string]any
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, p := range probes {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = bytes.NewBuffer(nil).ReadFrom(r.Body)
			w.Header().Set("Content-Type", "application/json")
			if p.Status != 0 {
				w.WriteHeader(p.Status)
			}
			_, _ = w.Write([]byte(p.Body))
		}))
		outputKinds := []string{"image"}
		if p.Text {
			outputKinds = []string{"image", "text"}
		}
		model := ai.ImageModel{ID: "m", Name: "m", API: ai.APIImagesOpenRouter, Provider: "openrouter", BaseURL: server.URL + "/v1", Input: []string{"text"}, Output: outputKinds, Cost: ai.ModelCost{Input: 1, Output: 2, CacheRead: 0.5, CacheWrite: 3}}
		maxRetries := 0
		got := ai.GenerateImagesOpenRouter(t.Context(), model, ai.ImagesContext{Input: []ai.ContentBlock{ai.TextContent{Text: "draw"}}}, ai.ImagesOptions{APIKey: "k", MaxRetries: &maxRetries})
		server.Close()
		gotMap := map[string]any{"api": string(got.API), "provider": got.Provider, "model": got.Model, "stopReason": string(got.StopReason), "output": []any{}}
		if got.ResponseID != "" {
			gotMap["responseId"] = got.ResponseID
		}
		if got.ErrorMessage != "" {
			gotMap["errorMessage"] = got.ErrorMessage
		}
		var outputs []any
		for _, item := range got.Output {
			raw, _ := json.Marshal(item)
			var decoded map[string]any
			_ = json.Unmarshal(raw, &decoded)
			delete(decoded, "textSignature")
			outputs = append(outputs, decoded)
		}
		if outputs != nil {
			gotMap["output"] = outputs
		}
		if got.Usage != nil {
			raw, _ := json.Marshal(got.Usage)
			var decoded map[string]any
			_ = json.Unmarshal(raw, &decoded)
			gotMap["usage"] = decoded
		}
		gotJSON, _ := json.Marshal(gotMap)
		wantJSON, _ := json.Marshal(want[i])
		if !bytes.Equal(gotJSON, wantJSON) {
			if failures++; failures <= 100 {
				t.Errorf("probe %d status %d text=%v body %q:\n PiG %s\n Pi  %s", i, p.Status, p.Text, p.Body, gotJSON, wantJSON)
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
