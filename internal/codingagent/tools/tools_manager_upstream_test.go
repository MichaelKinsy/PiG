package tools

import (
	"errors"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestEnsureToolUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/tools-manager.test.ts:102
	t.Run("reports status through a callback without writing to the console", func(t *testing.T) {
		t.Setenv("PI_OFFLINE", "1")
		t.Setenv("PATH", t.TempDir())
		manager := NewToolsManager(t.TempDir())
		var statuses []ToolStatus
		output := captureToolConsole(t, func() {
			if result := manager.EnsureTool(t.Context(), "fd", func(status ToolStatus) { statuses = append(statuses, status) }); result != "" {
				t.Errorf("result = %q, want unavailable", result)
			}
		})
		want := []ToolStatus{{Type: "warning", Message: "fd not found. Offline mode enabled, skipping download."}}
		if !reflect.DeepEqual(statuses, want) || output != "" {
			t.Fatalf("statuses=%+v, want %+v; console=%q", statuses, want, output)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools-manager.test.ts:120
	t.Run("surfaces the error cause chain when a download fails", func(t *testing.T) {
		t.Setenv("PIG_OFFLINE", "")
		t.Setenv("PI_OFFLINE", "")
		t.Setenv("PATH", t.TempDir())
		manager := NewToolsManager(t.TempDir())
		manager.platformOverride, manager.archOverride = "linux", "arm64"
		cause := &statusCauseError{message: "fetch failed", cause: errors.New("connect ETIMEDOUT 140.82.113.3:443")}
		manager.httpClient.Transport = toolsRoundTrip(func(req *http.Request) (*http.Response, error) {
			if strings.HasSuffix(req.URL.Path, "/releases/latest") {
				return redirectTo("/sharkdp/fd/releases/tag/v10.4.2"), nil
			}
			return &http.Response{StatusCode: http.StatusOK, Body: failedToolBody{cause}}, nil
		})
		var statuses []ToolStatus
		if result := manager.EnsureTool(t.Context(), "fd", func(status ToolStatus) { statuses = append(statuses, status) }); result != "" {
			t.Fatalf("result=%q, want unavailable", result)
		}
		want := []ToolStatus{{Type: "info", Message: "fd not found. Downloading..."}, {Type: "warning", Message: "Failed to download fd: fetch failed: connect ETIMEDOUT 140.82.113.3:443"}}
		if !reflect.DeepEqual(statuses, want) {
			t.Fatalf("statuses=%+v, want %+v", statuses, want)
		}
	})
}

func captureToolConsole(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	savedOut, savedErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = w, w
	defer func() { os.Stdout, os.Stderr = savedOut, savedErr }()
	fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(output)
}
