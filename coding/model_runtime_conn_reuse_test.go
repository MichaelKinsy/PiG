package coding

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi reuses connections through one process-wide undici dispatcher; 20 sequential requests through ModelRuntime must use one TCP connection (comb-audit row 6).
func TestModelRuntimeReusesOneConnectionAcrossRequests(t *testing.T) {
	var conns atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"x\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"x\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	server.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	server.Start()
	t.Cleanup(server.Close)
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	model := nativeCompatModel("m", "combprov", server.URL)
	if err := services.ModelRuntime().RegisterProvider("combprov", ProviderConfigInput{API: ai.APIOpenAICompletions, BaseURL: server.URL, APIKey: "k", Models: []ai.AnyModel{model}}); err != nil {
		t.Fatal(err)
	}
	request := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}
	g0 := runtime.NumGoroutine()
	for range 20 {
		m := services.ModelRuntime().StreamSimple(t.Context(), model, request, ai.StreamOptions{}).Result()
		if m.StopReason == ai.StopReasonError {
			t.Fatalf("error: %s", m.ErrorMessage)
		}
	}
	time.Sleep(100 * time.Millisecond)
	if n := conns.Load(); n > 1 {
		t.Fatalf("20 sequential requests -> %d TCP connections (want 1: Pi reuses one undici dispatcher); goroutines %d -> %d", n, g0, runtime.NumGoroutine())
	}
}
