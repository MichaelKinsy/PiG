//go:build !pig_strip_mistral_conversations

package ai

import (
	"io"
	"net/http"
	"testing"
	"testing/synctest"
	"time"
)

// pi-mono 6b07b4e5 (#10609): the Mistral request timeout covers only the wait for response headers, so an active stream that
// outlasts it is not aborted, and a request that never gets headers fails with "Mistral response headers timed out after Nms".
func TestMistralTimeoutCoversOnlyResponseHeaders(t *testing.T) {
	for _, tc := range []struct {
		name    string
		timeout *int
		elapsed time.Duration
		want    string
	}{
		{"explicit timeout", new(5), 5 * time.Millisecond, "Mistral response headers timed out after 5ms"},
		{"default timeout", nil, 60 * time.Second, "Mistral response headers timed out after 60000ms"},
		{"zero timeout stays armed", new(0), 0, "Mistral response headers timed out after 0ms"},
	} {
		t.Run("headers never arrive: "+tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				start := time.Now()
				opts := StreamOptions{TimeoutMs: tc.timeout, Fetch: &http.Client{Transport: FetchFunction(func(req *http.Request) (*http.Response, error) {
					<-req.Context().Done()
					return nil, req.Context().Err()
				})}}
				stream, err := mistralUpstreamProvider(t).Stream(t.Context(), mistralUpstreamContext(), opts)
				if stream != nil || err == nil || err.Error() != tc.want {
					t.Fatalf("stream=%v err=%v, want %q", stream, err, tc.want)
				}
				if elapsed := time.Since(start); elapsed != tc.elapsed {
					t.Errorf("settled after %s, want %s", elapsed, tc.elapsed)
				}
			})
		})
	}

	t.Run("an active stream outlasts the request timeout", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			reader, writer := io.Pipe()
			opts := StreamOptions{TimeoutMs: new(20), Fetch: &http.Client{Transport: FetchFunction(func(*http.Request) (*http.Response, error) {
				resp := mistralUpstreamSSE("")
				resp.Body = reader
				return resp, nil
			})}}
			stream, err := mistralUpstreamProvider(t).Stream(t.Context(), mistralUpstreamContext(), opts)
			if err != nil {
				t.Fatal(err)
			}
			go func() {
				thinking := `data: {"choices":[{"index":0,"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"x"}]}]}}]}` + "\n\n"
				for range 5 {
					_, _ = io.WriteString(writer, thinking)
					time.Sleep(10 * time.Millisecond)
				}
				_, _ = io.WriteString(writer, mistralUpstreamTerminal)
				_ = writer.Close()
			}()
			message := stream.Result()
			if message.StopReason != StopReasonStop {
				t.Fatalf("stop=%s error=%q", message.StopReason, message.ErrorMessage)
			}
			thinking, ok := message.Content[0].(ThinkingContent)
			if !ok || thinking.Thinking != "xxxxx" {
				t.Fatalf("content = %+v", message.Content)
			}
		})
	})
}
