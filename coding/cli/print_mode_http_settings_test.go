package cli

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi's main.ts applies the runtime settings' httpProxy and configureHttpDispatcher(settingsManager.getHttpIdleTimeoutMs()) right after
// createAgentSessionRuntime, before help, --list-models and every mode. PiG applied them only on the interactive path, so print, JSON and
// RPC requests kept the 5-minute default (and the provider retry settings). Probed: with httpIdleTimeoutMs 1500 and a google baseUrl
// that never answers, Pi 1.1.0 prints "fetch failed" after 1.5 s; PiG waited for the 5-minute default.
func TestPrintModeAppliesTheHTTPIdleTimeoutSettingLikePi(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })

	agentDir := t.TempDir()
	workDir := filepath.Join(agentDir, "work")
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(`{"providers":{"google":{"baseUrl":"`+server.URL+`/v1"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"httpIdleTimeoutMs":300,"retry":{"enabled":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := testbudget.Context(t)
	cmd := exec.CommandContext(ctx, bin, "--offline", "--no-extensions", "--model", "google/gemini-2.5-pro", "-p", "hi")
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "PIG_HOME="+t.TempDir(), "PIG_CODING_AGENT_DIR="+agentDir, "GEMINI_API_KEY=k", "NO_PROXY=127.0.0.1", "no_proxy=127.0.0.1")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := testenv.Run(t, cmd)
	if ctx.Err() != nil {
		t.Fatalf("print mode ignored httpIdleTimeoutMs and waited for the default; stderr %q", stderr.String())
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); !ok || exitErr.ExitCode() != 1 || strings.TrimSpace(stderr.String()) != "fetch failed" {
		t.Fatalf("err %v, stderr %q; want exit 1 with %q", err, stderr.String(), "fetch failed")
	}
}
