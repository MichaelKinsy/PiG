package main

import (
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

// TestEnableAnalyticsSendsNothing drives the real binary through a recording
// proxy. As in Pi 0.87.1, enableAnalytics is settings plumbing only: setting
// it true adds no outbound request compared with false.
func TestEnableAnalyticsSendsNothing(t *testing.T) {
	bin := buildPigBinaryForDiagnosticsTest(t)
	run := func(enabled string) []string {
		var mu sync.Mutex
		var requests []string
		proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			requests = append(requests, r.Method+" "+r.Host+r.URL.Path)
			mu.Unlock()
			w.WriteHeader(http.StatusBadGateway)
		}))
		defer proxy.Close()
		agentDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"enableAnalytics":`+enabled+`}`), 0o644); err != nil {
			t.Fatal(err)
		}
		home := t.TempDir()
		cmd := exec.Command(bin, "--model", "test-faux/faux-1", "--no-extensions", "--no-session", "--print", "TUI_LIVE_STREAM")
		cmd.Dir = t.TempDir()
		cmd.Env = append(os.Environ(),
			"HOME="+home,
			"PIG_HOME="+home,
			"PIG_CODING_AGENT_DIR="+agentDir,
			"PIG_TEST_FAUX=1",
			"PIG_TEST_FAUX_SCENARIO=parity-basic",
			"HTTP_PROXY="+proxy.URL, "HTTPS_PROXY="+proxy.URL, "http_proxy="+proxy.URL, "https_proxy="+proxy.URL,
			"NO_PROXY=", "no_proxy=",
		)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("pig --print (enableAnalytics %s): %v\n%s", enabled, err, stderr.String())
		}
		if strings.Contains(stderr.String(), "enableAnalytics") || strings.Contains(stderr.String(), "Invalid settings") {
			t.Fatalf("settings with enableAnalytics %s reported a problem:\n%s", enabled, stderr.String())
		}
		mu.Lock()
		defer mu.Unlock()
		slices.Sort(requests)
		return slices.Clone(requests)
	}
	off, on := run("false"), run("true")
	if !slices.Equal(on, off) {
		t.Fatalf("enableAnalytics true sent %v, false sent %v; want no extra request", on, off)
	}
}
