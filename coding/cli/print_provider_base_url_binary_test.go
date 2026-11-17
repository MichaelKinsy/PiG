package cli

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// Pi gives no provider id a base URL of its own: a models.json provider that defines models must state one (provider-composer.ts:224-225), so
// a file without it does not load and the model is not found, whatever the provider is called and whether or not OLLAMA_HOST is set. Pi 1.5.2
// print mode answers `Error: Model "ollama/m1" not found. Use --list-models to see available models.` with exit code 1 and sends nothing; with
// a baseUrl the request goes to that URL and not to OLLAMA_HOST (RUN 2026-11-13, both rows, in print, RPC and interactive mode).
func TestPrintModeGivesTheProviderIDOllamaNoBaseURLOfItsOwn(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	for _, tc := range []struct {
		name, provider string
		baseURL        bool
	}{
		{name: "ollama without a base URL", provider: "ollama"},
		{name: "ollama with a base URL", provider: "ollama", baseURL: true},
		{name: "another provider without a base URL", provider: "mylocal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var paths []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				mu.Lock()
				paths = append(paths, r.URL.Path)
				mu.Unlock()
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, `data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"m1","choices":[{"index":0,"delta":{"role":"assistant","content":"PONG"},"finish_reason":null}]}`+"\n\n"+
					`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"m1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`+"\n\n"+
					"data: [DONE]\n\n")
			}))
			t.Cleanup(server.Close)
			home, cwd := t.TempDir(), t.TempDir()
			agentDir := filepath.Join(home, "agent")
			if err := os.MkdirAll(agentDir, 0o700); err != nil {
				t.Fatal(err)
			}
			provider := map[string]any{"api": "openai-completions", "apiKey": "fixture", "models": []any{map[string]any{"id": "m1"}}}
			ollamaHost := server.URL + "/host/v1"
			if tc.baseURL {
				provider["baseUrl"] = server.URL + "/own/v1"
			}
			models, err := json.Marshal(map[string]any{"providers": map[string]any{tc.provider: provider}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(agentDir, "models.json"), models, 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), binary, "--model", tc.provider+"/m1", "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session", "--print", "hello")
			cmd.Dir = cwd
			// A request to any host but the listener ends at the closed proxy port: the test never reaches a real provider.
			cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR="+agentDir, "PIG_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1",
				"OLLAMA_HOST="+ollamaHost, "HTTPS_PROXY=http://127.0.0.1:1", "HTTP_PROXY=http://127.0.0.1:1", "NO_PROXY=127.0.0.1,localhost")
			cmd.Stdin = strings.NewReader("")
			var stdout, stderr strings.Builder
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			runErr := cmd.Run()
			mu.Lock()
			defer mu.Unlock()
			if tc.baseURL {
				if runErr != nil || stdout.String() != "PONG\n" || !slices.Equal(paths, []string{"/own/v1/chat/completions"}) {
					t.Fatalf("err=%v stdout=%q stderr=%q requests=%q, want PONG from /own/v1/chat/completions", runErr, stdout.String(), stderr.String(), paths)
				}
				return
			}
			var exit *exec.ExitError
			// Pi c8c456c85c6e (main.ts:917-918): the models.json provider does not load, so no model is available and the error is followed by the no-models warning (probed on Pi).
			want := `Error: Model "` + tc.provider + `/m1" not found. Use --list-models to see available models.` + "\n" + noModelsWarningCut
			if !errors.As(runErr, &exit) || exit.ExitCode() != 1 || stdout.String() != "" || !strings.HasSuffix(cutDocsRoot(stderr.String()), want) || len(paths) != 0 {
				t.Fatalf("err=%v stdout=%q stderr=%q requests=%q, want exit 1 with %q and no request", runErr, stdout.String(), stderr.String(), paths, want)
			}
		})
	}
}
