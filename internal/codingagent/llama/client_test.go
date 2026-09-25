package llama

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Ports packages/coding-agent/test/llama-extension.test.ts client cases.

// sseHub registers /models/sse responses and broadcasts events to them, like
// the upstream test's `streams` set plus `send`.
type sseHub struct {
	mu      sync.Mutex
	streams map[http.ResponseWriter]chan string
}

func newSSEHub() *sseHub { return &sseHub{streams: map[http.ResponseWriter]chan string{}} }

func (h *sseHub) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	w.(http.Flusher).Flush()
	events := make(chan string, 16)
	h.mu.Lock()
	h.streams[w] = events
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.streams, w)
		h.mu.Unlock()
	}()
	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-events:
			_, _ = fmt.Fprintf(w, "data: %s\n\n", event)
			w.(http.Flusher).Flush()
		}
	}
}

func (h *sseHub) send(event any) {
	data, _ := json.Marshal(event)
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, events := range h.streams {
		events <- string(data)
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func listen(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		server.CloseClientConnections()
		server.Close()
	})
	return server.URL
}

func TestNormalizeLlamaServerURL(t *testing.T) {
	cases := []struct{ input, want string }{
		{"http://127.0.0.1:8080/v1/", "http://127.0.0.1:8080"},
		{"https://example.com/prefix/v1", "https://example.com/prefix"},
		{"\thttp://HOST:80/\n", "http://host"},
		{"http://host/a//v1", "http://host/a"},
	}
	for _, test := range cases {
		got, err := NormalizeLlamaServerURL(test.input)
		if err != nil || got != test.want {
			t.Errorf("NormalizeLlamaServerURL(%q) = %q, %v; want %q", test.input, got, err, test.want)
		}
	}
	if _, err := NormalizeLlamaServerURL("file:///tmp/llama"); err == nil || !strings.Contains(err.Error(), "http or https") {
		t.Fatalf("file URL error = %v, want http or https", err)
	}
	if _, err := NormalizeLlamaServerURL("127.0.0.1:8080"); err == nil || err.Error() != "Invalid URL" {
		t.Fatalf("schemeless URL error = %v, want Invalid URL", err)
	}
	if got, _ := LlamaInferenceURL("http://localhost:8080/v1"); got != "http://localhost:8080/v1" {
		t.Fatalf("LlamaInferenceURL = %q", got)
	}
}

func TestFormatBytes(t *testing.T) {
	cases := map[float64]string{
		512:                                    "512 B",
		1024:                                   "1.00 KiB",
		1152:                                   "1.13 KiB", // 1.125 is an exact tie; toFixed rounds up.
		10 * 1024:                              "10.0 KiB",
		5 * 1024 * 1024:                        "5.00 MiB",
		3.5 * 1024 * 1024 * 1024 * 1024 * 1024: "3584.0 TiB",
	}
	for input, want := range cases {
		if got := FormatBytes(input); got != want {
			t.Errorf("FormatBytes(%v) = %q, want %q", input, got, want)
		}
	}
}

func TestLoadAndWaitReportsSSEProgressAndWaitsForLoadedCatalogState(t *testing.T) {
	var mu sync.Mutex
	status := "unloaded"
	hub := newSSEHub()
	url := listen(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/models/sse":
			hub.serve(w, r)
		case r.URL.Path == "/models/load" && r.Method == http.MethodPost:
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["model"] != "test-model" || r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("load body = %v, content type %q", body, r.Header.Get("Content-Type"))
			}
			mu.Lock()
			status = "loading"
			mu.Unlock()
			writeJSON(w, map[string]any{"success": true})
			time.AfterFunc(20*time.Millisecond, func() {
				hub.send(map[string]any{"model": "test-model", "event": "status_change", "data": map[string]any{
					"status":   "loading",
					"progress": map[string]any{"stages": []string{"text_model", "mmproj_model"}, "current": "text_model", "value": 0.5},
				}})
				mu.Lock()
				status = "loaded"
				mu.Unlock()
				hub.send(map[string]any{"model": "test-model", "event": "status_change", "data": map[string]any{"status": "loaded"}})
			})
		case r.URL.Path == "/models":
			mu.Lock()
			current := status
			mu.Unlock()
			writeJSON(w, map[string]any{"data": []any{map[string]any{"id": "test-model", "status": map[string]any{"value": current}}}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	client, err := NewLlamaClient(url, "")
	if err != nil {
		t.Fatal(err)
	}
	var progress []LlamaProgress
	model, err := client.LoadAndWait(context.Background(), "test-model", func(entry LlamaProgress) { progress = append(progress, entry) })
	if err != nil {
		t.Fatal(err)
	}
	if model.Status.Value != LlamaModelStatusLoaded {
		t.Fatalf("status = %q, want loaded", model.Status.Value)
	}
	index := slices.IndexFunc(progress, func(entry LlamaProgress) bool { return entry.Message == "Loading text model" })
	if index < 0 {
		t.Fatalf("progress = %+v, want Loading text model", progress)
	}
	if ratio := progress[index].Ratio; ratio == nil || *ratio != 0.25 {
		t.Fatalf("stage ratio = %v, want 0.25", ratio)
	}
}

func TestDownloadAndWaitReportsByteProgressAndReturnsRefreshedCatalog(t *testing.T) {
	var mu sync.Mutex
	status := "missing"
	hub := newSSEHub()
	var reloadQueries []string
	url := listen(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/models/sse":
			hub.serve(w, r)
		case r.URL.Path == "/models" && r.Method == http.MethodPost:
			mu.Lock()
			status = "downloading"
			mu.Unlock()
			writeJSON(w, map[string]any{"success": true})
			time.AfterFunc(20*time.Millisecond, func() {
				hub.send(map[string]any{"model": "owner/repo:Q4_K_M", "event": "download_progress", "data": map[string]any{
					"progress": map[string]any{"https://example/model.gguf": map[string]any{"done": 512, "total": 1024}},
				}})
				mu.Lock()
				status = "unloaded"
				mu.Unlock()
				hub.send(map[string]any{"model": "owner/repo:Q4_K_M", "event": "download_finished", "data": map[string]any{}})
			})
		case strings.HasPrefix(r.URL.Path, "/models"):
			mu.Lock()
			current := status
			reloadQueries = append(reloadQueries, r.URL.RawQuery)
			mu.Unlock()
			data := []any{}
			if current != "missing" {
				data = append(data, map[string]any{"id": "owner/repo:Q4_K_M", "status": map[string]any{"value": current}})
			}
			writeJSON(w, map[string]any{"data": data})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	client, err := NewLlamaClient(url, "")
	if err != nil {
		t.Fatal(err)
	}
	var progress []LlamaProgress
	models, err := client.DownloadAndWait(context.Background(), "owner/repo:Q4_K_M", func(entry LlamaProgress) { progress = append(progress, entry) })
	if err != nil {
		t.Fatal(err)
	}
	want := []LlamaModelInfo{{ID: "owner/repo:Q4_K_M", Status: LlamaModelInfoStatus{Value: LlamaModelStatusUnloaded}}}
	if !reflect.DeepEqual(models, want) {
		t.Fatalf("models = %+v, want %+v", models, want)
	}
	half := 0.5
	wantProgress := LlamaProgress{Message: "Downloading model", Ratio: &half, Detail: "512 B / 1.00 KiB", keys: progressRatioKey | progressDetailKey}
	if !slices.ContainsFunc(progress, func(entry LlamaProgress) bool { return reflect.DeepEqual(entry, wantProgress) }) {
		t.Fatalf("progress = %+v, want %+v", progress, wantProgress)
	}
	mu.Lock()
	defer mu.Unlock()
	if last := reloadQueries[len(reloadQueries)-1]; last != "reload=1" {
		t.Fatalf("final catalog query = %q, want reload=1", last)
	}
}

func TestLoadAndWaitReportsExitCodeAndCancellation(t *testing.T) {
	url := listen(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models/sse":
			w.WriteHeader(http.StatusNotFound)
		case "/models/load":
			writeJSON(w, map[string]any{"success": true})
		case "/models":
			writeJSON(w, map[string]any{"data": []any{map[string]any{"id": "m", "status": map[string]any{"value": "unloaded", "failed": true, "exit_code": 3}}}})
		}
	})
	client, _ := NewLlamaClient(url, "")
	if _, err := client.LoadAndWait(context.Background(), "m", func(LlamaProgress) {}); err == nil || err.Error() != "Model exited with code 3" {
		t.Fatalf("error = %v, want exit code 3", err)
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(errors.New("Cancelled"))
	if _, err := client.LoadAndWait(ctx, "m", func(LlamaProgress) {}); err == nil || err.Error() != "Cancelled" {
		t.Fatalf("cancelled error = %v, want Cancelled", err)
	}
}

func TestClientRequestErrorsMirrorFetch(t *testing.T) {
	url := listen(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/models":
			w.WriteHeader(http.StatusServiceUnavailable)
			writeJSON(w, map[string]any{"error": map[string]any{"message": "Loading model"}})
		case "/props":
			w.WriteHeader(http.StatusTeapot)
		}
	})
	client, _ := NewLlamaClient(url, "secret")
	if _, err := client.List(context.Background(), false); err == nil || err.Error() != "Loading model" {
		t.Fatalf("list error = %v, want payload message", err)
	}
	if _, err := client.Props(context.Background(), ""); err == nil || err.Error() != "llama.cpp returned HTTP 418" {
		t.Fatalf("props error = %v, want HTTP 418", err)
	}

	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()
	offline, _ := NewLlamaClient(closedURL, "")
	var failure *fetchError
	if _, err := offline.List(context.Background(), false); !errors.As(err, &failure) || err.Error() != "fetch failed" {
		t.Fatalf("connection error = %v, want fetch failed", err)
	}
}

func TestListRejectsCatalogsOutsideRouterMode(t *testing.T) {
	payload := map[string]any{"data": []any{map[string]any{"id": "single"}}}
	url := listen(t, func(w http.ResponseWriter, r *http.Request) { writeJSON(w, payload) })
	client, _ := NewLlamaClient(url, "")
	if _, err := client.List(context.Background(), false); err == nil || err.Error() != "Server is not running in llama.cpp router mode" {
		t.Fatalf("error = %v", err)
	}
	payload = map[string]any{"object": "list"}
	if _, err := client.List(context.Background(), false); err == nil || err.Error() != "llama.cpp returned an invalid model catalog" {
		t.Fatalf("error = %v", err)
	}
}

func TestDispatchFramesHandlesSplitAndMalformedEvents(t *testing.T) {
	var events []LlamaModelEvent
	var decoder utf8StreamDecoder
	buffer := ""
	for _, chunk := range []string{"\uFEFFdata: {\"model\":\"m\",\"event\":\"e\",\"data\":{\"x\":\"\xc3", "\xa9\"}}\r\n\r\ndata: nope\n\n", "data: {\"model\":1}\n\n"} {
		buffer += strings.ReplaceAll(decoder.decode([]byte(chunk)), "\r\n", "\n")
		buffer = dispatchFrames(buffer, func(event LlamaModelEvent) { events = append(events, event) })
	}
	if len(events) != 1 || events[0].Model != "m" || events[0].Event != "e" {
		t.Fatalf("events = %+v", events)
	}
	if x, _ := stringField(events[0].Data, "x"); x != "é" {
		t.Fatalf("split multibyte payload = %q", x)
	}
}
