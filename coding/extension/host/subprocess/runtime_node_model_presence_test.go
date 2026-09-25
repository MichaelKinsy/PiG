package subprocess_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

func TestConnectedNodeProviderPreservesModelListPresence(t *testing.T) {
	parent := "/tmp"
	if runtime.GOOS == "windows" {
		parent = os.TempDir()
	}
	sockDir, err := os.MkdirTemp(parent, "pig-model-presence-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	t.Setenv("XDG_RUNTIME_DIR", sockDir)
	t.Setenv("TMPDIR", sockDir)
	t.Setenv("TMP", sockDir)
	t.Setenv("TEMP", sockDir)

	services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests <- request.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer server.Close()
	host := subprocess.NewHost(t.TempDir())

	registrations := make(chan struct {
		name   string
		config extension.ProviderConfig
	}, 3)
	host.SetProviderCallbacks(func(name string, config extension.ProviderConfig) {
		if name == "node-collision" {
			config.BaseURL = server.URL
		}
		services.Registry().RegisterProvider(name, config)
		registrations <- struct {
			name   string
			config extension.ProviderConfig
		}{name: name, config: config}
	}, services.Registry().UnregisterProvider)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := host.Load(ctx, subprocess.ExtConfig{
		Name:    "node-model-registry-presence",
		Source:  filepath.Join("testdata", "node-model-registry.mjs"),
		Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	defer host.Shutdown("test cleanup")

	got := make(map[string]extension.ProviderConfig, 3)
	for len(got) < 3 {
		select {
		case registration := <-registrations:
			got[registration.name] = registration.config
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if got["openai"].Models != nil {
		t.Fatalf("omitted models decoded as %#v, want nil", got["openai"].Models)
	}
	if got["anthropic"].Models == nil || len(got["anthropic"].Models) != 0 {
		t.Fatalf("explicit empty models decoded as %#v, want non-nil empty", got["anthropic"].Models)
	}
	if model := services.ModelRuntime().GetModel("openai", "gpt-5.4"); model == nil {
		t.Fatal("connected Node omitted models removed generated membership")
	}
	if model := services.ModelRuntime().GetModel("anthropic", "claude-fable-5"); model != nil {
		t.Fatalf("connected Node explicit empty models retained generated membership: %#v", model)
	}
	for iteration := range 512 {
		entry, ok := services.Registry().Resolve("node-collision", "model")
		if !ok {
			t.Fatal("connected Node collision model is absent")
		}
		assertConnectedHeader(t, iteration, entry.Headers, "x-provider-dupe", "second")
		assertConnectedHeader(t, iteration, entry.Headers, "x-model-dupe", "second")
	}
	model := services.ModelRuntime().GetModel("node-collision", "model")
	if model == nil {
		t.Fatal("connected Node collision model lookup returned nil")
	}
	terminal := services.ModelRuntime().Complete(context.Background(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}, ai.StreamOptions{})
	if terminal.StopReason != ai.StopReasonStop {
		t.Fatalf("connected Node terminal = %#v", terminal)
	}
	select {
	case request := <-requests:
		if request.Get("x-provider-dupe") != "second" || request.Get("x-model-dupe") != "second" {
			t.Fatalf("connected Node request headers = %#v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("connected Node provider request was not captured")
	}

	host.Shutdown("test done")
	if model := services.ModelRuntime().GetModel("anthropic", "claude-fable-5"); model == nil {
		t.Fatal("connected Node unregister did not restore generated membership")
	}
	if _, ok := services.Registry().Resolve("node-collision", "model"); ok {
		t.Fatal("connected Node shutdown retained collision provider")
	}
}

func assertConnectedHeader(t *testing.T, iteration int, headers map[string]string, wantName, wantValue string) {
	t.Helper()
	matches := 0
	for name, value := range headers {
		if !strings.EqualFold(name, wantName) {
			continue
		}
		matches++
		if name != wantName || value != wantValue {
			t.Fatalf("iteration %d header = %q:%q, want %s:%s", iteration, name, value, wantName, wantValue)
		}
	}
	if matches != 1 {
		t.Fatalf("iteration %d header matches = %d, want 1: %#v", iteration, matches, headers)
	}
}
