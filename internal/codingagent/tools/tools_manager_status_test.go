package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Ported from upstream test/tools-manager.test.ts "ensureTool": status goes
// through the callback, never the console.
func TestEnsureToolReportsStatusThroughCallback(t *testing.T) {
	t.Setenv("PIG_OFFLINE", "1")
	t.Setenv("PATH", t.TempDir())
	tm := NewToolsManager(t.TempDir())
	var statuses []ToolStatus
	stderr := captureStderr(t, func() {
		if got := tm.EnsureTool(context.Background(), "fd", func(s ToolStatus) { statuses = append(statuses, s) }); got != "" {
			t.Errorf("EnsureTool = %q", got)
		}
	})
	want := []ToolStatus{{Type: "warning", Message: "fd not found. Offline mode enabled, skipping download."}}
	if len(statuses) != 1 || statuses[0] != want[0] {
		t.Fatalf("statuses = %+v, want %+v", statuses, want)
	}
	if stderr != "" {
		t.Fatalf("EnsureTool wrote to the console: %q", stderr)
	}
}

func TestEnsureToolReportsDownloadFailure(t *testing.T) {
	t.Setenv("PIG_OFFLINE", "")
	t.Setenv("PI_OFFLINE", "")
	t.Setenv("PATH", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	tm := NewToolsManager(t.TempDir())
	tm.releaseBaseURL = srv.URL
	tm.platformOverride = "darwin"
	tm.archOverride = "arm64"
	var statuses []ToolStatus
	stderr := captureStderr(t, func() {
		tm.EnsureTool(context.Background(), "fd", func(s ToolStatus) { statuses = append(statuses, s) })
	})
	want := []ToolStatus{
		{Type: "info", Message: "fd not found. Downloading..."},
		{Type: "warning", Message: "Failed to download fd: Failed to resolve latest sharkdp/fd release: HTTP 500 without redirect"},
	}
	if !reflect.DeepEqual(statuses, want) {
		t.Fatalf("statuses = %+v, want %+v", statuses, want)
	}
	if stderr != "" {
		t.Fatalf("EnsureTool wrote to the console: %q", stderr)
	}
}

// statusCauseError models Error.message and Error.cause as separate values.
type statusCauseError struct {
	message string
	cause   error
}

func (e *statusCauseError) Error() string { return e.message }
func (e *statusCauseError) Unwrap() error { return e.cause }

type failedToolBody struct{ err error }

func (b failedToolBody) Read([]byte) (int, error) { return 0, b.err }
func (b failedToolBody) Close() error             { return nil }

// Upstream ensureTool joins distinct messages from at most five causes, including cycles.
// Fail during the asset body so this exercises the real installer without HTTP's URL wrapper.
func toolStatusFailureCases() []struct {
	name string
	err  error
	want string
} {
	cycle := &statusCauseError{message: "fetch failed"}
	cycle.cause = cycle
	return []struct {
		name string
		err  error
		want string
	}{
		{"plain", errors.New("boom"), "boom"},
		{"hidden cause", &statusCauseError{"fetch failed", errors.New("connect ETIMEDOUT")}, "fetch failed: connect ETIMEDOUT"},
		{"duplicate", &statusCauseError{"fetch failed", &statusCauseError{"fetch failed", errors.New("TLS failure")}}, "fetch failed: TLS failure"},
		{"wrapped Go error", fmt.Errorf("fetch failed: %w", errors.New("DNS failure")), "fetch failed: DNS failure"},
		{"wrapped hidden cause", fmt.Errorf("download: %w", &statusCauseError{"fetch failed", errors.New("DNS failure")}), "download: fetch failed: DNS failure"},
		{"depth cap", &statusCauseError{"one", &statusCauseError{"two", &statusCauseError{"three", &statusCauseError{"four", &statusCauseError{"five", errors.New("six")}}}}}, "one: two: three: four: five"},
		{"cycle", cycle, "fetch failed"},
		{"empty", &statusCauseError{"", errors.New("detail")}, ": detail"},
	}
}

func TestToolFailureMessage(t *testing.T) {
	for _, tc := range toolStatusFailureCases() {
		t.Run(tc.name, func(t *testing.T) {
			if got := toolFailureMessage(tc.err); got != tc.want {
				t.Errorf("toolFailureMessage = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEnsureToolFailureCauseChain(t *testing.T) {
	for _, tc := range toolStatusFailureCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PIG_OFFLINE", "")
			t.Setenv("PI_OFFLINE", "")
			t.Setenv("PATH", t.TempDir())
			tm := NewToolsManager(t.TempDir())
			tm.platformOverride, tm.archOverride = "linux", "arm64"
			tm.httpClient.Transport = toolsRoundTrip(func(req *http.Request) (*http.Response, error) {
				if strings.HasSuffix(req.URL.Path, "/releases/latest") {
					return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"/sharkdp/fd/releases/tag/test"}}, Body: http.NoBody}, nil
				}
				return &http.Response{StatusCode: http.StatusOK, Body: failedToolBody{tc.err}}, nil
			})
			var statuses []ToolStatus
			if path := tm.EnsureTool(t.Context(), "fd", func(s ToolStatus) { statuses = append(statuses, s) }); path != "" {
				t.Fatalf("failed download returned %q", path)
			}
			want := []ToolStatus{
				{Type: "info", Message: "fd not found. Downloading..."},
				{Type: "warning", Message: "Failed to download fd: " + tc.want},
			}
			if !reflect.DeepEqual(statuses, want) {
				t.Fatalf("statuses = %#v, want %#v", statuses, want)
			}
		})
	}
}

func TestEnsureToolSuccessfulStatusAndSilentReuse(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("PIG_OFFLINE", "")
	t.Setenv("PI_OFFLINE", "")
	assetName := toolsTable["fd"].GetAssetName("test", "linux", "arm64")
	asset := buildTarGz(t, "fd", []byte("fake binary"))
	srv := newFakeReleaseServer(t, "sharkdp/fd", "vtest", assetName, asset)
	tm := makeTestManager(t, srv, "linux", "arm64")
	var statuses []ToolStatus
	stderr := captureStderr(t, func() {
		path := tm.EnsureTool(t.Context(), "fd", func(s ToolStatus) { statuses = append(statuses, s) })
		if path != filepath.Join(tm.BinDir(), "fd") {
			t.Errorf("installed path = %q", path)
		}
		want := []ToolStatus{{"info", "fd not found. Downloading..."}, {"info", "fd installed to " + path}}
		if !reflect.DeepEqual(statuses, want) {
			t.Errorf("statuses = %#v, want %#v", statuses, want)
		}
		if reused := tm.EnsureTool(t.Context(), "fd", func(s ToolStatus) { t.Errorf("installed tool reported %+v", s) }); reused != path {
			t.Errorf("reused path = %q, want %q", reused, path)
		}
	})
	if stderr != "" {
		t.Fatalf("installer wrote to stderr: %q", stderr)
	}
	entries, err := os.ReadDir(tm.BinDir())
	if err != nil || len(entries) != 1 || entries[0].Name() != "fd" {
		t.Fatalf("install retained files besides fd: %v, %v", entries, err)
	}
}

// Pi grep/find call ensureTool without onStatus; missing tools appear only in their tool result.
func TestSearchToolEnsureRemainsSilent(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("PIG_OFFLINE", "1")
	binDir := filepath.Join(t.TempDir(), "bin")
	for _, tool := range CreateCodingTools(t.TempDir(), nil, binDir) {
		if tool.Name() != "grep" && tool.Name() != "find" {
			continue
		}
		stderr := captureStderr(t, func() {
			result, err := tool.Execute(t.Context(), "", json.RawMessage(`{"pattern":"missing"}`), nil)
			if err != nil || !result.IsError {
				t.Errorf("%s missing tool result = %+v, %v", tool.Name(), result, err)
			}
		})
		if stderr != "" {
			t.Errorf("%s ensureTool wrote to stderr: %q", tool.Name(), stderr)
		}
	}
}

// TestManagedToolCallbacksProbe pairs complete EnsureTool status records with pinned Pi.
func TestManagedToolCallbacksProbe(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("PIG_OFFLINE", "")
	t.Setenv("PI_OFFLINE", "")
	type status struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	type trace struct {
		Name     string   `json:"name"`
		Statuses []status `json:"statuses"`
	}
	var traces []trace
	for _, tc := range toolStatusFailureCases() {
		tm := NewToolsManager(t.TempDir())
		tm.platformOverride, tm.archOverride = "linux", "arm64"
		tm.httpClient.Transport = toolsRoundTrip(func(req *http.Request) (*http.Response, error) {
			if strings.HasSuffix(req.URL.Path, "/releases/latest") {
				return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"/sharkdp/fd/releases/tag/test"}}, Body: http.NoBody}, nil
			}
			return &http.Response{StatusCode: http.StatusOK, Body: failedToolBody{tc.err}}, nil
		})
		result := trace{Name: tc.name}
		tm.EnsureTool(t.Context(), "fd", func(s ToolStatus) { result.Statuses = append(result.Statuses, status(s)) })
		traces = append(traces, result)
	}
	t.Setenv("PI_OFFLINE", "1")
	for _, tool := range []string{"fd", "rg"} {
		result := trace{Name: "offline " + tool}
		NewToolsManager(t.TempDir()).EnsureTool(t.Context(), tool, func(s ToolStatus) { result.Statuses = append(result.Statuses, status(s)) })
		traces = append(traces, result)
	}
	encoded, err := json.Marshal(traces)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("MANAGED_TOOL_CALLBACKS %s\n", encoded)
}

// captureStderr returns what fn writes to os.Stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = saved }()
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()
	fn()
	_ = w.Close()
	out, _ := io.ReadAll(r)
	return string(out)
}
