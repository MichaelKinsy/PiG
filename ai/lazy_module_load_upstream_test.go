package ai_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Equivalence port of packages/ai/test/lazy-module-load.test.ts (Pi 1.0.0). Pi counts the provider SDK
// specifiers Node's resolver sees, because importing an SDK is what costs startup time and initializes a client.
// Go links every provider at build time, so the observable cost of "loading a provider" is its runtime effects:
// starting goroutines, opening sockets, and reading credentials. The invariant is the same as Pi's: importing ai,
// reading the catalog and building every built-in provider has none of those effects; streaming one model has them
// only for that model's provider.

const lazyProbeEnv = "PIG_LAZY_MODULE_LOAD_PROBE"

// lazyProbeReport is what a fresh process reports after one action.
type lazyProbeReport struct {
	PiGGoroutines []string `json:"pigGoroutines"`
	InitSockets   int      `json:"initSockets"`
	Sockets       int      `json:"sockets"`
	ProxyDials    int64    `json:"proxyDials"`
	Credentials   []string `json:"credentials"`
	ModelsStore   []string `json:"modelsStore"`
	Models        int      `json:"models"`
}

// countingCredentialStore records each provider whose credentials are read.
type countingCredentialStore struct {
	*ai.InMemoryCredentialStore
	mu    sync.Mutex
	reads []string
}

func (s *countingCredentialStore) Read(ctx context.Context, providerID string) (*ai.Credential, error) {
	s.mu.Lock()
	s.reads = append(s.reads, providerID)
	s.mu.Unlock()
	return s.InMemoryCredentialStore.Read(ctx, providerID)
}

func (s *countingCredentialStore) List(ctx context.Context) ([]ai.CredentialInfo, error) {
	s.mu.Lock()
	s.reads = append(s.reads, "<list>")
	s.mu.Unlock()
	return s.InMemoryCredentialStore.List(ctx)
}

func (s *countingCredentialStore) read() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.reads)
}

type countingModelsStore struct {
	*ai.InMemoryModelsStore
	mu    sync.Mutex
	reads []string
}

func (s *countingModelsStore) Read(ctx context.Context, providerID string) (*ai.ModelsStoreEntry, error) {
	s.mu.Lock()
	s.reads = append(s.reads, providerID)
	s.mu.Unlock()
	return s.InMemoryModelsStore.Read(ctx, providerID)
}

// pigGoroutines lists the goroutines that have a frame in a PiG package, other than the probe's own.
func pigGoroutines() []string {
	buffer := make([]byte, 1<<20)
	buffer = buffer[:runtime.Stack(buffer, true)]
	var found []string
	for stack := range strings.SplitSeq(string(buffer), "\n\n") {
		if strings.Contains(stack, "TestLazyModuleLoad") || strings.Contains(stack, "testing.tRunner") || strings.Contains(stack, "testing.(*M)") {
			continue
		}
		if strings.Contains(stack, "github.com/MichaelKinsy/PiG/") {
			found = append(found, stack)
		}
	}
	return found
}

// openSockets counts the process's socket descriptors (Linux /proc), or -1 elsewhere.
func openSockets() int {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return -1
	}
	count := 0
	for _, entry := range entries {
		if target, err := os.Readlink("/proc/self/fd/" + entry.Name()); err == nil && strings.HasPrefix(target, "socket:") {
			count++
		}
	}
	return count
}

// TestLazyModuleLoadProbe runs one action in a fresh process. It does nothing unless the parent test started it.
func TestLazyModuleLoadProbe(t *testing.T) {
	action := os.Getenv(lazyProbeEnv)
	if action == "" {
		t.Skip("probe child only")
	}
	initSockets := openSockets()
	// Every proxy variable points at a listener that counts connections, so any HTTP request the action makes is
	// observed even when it never reaches the provider.
	var dials atomic.Int64
	proxy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(name, "http://"+proxy.Addr().String())
	}
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	go func() {
		for {
			conn, err := proxy.Accept()
			if err != nil {
				return
			}
			dials.Add(1)
			_ = conn.Close()
		}
	}()
	baselineSockets := openSockets()
	credentials := &countingCredentialStore{InMemoryCredentialStore: ai.NewInMemoryCredentialStore()}
	modelsStore := &countingModelsStore{InMemoryModelsStore: ai.NewInMemoryModelsStore()}
	report := lazyProbeReport{InitSockets: initSockets}
	switch action {
	case "import":
	case "build":
		models := ai.BuiltinModels(ai.CreateModelsOptions{Credentials: credentials, ModelsStore: modelsStore})
		report.Models = len(models.GetModels())
		_ = ai.BuiltinProviders()
	case "compat":
		for _, provider := range ai.ListProviders() {
			report.Models += len(ai.ListModels(provider))
		}
		if _, ok := ai.LookupModelExact("anthropic/claude-sonnet-4-6"); !ok {
			t.Fatal("catalog lacks anthropic/claude-sonnet-4-6")
		}
		for _, api := range ai.RegisteredAPIs() {
			if _, ok := ai.LookupBuiltInProvider(api); !ok {
				t.Fatalf("no factory for %s", api)
			}
		}
	default:
		t.Fatalf("unknown action %q", action)
	}
	report.PiGGoroutines = pigGoroutines()
	report.Sockets = openSockets()
	if report.Sockets >= 0 {
		report.Sockets -= baselineSockets
	}
	report.ProxyDials = dials.Load()
	report.Credentials = credentials.read()
	modelsStore.mu.Lock()
	report.ModelsStore = slices.Clone(modelsStore.reads)
	modelsStore.mu.Unlock()
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("LAZY-PROBE %s\n", encoded)
}

func runLazyProbe(t *testing.T, action string) lazyProbeReport {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestLazyModuleLoadProbe$", "-test.count=1")
	cmd.Env = append(os.Environ(), lazyProbeEnv+"="+action)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("probe %s: %v\n%s", action, err, output)
	}
	for line := range strings.SplitSeq(string(output), "\n") {
		if encoded, ok := strings.CutPrefix(line, "LAZY-PROBE "); ok {
			var report lazyProbeReport
			if err := json.Unmarshal([]byte(encoded), &report); err != nil {
				t.Fatal(err)
			}
			return report
		}
	}
	t.Fatalf("probe %s printed no report:\n%s", action, output)
	return lazyProbeReport{}
}

func wantNoProviderEffects(t *testing.T, report lazyProbeReport) {
	t.Helper()
	if len(report.PiGGoroutines) != 0 {
		t.Errorf("PiG goroutines started:\n%s", strings.Join(report.PiGGoroutines, "\n\n"))
	}
	if report.InitSockets > 0 {
		t.Errorf("%d sockets open after package initialization", report.InitSockets)
	}
	if report.Sockets > 0 {
		t.Errorf("%d sockets opened", report.Sockets)
	}
	if report.ProxyDials != 0 {
		t.Errorf("%d connections reached the proxy", report.ProxyDials)
	}
	if len(report.Credentials) != 0 {
		t.Errorf("credentials read for %v", report.Credentials)
	}
	if len(report.ModelsStore) != 0 {
		t.Errorf("models store read for %v", report.ModelsStore)
	}
}

func TestLazyModuleLoadUpstream(t *testing.T) {
	cases := upstreamCaseSites(t, "packages/ai/test/lazy-module-load.test.ts")
	if len(cases) != 5 {
		t.Fatalf("upstream denominator has %d cases, want the 5 this port maps", len(cases))
	}
	byLine := map[int]func(t *testing.T){
		// does not load provider SDKs when importing the root barrel: package initialization of ai (and of every
		// package this test binary links) leaves no goroutine, socket or credential read behind.
		60: func(t *testing.T) { wantNoProviderEffects(t, runLazyProbe(t, "import")) },
		// does not load provider SDKs when building all builtin providers: builtinModels().getModels() is
		// BuiltinModels(...).GetModels() plus BuiltinProviders(); the credential and models stores are never read.
		65: func(t *testing.T) {
			report := runLazyProbe(t, "build")
			if report.Models == 0 {
				t.Fatal("built-in collection exposes no models")
			}
			wantNoProviderEffects(t, report)
		},
		// does not load provider SDKs when importing the compat entrypoint: the global catalog and API registry that
		// compat.ts re-exports (ListProviders/ListModels/LookupModelExact/RegisteredAPIs/LookupBuiltInProvider).
		74: func(t *testing.T) {
			report := runLazyProbe(t, "compat")
			if report.Models == 0 {
				t.Fatal("global catalog exposes no models")
			}
			wantNoProviderEffects(t, report)
		},
		// loads only the Anthropic SDK when streaming through the lazy API wrapper: anthropicMessagesApi().streamSimple
		// is the direct ai.StreamSimple, which connects once, to the Anthropic endpoint only.
		81: func(t *testing.T) {
			server, requests := anthropicLazyServer(t)
			model := lazyAnthropicModel(t, ai.BuiltinModels(), server.URL)
			stream, err := ai.StreamSimple(t.Context(), model, ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}}), ai.StreamOptions{APIKey: "test-key"})
			if err != nil {
				t.Fatal(err)
			}
			wantAnthropicOnly(t, stream.Result(), requests())
		},
		// loads only the Anthropic SDK when dispatching through streamSimple: the collection dispatch reads only the
		// Anthropic credential and connects once, to the Anthropic endpoint only.
		103: func(t *testing.T) {
			server, requests := anthropicLazyServer(t)
			credentials := &countingCredentialStore{InMemoryCredentialStore: ai.NewInMemoryCredentialStore()}
			if _, err := credentials.Modify(t.Context(), "anthropic", func(*ai.Credential) (*ai.Credential, error) {
				return &ai.Credential{Type: ai.CredentialAPIKey, Key: "test-key"}, nil
			}); err != nil {
				t.Fatal(err)
			}
			models := ai.BuiltinModels(ai.CreateModelsOptions{Credentials: credentials})
			model := lazyAnthropicModel(t, models, server.URL)
			if before := credentials.read(); len(before) != 0 {
				t.Fatalf("credentials read before dispatch: %v", before)
			}
			message := models.CompleteSimple(t.Context(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}})
			wantAnthropicOnly(t, message, requests())
			for _, provider := range credentials.read() {
				if provider != "anthropic" {
					t.Fatalf("credentials read for %q during an Anthropic dispatch (all reads %v)", provider, credentials.read())
				}
			}
		},
	}
	for _, tc := range cases {
		run, ok := byLine[tc.Line]
		if !ok {
			t.Fatalf("unmapped upstream case %s at line %d", tc.ID, tc.Line)
		}
		t.Run(tc.ID, func(t *testing.T) {
			t.Logf(".upstream/current/packages/ai/test/lazy-module-load.test.ts:%d", tc.Line)
			run(t)
		})
	}
}

const lazyAnthropicSSE = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_lazy\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n" +
	"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n" +
	"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
	"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
	"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

// anthropicLazyServer serves one Anthropic stream and records each request path.
func anthropicLazyServer(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(lazyAnthropicSSE))
	}))
	t.Cleanup(server.Close)
	return server, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(paths)
	}
}

func lazyAnthropicModel(t *testing.T, models *ai.Models, baseURL string) *ai.Model {
	t.Helper()
	catalog := models.GetModel("anthropic", "claude-sonnet-4-6")
	if catalog == nil {
		t.Fatal("catalog lacks anthropic/claude-sonnet-4-6")
	}
	model := *catalog
	model.ProviderMeta.BaseURL = baseURL
	return &model
}

func wantAnthropicOnly(t *testing.T, message *ai.AssistantMessage, paths []string) {
	t.Helper()
	if message == nil || message.StopReason == ai.StopReasonError {
		t.Fatalf("stream failed: %+v", message)
	}
	if message.API != ai.APIAnthropicMessages || message.Provider != "anthropic" {
		t.Fatalf("message from %s/%s, want anthropic-messages/anthropic", message.API, message.Provider)
	}
	if !slices.Equal(paths, []string{"POST /v1/messages"}) {
		t.Fatalf("requests = %v, want exactly one Anthropic messages request", paths)
	}
}
