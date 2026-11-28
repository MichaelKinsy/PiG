package cli

// pi: packages/coding-agent/src/cli/list-models.ts

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func listModelsServices(t *testing.T, agentDir string) *coding.AgentSessionServices {
	t.Helper()
	t.Setenv("PIG_HOME", t.TempDir())
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	return services
}

// list-models.ts:34-37 reports modelRuntime.getError() on stderr before it awaits the available models (:39), and the error does not end the
// listing: the table of the providers that have auth follows (:101-114). Both streams share one pipe here, so the order of the writes is the order read.
func TestListModelsWarnsOfTheLoadErrorBeforeTheTable(t *testing.T) {
	filterAllProviderEnv(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENCODE_API_KEY", "x")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	services := listModelsServices(t, dir)
	if services.ModelRuntime().GetError() == "" {
		t.Fatal("the malformed models.json produced no runtime error")
	}
	// Pi v1.7.0 (eee9f85fc): list-models.ts no longer prints the warning itself; createAgentSessionServices
	// emits `models.json error: ...` as a warning diagnostic and main.ts writes diagnostics to stderr before listModels().
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout, oldStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = w, w
	read := drainPipe(r)
	diags := []codingagent.AgentSessionRuntimeDiagnostic{{Type: "warning", Message: "models.json error: " + services.ModelRuntime().GetError()}}
	codingagent.WriteDiagnosticsToStderr(diags)
	listErr := listModels(t.Context(), services.ModelRuntime(), services.Registry().ModelRegistry, "")
	os.Stdout, os.Stderr = oldStdout, oldStderr
	_ = w.Close()
	result := <-read
	_ = r.Close()
	if listErr != nil || result.err != nil {
		t.Fatalf("listModels = %v, read = %v", listErr, result.err)
	}
	out := string(result.data)
	warning := strings.Index(out, "Warning: models.json error:")
	header := regexp.MustCompile(`(?m)^provider +model +context +max-out +thinking +images$`).FindStringIndex(out)
	if warning < 0 || header == nil {
		t.Fatalf("want the load warning and the table, got:\n%s", out)
	}
	if warning > header[0] {
		t.Fatalf("the load warning follows the table header, want it first:\n%s", out)
	}
	if !regexp.MustCompile(`(?m)^opencode +\S`).MatchString(out[header[1]:]) {
		t.Fatalf("the load error ended the listing: no opencode row after the header:\n%s", out)
	}
}

// main.ts:951 bounds listModels by AbortSignal.timeout(15_000) and list-models.ts:39 passes it to getAvailable: a pass the signal ends rejects
// (model-runtime.ts:381-386), so the call ends with the signal's error and no table line is printed.
func TestListModelsEndsAtItsSignalWithoutATable(t *testing.T) {
	filterAllProviderEnv(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENCODE_API_KEY", "x")
	services := listModelsServices(t, t.TempDir())
	fired, cancel := context.WithDeadline(t.Context(), time.Unix(0, 0))
	defer cancel()
	var err error
	out := captureStdout(func() {
		err = listModels(fired, services.ModelRuntime(), services.Registry().ModelRegistry, "")
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("listModels = %v, want the fired bound", err)
	}
	if out != "" {
		t.Fatalf("stdout = %q, want no table after the bound fired", out)
	}
	if got := uncaughtListModelsError(err); got != "DOMException [TimeoutError]: The operation was aborted due to timeout" {
		t.Fatalf("uncaughtListModelsError = %q, want AbortSignal.timeout's error", got)
	}
}

// The availability pass list-models.ts:39 awaits does no token refresh and no catalog request: a stored OAuth credential is available as it is
// (packages/ai/src/models.ts:697-699), also when its access token has expired, and a models.json provider with a literal key and its own base URL is
// available by its configured key (provider-composer.ts:450-456). Both list, and auth.json is left as it was.
func TestListModelsListsStoredOAuthAndConfiguredKeyProvidersWithoutRefreshing(t *testing.T) {
	filterAllProviderEnv(t)
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	auth := []byte(`{"openai-codex":{"type":"oauth","refresh":"fixture-refresh","access":"fixture-access","expires":1}}`)
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), auth, 0o600); err != nil {
		t.Fatal(err)
	}
	models := `{"providers":{"shape-test":{"baseUrl":"http://127.0.0.1:1/v1","api":"openai-completions","apiKey":"fixture","models":[{"id":"shape-model","name":"Shape model","contextWindow":1024,"maxTokens":64,"reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}]}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	services := listModelsServices(t, dir)
	var err error
	out := captureStdout(func() {
		err = listModels(t.Context(), services.ModelRuntime(), services.Registry().ModelRegistry, "")
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []string{`(?m)^openai-codex +\S`, `(?m)^shape-test +shape-model +1\.0K +64 +no +no *$`} {
		if !regexp.MustCompile(row).MatchString(out) {
			t.Fatalf("no row matches %s:\n%s", row, out)
		}
	}
	after, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	if err != nil || !bytes.Equal(after, auth) {
		t.Fatalf("auth.json after the listing = %q (%v), want it untouched", after, err)
	}
}
