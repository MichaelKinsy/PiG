package ai

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Codex hands resolveCodexUrl's text to fetch (openai-codex-responses.ts:400, 641-646) and Azure's SDK client hands its URL to fetch: undici's Request constructor parses it with the URL parser, so an upper-case scheme, a space or a dot segment reaches the server as the serialized URL, and the fragment is not sent. A URL with credentials is refused with fetch's TypeError, which Codex reports as is and the SDK as a connection error.
func TestCodexAndAzureRequestTargetIsParsedLikeFetch(t *testing.T) {
	var mu sync.Mutex
	var targets []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		mu.Lock()
		targets = append(targets, r.RequestURI)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, observationFinalBody(APIOpenAIResponses))
	}))
	defer server.Close()
	upper := strings.Replace(server.URL, "http://", "HTTP://", 1)
	credentialed := strings.Replace(server.URL, "http://", "http://u:p@", 1)
	for _, test := range []struct {
		api          API
		base, target string
		error        string
	}{
		{APIOpenAICodexResponses, upper + "/a b/./c", "/a%20b/c/codex/responses", ""},
		{APIOpenAICodexResponses, upper + "/x#frag", "/x", ""},
		{APIOpenAICodexResponses, credentialed, "", "Request cannot be constructed from a URL that includes credentials: " + credentialed + "/codex/responses"},
		{APIAzureOpenAIResponses, upper + "/openai/v1", "/openai/v1/responses?api-version=v1", ""},
		{APIAzureOpenAIResponses, credentialed + "/openai/v1", "", "Connection error."},
	} {
		t.Run(string(test.api)+" "+test.base, func(t *testing.T) {
			mu.Lock()
			targets = nil
			mu.Unlock()
			provider := observationProvider(t, test.api, test.base)
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("x")}}}), StreamOptions{Transport: TransportSSE, MaxRetries: new(0)})
			message := ""
			if err != nil {
				message = err.Error()
			} else if result := stream.Result(); result.StopReason == StopReasonError {
				message = result.ErrorMessage
			}
			mu.Lock()
			defer mu.Unlock()
			if message != test.error {
				t.Fatalf("error %q, want %q (requests %q)", message, test.error, targets)
			}
			if want := []string{test.target}; test.target == "" && len(targets) != 0 || test.target != "" && (len(targets) != 1 || targets[0] != want[0]) {
				t.Fatalf("request targets %q, want %q", targets, test.target)
			}
		})
	}
}

// A path net/url cannot parse ("%zz") is sent as written, and fetch's request target has no fragment there either. net/http's server refuses such a target, so a raw listener reads the request line.
func TestCodexRequestTargetDropsTheFragmentOfAnOpaquePath(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	lines := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			lines <- err.Error()
			return
		}
		defer func() { _ = conn.Close() }()
		line, _ := bufio.NewReader(conn).ReadString('\n')
		lines <- strings.TrimRight(line, "\r\n")
		_, _ = io.WriteString(conn, "HTTP/1.1 400 Bad Request\r\nContent-Length: 2\r\nConnection: close\r\n\r\n{}")
	}()
	base := "HTTP://" + listener.Addr().String()
	provider := observationProvider(t, APIOpenAICodexResponses, base+"/%zz#frag")
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("x")}}}), StreamOptions{Transport: TransportSSE, MaxRetries: new(0)})
	if err != nil {
		t.Fatal(err)
	}
	stream.Result()
	if got, want := <-lines, "POST /%zz HTTP/1.1"; got != want {
		t.Fatalf("request line %q, want %q", got, want)
	}
}
