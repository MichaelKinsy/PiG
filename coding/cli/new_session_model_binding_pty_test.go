//go:build linux

package cli

import (
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// modelCallRecorder is the provider endpoint of the test: it records each chat completion request and answers it with a streamed reply naming its number.
type modelCallRecorder struct {
	mu       sync.Mutex
	requests []modelCallRequest
}

type modelCallRequest struct{ Path, Authorization, Model string }

func (r *modelCallRecorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Model string `json:"model"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)
	r.mu.Lock()
	r.requests = append(r.requests, modelCallRequest{Path: req.URL.Path, Authorization: req.Header.Get("Authorization"), Model: body.Model})
	n := len(r.requests)
	r.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprintf(w, "data: {\"id\":\"c%d\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ANSWER-NUMBER-%d\"},\"finish_reason\":null}]}\n\n", n, body.Model, n)
	_, _ = fmt.Fprintf(w, "data: {\"id\":\"c%d\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":3,\"total_tokens\":8}}\n\n", n, body.Model)
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
}

func (r *modelCallRecorder) all() []modelCallRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]modelCallRequest(nil), r.requests...)
}

// waitForScreen waits until the terminal output after mark holds needle.
func waitForScreen(t *testing.T, o *ptyOutput, mark int, needle, what string) {
	t.Helper()
	deadline := time.Now().Add(testbudget.Wait(t))
	for !strings.Contains(string(o.since(mark)), needle) {
		if time.Now().After(deadline) {
			t.Fatalf("%s: the screen never showed %q; tail: %q", what, needle, tail(o.since(mark), 1500))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A user signed in to a provider runs /new and then picks a model that only the new session's registry lists (a model the provider's catalog gained after
// this process started, or a models.json entry added since). Pi builds that model from the new session's own services (interactive-mode.ts setModel over
// the runtime's ModelRuntime), so it keeps the listed context window and the provider's credential. The replacement session must carry the model builder
// over its own services: the start-up closure builds a model its registry does not list as an empty one (window 0, so auto-compaction at once) that the
// registry did not bind to the provider.
func TestInteractiveModelPickedAfterNewSessionKeepsItsListedWindowAndCredential(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary")
	}
	t.Parallel()
	storeRow := func(provider, id, baseURL string) ai.ModelsStoreEntry {
		row := fmt.Sprintf(`{"api":"openai-completions","baseUrl":%q,"contextWindow":400000,"cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0},"id":%q,"input":["text"],"maxTokens":32000,"name":%q,"provider":%q,"reasoning":false,"type":"chat"}`, baseURL, id, id, provider)
		models, err := ai.DecodeStoredModels([]json.RawMessage{json.RawMessage(row)})
		if err != nil {
			t.Fatal(err)
		}
		return ai.ModelsStoreEntry{Models: models, CheckedAt: new(1.0), LastModified: new(*ai.GetBuiltinModelDataGeneratedAt() + 1)}
	}
	writeJSON := func(path string, value any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	type m = map[string]any
	customModels := func(base string, ids ...string) m {
		var models []m
		for _, id := range ids {
			models = append(models, m{"id": id, "name": id, "contextWindow": 400000, "maxTokens": 32000, "input": []string{"text"}})
		}
		return m{"providers": m{"custom": m{"baseUrl": base + "/v1", "api": "openai-completions", "apiKey": "k-custom-FAKE", "models": models}}}
	}
	shapes := []struct {
		name, provider, start, listed, wantAuth string
		// seed writes the signed-in agent directory the process starts with; list makes the listed model visible to every Services built from then on.
		seed, list func(agentDir, base string)
	}{
		{
			name: "oauth", provider: "github-copilot", start: "gemini-3.8-flash", listed: "claude-fable-5.2", wantAuth: "Bearer tid=1;",
			seed: func(agentDir, base string) {
				writeJSON(filepath.Join(agentDir, "auth.json"), m{"github-copilot": m{"type": "oauth", "refresh": "ghu_FAKEREFRESH", "access": "tid=1;exp=4102444800;proxy-ep=" + strings.TrimPrefix(base, "https://"),
					"expires": time.Now().Add(time.Hour).UnixMilli(), "availableModelIds": []string{"gemini-3.8-flash", "claude-fable-5.2"}}})
			},
			list: func(agentDir, base string) {
				if err := ai.NewFileModelsStore(filepath.Join(agentDir, "models-store.json")).Write(t.Context(), "github-copilot", storeRow("github-copilot", "claude-fable-5.2", base)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "api key", provider: "groq", start: "openai/gpt-oss-120b", listed: "zz-remote-1", wantAuth: "Bearer gsk_FAKEKEY123456",
			seed: func(agentDir, base string) {
				writeJSON(filepath.Join(agentDir, "auth.json"), m{"groq": m{"type": "api_key", "key": "gsk_FAKEKEY123456"}})
				writeJSON(filepath.Join(agentDir, "models.json"), m{"providers": m{"groq": m{"baseUrl": base + "/openai/v1"}}})
			},
			list: func(agentDir, base string) {
				if err := ai.NewFileModelsStore(filepath.Join(agentDir, "models-store.json")).Write(t.Context(), "groq", storeRow("groq", "zz-remote-1", base+"/openai/v1")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "custom base URL", provider: "custom", start: "cm-1", listed: "cm-2", wantAuth: "Bearer k-custom-FAKE",
			seed: func(agentDir, base string) {
				writeJSON(filepath.Join(agentDir, "auth.json"), m{})
				writeJSON(filepath.Join(agentDir, "models.json"), customModels(base, "cm-1"))
			},
			list: func(agentDir, base string) {
				writeJSON(filepath.Join(agentDir, "models.json"), customModels(base, "cm-1", "cm-2"))
			},
		},
	}
	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()
			recorder := &modelCallRecorder{}
			server := httptest.NewTLSServer(recorder)
			t.Cleanup(server.Close)
			home := shortTempDir(t)
			agentDir := filepath.Join(home, "agent")
			seedFirstRunDone(t, agentDir)
			writeJSON(filepath.Join(agentDir, "settings.json"), m{"defaultProvider": shape.provider, "defaultModel": shape.start})
			shape.seed(agentDir, server.URL)
			certPath := filepath.Join(home, "server-ca.pem")
			if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600); err != nil {
				t.Fatal(err)
			}

			master, slave := openPTY(t, 40, 140)
			t.Cleanup(func() { _ = master.Close() })
			cmd := exec.Command(buildPigBinaryForSignalTest(t), "--no-extensions", "--session-dir", filepath.Join(home, "sessions"))
			cmd.Dir = t.TempDir()
			// Every address but the loopback test server is unreachable: a request that leaves the machine fails to connect.
			cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+home, "PI_HOME="+home, "PIG_CODING_AGENT_DIR="+agentDir, "PI_CODING_AGENT_DIR="+agentDir,
				"PIG_OFFLINE=1", "PI_OFFLINE=1", "PIG_SKIP_VERSION_CHECK=1", "PI_SKIP_VERSION_CHECK=1", "SSL_CERT_FILE="+certPath,
				"NO_PROXY=127.0.0.1,localhost", "no_proxy=127.0.0.1,localhost", "HTTPS_PROXY=http://127.0.0.1:1", "HTTP_PROXY=http://127.0.0.1:1", "ALL_PROXY=http://127.0.0.1:1", "https_proxy=http://127.0.0.1:1", "http_proxy=http://127.0.0.1:1", "TERM=xterm-256color")
			cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
			if err := cmd.Start(); err != nil {
				t.Fatalf("start pig: %v", err)
			}
			_ = slave.Close()
			exited := make(chan struct{})
			go func() { _ = cmd.Wait(); close(exited) }()
			t.Cleanup(func() {
				_ = cmd.Process.Kill()
				<-exited
			})
			out := &ptyOutput{}
			go func() { _, _ = io.Copy(out, master) }()
			send := func(text string) {
				t.Helper()
				if _, err := master.WriteString(text); err != nil {
					t.Fatal(err)
				}
			}

			waitForScreen(t, out, 0, "(auto)", "start-up")
			time.Sleep(time.Second)
			send("first question")
			time.Sleep(300 * time.Millisecond)
			send("\r")
			waitForScreen(t, out, 0, "ANSWER-NUMBER-1", "the first prompt, signed in")

			shape.list(agentDir, server.URL)
			mark := out.mark()
			send("/new")
			time.Sleep(300 * time.Millisecond)
			send("\r")
			waitForScreen(t, out, mark, "New session started", "/new")
			time.Sleep(time.Second)

			mark = out.mark()
			send("/model " + shape.listed)
			time.Sleep(time.Second)
			send("\r")
			time.Sleep(1500 * time.Millisecond)
			send("\r")
			waitForScreen(t, out, mark, "Model: "+shape.listed, "the model switch")
			waitForScreen(t, out, mark, "/400k", "the footer after the switch (the listed context window)")

			mark = out.mark()
			send("second question")
			time.Sleep(300 * time.Millisecond)
			send("\r")
			waitForScreen(t, out, mark, "ANSWER-NUMBER-2", "the prompt after /new and the switch")
			requests := recorder.all()
			if len(requests) != 2 {
				t.Fatalf("provider requests = %+v, want 2", requests)
			}
			if got := requests[1]; got.Model != shape.listed || !strings.HasPrefix(got.Authorization, shape.wantAuth) || !strings.HasSuffix(got.Path, "/chat/completions") {
				t.Fatalf("request after /new = %+v, want model %q with an Authorization beginning %q", got, shape.listed, shape.wantAuth)
			}
		})
	}
}
