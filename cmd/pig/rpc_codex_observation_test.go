package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// The first assistant message_start of `pig --mode rpc` over Codex SSE must equal Pi 0.87.1's. The oracle
// (coding/testdata/rpc33-observation/providers/openai-codex-responses/probe.mjs) ran the real Pi CLI against the same
// wire fixtures; Pi is deterministic there, so every Go run must reproduce it. PIG_D82_ORACLE_RUNS raises the run count.

const codexRPCOraclePath = "../../coding/testdata/rpc33-observation/providers/openai-codex-responses/pi.json"

func codexRPCOracleRuns(t *testing.T) int {
	t.Helper()
	runs := 20
	if value := os.Getenv("PIG_D82_ORACLE_RUNS"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 {
			t.Fatalf("PIG_D82_ORACLE_RUNS=%q", value)
		}
		runs = parsed
	}
	return runs
}

type codexRPCStart struct {
	Content    json.RawMessage `json:"content"`
	StopReason string          `json:"stopReason"`
	ResponseID *string         `json:"responseId"`
	Usage      struct {
		TotalTokens int `json:"totalTokens"`
	} `json:"usage"`
}

// codexRPCServer answers requests alternately with the fixture (tool call) and a text reply that ends the run. A pending
// fixture sends headers first and its body only after release is closed, that is after the RPC start was observed.
func codexRPCServer(t *testing.T, mode, fixture string, release <-chan struct{}) *httptest.Server {
	t.Helper()
	text := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r2\"}}\n\n" +
		"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"m2\",\"role\":\"assistant\",\"status\":\"in_progress\",\"content\":[]}}\n\n" +
		"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"m2\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\",\"annotations\":[]}]}}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r2\",\"status\":\"completed\"}}\n\n"
	var mu sync.Mutex
	count := 0
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		mu.Lock()
		count++
		first := count%2 == 1
		mu.Unlock()
		body := text
		if first {
			body = fixture
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		const head = "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n"
		if mode == "buffered" || !first {
			// One write, like Node's res.writeHead + res.end(body).
			_, _ = fmt.Fprintf(conn, "%sContent-Length: %d\r\n\r\n%s", head, len(body), body)
			return
		}
		_, _ = io.WriteString(conn, head+"Transfer-Encoding: chunked\r\n\r\n")
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = fmt.Fprintf(conn, "%x\r\n%s\r\n0\r\n\r\n", len(body), body)
	}))
	server.Start()
	t.Cleanup(server.Close)
	return server
}

func TestRPCCodexSSEStartMatchesPi(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(codexRPCOraclePath)
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Fixtures map[string]string          `json:"fixtures"`
		RPC      map[string]json.RawMessage `json:"rpc"`
	}
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	claims, _ := json.Marshal(map[string]any{"https://api.openai.com/auth": map[string]string{"chatgpt_account_id": "acct"}})
	token := "h." + base64.RawURLEncoding.EncodeToString(claims) + ".s"
	runs := codexRPCOracleRuns(t)
	for _, mode := range []string{"buffered", "pending"} {
		t.Run(mode, func(t *testing.T) {
			var want struct {
				Content    json.RawMessage `json:"content"`
				StopReason string          `json:"stopReason"`
				ResponseID *string         `json:"responseId"`
				Usage      int             `json:"usage"`
			}
			if err := json.Unmarshal(oracle.RPC[mode+"/tool"], &want); err != nil {
				t.Fatal(err)
			}
			for run := range runs {
				t.Run(fmt.Sprintf("run-%d", run), func(t *testing.T) {
					release := make(chan struct{})
					server := codexRPCServer(t, mode, oracle.Fixtures["tool"], release)
					dir := t.TempDir()
					agent := filepath.Join(dir, "agent")
					if err := os.MkdirAll(agent, 0o755); err != nil {
						t.Fatal(err)
					}
					models, _ := json.Marshal(map[string]any{"providers": map[string]any{"p": map[string]any{
						"api": "openai-codex-responses", "baseUrl": server.URL, "apiKey": token,
						"models": []any{map[string]any{"id": "strict", "name": "strict", "reasoning": false, "input": []string{"text"}, "cost": map[string]int{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 128000, "maxTokens": 1000}},
					}}})
					for name, content := range map[string]string{"models.json": string(models), "settings.json": `{"transport":"sse"}`} {
						if err := os.WriteFile(filepath.Join(agent, name), []byte(content), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					if err := os.WriteFile(filepath.Join(dir, "parity-read-target.txt"), []byte("x\n"), 0o600); err != nil {
						t.Fatal(err)
					}
					p := startRPCProcessAt(t, dir, []string{"HOME=" + dir, "PIG_HOME=" + filepath.Join(dir, "home"), "PIG_CODING_AGENT_DIR=" + agent},
						"--offline", "--no-extensions", "--model", "p/strict", "--no-context-files", "--no-skills", "--no-session")
					p.send(`{"id":"read","type":"prompt","message":"READ"}`)
					var start map[string]any
					p.await("assistant message_start", func(record rpcRecord) bool {
						message, _ := record["message"].(map[string]any)
						if record["type"] == "message_start" && message["role"] == "assistant" {
							start = message
							return true
						}
						return false
					})
					close(release)
					encoded, _ := json.Marshal(start)
					var got codexRPCStart
					if err := json.Unmarshal(encoded, &got); err != nil {
						t.Fatal(err)
					}
					var gotContent, wantContent any
					_ = json.Unmarshal(got.Content, &gotContent)
					_ = json.Unmarshal(want.Content, &wantContent)
					if !reflect.DeepEqual(gotContent, wantContent) || got.StopReason != want.StopReason || !reflect.DeepEqual(got.ResponseID, want.ResponseID) || got.Usage.TotalTokens != want.Usage {
						t.Fatalf("start = %s\nPi = %s", strings.TrimSpace(string(encoded)), strings.TrimSpace(string(oracle.RPC[mode+"/tool"])))
					}
				})
			}
		})
	}
}
