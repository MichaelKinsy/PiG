package cli

// pi: packages/coding-agent/src/core/provider-composer.ts

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

const unknownAPIRefusal = "No API provider registered for api: "

func unknownAPIListener(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	requests := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		requests.Add(1)
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"listener says no"}}`))
	}))
	t.Cleanup(server.Close)
	return server.URL + "/v1", requests
}

// unknownAPIHome is one scratch home: every directory pig or Pi would read is inside it.
type unknownAPIHome struct{ root, agent, cwd string }

func newUnknownAPIHome(t *testing.T, models, settings, auth string) unknownAPIHome {
	t.Helper()
	root := shortTempDir(t)
	home := unknownAPIHome{root: root, agent: filepath.Join(root, "agent"), cwd: filepath.Join(root, "cwd")}
	for _, dir := range []string{home.agent, home.cwd, filepath.Join(root, "other")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	home.write(t, "models.json", models)
	if settings != "" {
		home.write(t, "settings.json", settings)
	}
	if auth != "" {
		home.write(t, "auth.json", auth)
	}
	return home
}

func (home unknownAPIHome) write(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home.agent, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (home unknownAPIHome) command(ctx context.Context, bin string, extraEnv []string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bin, append([]string{"--offline", "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-context-files"}, args...)...)
	cmd.Dir = home.cwd
	other := filepath.Join(home.root, "other")
	cmd.Env = append(append(os.Environ(), "HOME="+home.root, "PIG_HOME="+home.agent, "PIG_CODING_AGENT_DIR="+home.agent, "PI_HOME="+other, "PI_CODING_AGENT_DIR="+other, "TERM=dumb"), extraEnv...)
	return cmd
}

func (home unknownAPIHome) run(t *testing.T, bin string, extraEnv []string, args ...string) (string, string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testbudget.Wait(t))
	defer cancel()
	cmd := home.command(ctx, bin, extraEnv, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("run pig: %v\nstderr: %s", err, stderr.String())
	}
	return stdout.String(), stderr.String(), code
}

func unknownAPIModels(baseURL, api string) string {
	return fmt.Sprintf(`{"providers":{"odd":{"baseUrl":%q,"apiKey":"k","api":%q,"models":[{"id":"m"}]}}}`, baseURL, api)
}

// A model whose api names no registered API implementation is refused when its first request starts, with Pi's text, exit status and
// stop reason, and nothing is sent to its endpoint (provider-composer.ts:603-604).
// PiG 0.3.0 through 0.7.0 sent the conversation to the endpoint as an OpenAI chat-completions request.
func TestUnknownAPINamePrintSendsNothingAndReportsPiText(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	fixture, err := filepath.Abs(filepath.Join("testdata", "unknown-api-provider.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		name                   string
		models, settings, auth func(baseURL string) string
		args                   []string
		api                    string
		wantStderr             string
	}{
		{name: "provider-api/nosuchapi", api: "nosuchapi", args: []string{"--model", "odd/m"}},
		{name: "provider-api/constructor", api: "constructor", args: []string{"--model", "odd/m"}},
		{name: "provider-api/__proto__", api: "__proto__", args: []string{"--model", "odd/m"}},
		{name: "provider-api/toString", api: "toString", args: []string{"--model", "odd/m"}},
		{name: "model-api", api: "nosuchapi", args: []string{"--model", "odd/m"}, models: func(baseURL string) string {
			return fmt.Sprintf(`{"providers":{"odd":{"baseUrl":%q,"apiKey":"k","api":"openai-completions","models":[{"id":"m","api":"nosuchapi"}]}}}`, baseURL)
		}},
		{name: "built-in-provider-model-api", api: "nosuchapi", args: []string{"--model", "openai/m"}, models: func(baseURL string) string {
			return fmt.Sprintf(`{"providers":{"openai":{"baseUrl":%q,"apiKey":"k","models":[{"id":"m","api":"nosuchapi"}]}}}`, baseURL)
		}},
		{name: "oauth-provider-model-api", api: "nosuchapi", args: []string{"--model", "anthropic/m"}, models: func(baseURL string) string {
			return fmt.Sprintf(`{"providers":{"anthropic":{"baseUrl":%q,"models":[{"id":"m","api":"nosuchapi"}]}}}`, baseURL)
		}, auth: func(string) string {
			return `{"anthropic":{"type":"oauth","access":"access-token","refresh":"refresh-token","expires":4102444800000}}`
		}},
		{name: "settings-default-model", api: "nosuchapi", settings: func(string) string { return `{"defaultProvider":"odd","defaultModel":"m"}` }},
		{name: "extension-registered-provider", api: "nosuchapi", args: []string{"--model", "extp/m", "-e", fixture}, models: func(string) string { return `{}` }},
		// The credential is resolved before the api is looked up (model-runtime.ts:725-729 prepareRequest, then provider.stream), so its failure is the error.
		{name: "failing-key-first", api: "nosuchapi", args: []string{"--model", "odd/m"}, models: func(baseURL string) string {
			return fmt.Sprintf(`{"providers":{"odd":{"baseUrl":%q,"apiKey":"!exit 1","api":"nosuchapi","models":[{"id":"m"}]}}}`, baseURL)
		}, wantStderr: `API key auth failed for provider odd: Failed to resolve API key for provider "odd" from shell command: exit 1`},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			baseURL, requests := unknownAPIListener(t)
			models, settings, auth := unknownAPIModels(baseURL, row.api), "", ""
			if row.models != nil {
				models = row.models(baseURL)
			}
			if row.settings != nil {
				settings = row.settings(baseURL)
			}
			if row.auth != nil {
				auth = row.auth(baseURL)
			}
			home := newUnknownAPIHome(t, models, settings, auth)
			stdout, stderr, code := home.run(t, bin, []string{"UNKNOWN_API_BASE_URL=" + baseURL}, append(append([]string{"--no-session"}, row.args...), "-p", "hello")...)
			want := row.wantStderr
			if want == "" {
				want = unknownAPIRefusal + row.api
			}
			if got := strings.TrimSpace(stderr); got != want {
				t.Errorf("stderr = %q, want %q", got, want)
			}
			if code != 1 || stdout != "" {
				t.Errorf("exit = %d, stdout = %q, want exit 1 and no output", code, stdout)
			}
			if got := requests.Load(); got != 0 {
				t.Errorf("requests at the endpoint = %d, want 0", got)
			}
		})
	}
}

// JSON mode ends the turn with the refusal as the assistant error and exits 0, as Pi does.
func TestUnknownAPINameJSONModeSendsNothingAndReportsPiText(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary")
	}
	t.Parallel()
	bin := buildPigBinaryForSignalTest(t)
	baseURL, requests := unknownAPIListener(t)
	home := newUnknownAPIHome(t, unknownAPIModels(baseURL, "nosuchapi"), "", "")
	stdout, stderr, code := home.run(t, bin, nil, "--no-session", "--model", "odd/m", "--mode", "json", "-p", "hello")
	message := unknownAPITurnEnd(t, strings.NewReader(stdout))
	if message.StopReason != "error" || message.ErrorMessage != unknownAPIRefusal+"nosuchapi" || message.API != "nosuchapi" {
		t.Errorf("turn_end message = %+v", message)
	}
	if code != 0 || requests.Load() != 0 {
		t.Errorf("exit = %d, requests = %d, want 0 and 0\nstderr: %s", code, requests.Load(), stderr)
	}
}

// A session written while the provider's api was known resumes onto the same model after models.json names an unknown api: the next
// prompt is refused and nothing is sent.
func TestUnknownAPINameResumedSessionSendsNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary")
	}
	t.Parallel()
	bin := buildPigBinaryForSignalTest(t)
	baseURL, requests := unknownAPIListener(t)
	home := newUnknownAPIHome(t, unknownAPIModels(baseURL, "openai-completions"), "", "")
	sessions := filepath.Join(home.root, "sessions")
	if _, stderr, code := home.run(t, bin, nil, "--session-dir", sessions, "--model", "odd/m", "-p", "hello"); code != 1 || requests.Load() != 1 {
		t.Fatalf("first run with the known api: exit %d, requests %d, stderr %q; want exit 1 after one request", code, requests.Load(), stderr)
	}
	home.write(t, "models.json", unknownAPIModels(baseURL, "nosuchapi"))
	stdout, stderr, code := home.run(t, bin, nil, "--session-dir", sessions, "-c", "-p", "again")
	if got, want := strings.TrimSpace(stderr), unknownAPIRefusal+"nosuchapi"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
	if code != 1 || stdout != "" || requests.Load() != 1 {
		t.Errorf("exit = %d, stdout = %q, requests = %d; want exit 1, no output and no request after the first run's", code, stdout, requests.Load())
	}
}

type unknownAPIMessage struct {
	API          string `json:"api"`
	StopReason   string `json:"stopReason"`
	ErrorMessage string `json:"errorMessage"`
}

func unknownAPITurnEnd(t *testing.T, output io.Reader) unknownAPIMessage {
	t.Helper()
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for scanner.Scan() {
		var event struct {
			Type    string            `json:"type"`
			Message unknownAPIMessage `json:"message"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err == nil && event.Type == "turn_end" {
			return event.Message
		}
	}
	t.Fatalf("no turn_end event (%v)", scanner.Err())
	return unknownAPIMessage{}
}

// RPC: the prompt is accepted and its turn ends with stopReason "error" and Pi's text, for a model given at startup and for one selected by set_model.
func TestUnknownAPINameRPCSendsNothingAndReportsPiText(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	for _, row := range []struct {
		name     string
		args     []string
		commands string
	}{
		{name: "startup-model", args: []string{"--model", "odd/m"}, commands: `{"id":"1","type":"prompt","message":"hello"}` + "\n"},
		{name: "set_model", commands: `{"id":"1","type":"set_model","provider":"odd","modelId":"m"}` + "\n" + `{"id":"2","type":"prompt","message":"hello"}` + "\n"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			baseURL, requests := unknownAPIListener(t)
			home := newUnknownAPIHome(t, unknownAPIModels(baseURL, "nosuchapi"), "", "")
			ctx, cancel := context.WithTimeout(context.Background(), testbudget.Wait(t))
			defer cancel()
			cmd := home.command(ctx, bin, nil, append([]string{"--no-session", "--mode", "rpc"}, row.args...)...)
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(stdin, row.commands); err != nil {
				t.Fatal(err)
			}
			message := unknownAPITurnEnd(t, stdout)
			_ = stdin.Close()
			_, _ = io.Copy(io.Discard, stdout)
			if err := cmd.Wait(); err != nil {
				t.Errorf("rpc exit: %v\nstderr: %s", err, stderr.String())
			}
			if message.StopReason != "error" || message.ErrorMessage != unknownAPIRefusal+"nosuchapi" || message.API != "nosuchapi" {
				t.Errorf("turn_end message = %+v", message)
			}
			if got := requests.Load(); got != 0 {
				t.Errorf("requests at the endpoint = %d, want 0", got)
			}
		})
	}
}
