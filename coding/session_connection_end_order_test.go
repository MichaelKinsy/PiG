package coding

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// The session's retry of a connection the server ends without a response, by how it ends it and when: order A after it read the request
// body to its end, order B as soon as the request headers are parsed, while the client still writes a 1 MiB body. The row of the small
// request reset at its headers is TestSessionRetriesAConnectionResetAfterTheRequestHeaders.
//
// Pi's texts are the oracle (ai TestProviderReportsAConnectionTheServerEndsAsPi has the counts): Pi 1.5.2, each API module's stream()
// against a node:http server, both orders, 6 KiB and 1 MiB bodies, quiet and beside 64 busy loops, gave the five SDK shapes
// `Connection error.: read ECONNRESET` 2800 of 2800 for socket.resetAndDestroy() and `Connection error.: other side closed
// (UND_ERR_SOCKET)` 2800 of 2800 for each of socket.end() and socket.destroy().
type sessionConnectionEnd struct {
	name string
	// afterBody is order A.
	afterBody bool
	end       func(*net.TCPConn)
	want      string
}

const (
	sessionConnectionReset  = "Connection error.: read ECONNRESET"
	sessionConnectionClosed = "Connection error.: other side closed (UND_ERR_SOCKET)"
)

func sessionConnectionEnds() []sessionConnectionEnd {
	// socket.resetAndDestroy(): a reset, no FIN.
	reset := func(conn *net.TCPConn) {
		_ = conn.SetLinger(0)
		_ = conn.Close()
	}
	// socket.end(): the server's FIN; it keeps reading until the client closes.
	end := func(conn *net.TCPConn) {
		_ = conn.CloseWrite()
		_, _ = io.Copy(io.Discard, conn)
		_ = conn.Close()
	}
	ends := []sessionConnectionEnd{
		{"reset after the request body", true, reset, sessionConnectionReset},
		{"reset at the request headers", false, reset, sessionConnectionReset},
		{"FIN after the request body", true, end, sessionConnectionClosed},
		{"FIN at the request headers", false, end, sessionConnectionClosed},
		// socket.destroy() once the request is read: close() with nothing unread sends a FIN.
		{"close after the request body", true, func(conn *net.TCPConn) { _ = conn.Close() }, sessionConnectionClosed},
	}
	return append(ends, platformSessionConnectionEnds()...)
}

// endConnection ends the request's connection as end says.
func endConnection(t *testing.T, w http.ResponseWriter, r *http.Request, end sessionConnectionEnd) {
	t.Helper()
	if end.afterBody {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Errorf("request body: %v", err)
			return
		}
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		t.Error("test server does not support hijacking")
		return
	}
	connection, _, err := hijacker.Hijack()
	if err != nil {
		t.Errorf("hijack: %v", err)
		return
	}
	tcp, ok := connection.(*net.TCPConn)
	if !ok {
		t.Errorf("connection is %T, want *net.TCPConn", connection)
		_ = connection.Close()
		return
	}
	end.end(tcp)
}

// One ended connection, one retry announced with Pi's text, the answer arrives.
func TestSessionRetriesAConnectionTheServerEndsInEitherOrder(t *testing.T) {
	prompt := strings.Repeat("x", 1<<20)
	for _, api := range closedConnectionAPIs() {
		for _, end := range sessionConnectionEnds() {
			t.Run(api.name+"/"+end.name, func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if requests.Add(1) == 1 {
						endConnection(t, w, r, end)
						return
					}
					_, _ = io.Copy(io.Discard, r.Body)
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = w.Write([]byte(api.okBody))
				}))
				t.Cleanup(server.Close)
				// The 1 MiB prompt fits the model's window, so the session sends it as it is.
				h := closedConnectionHarnessWith(t, api, server, harnessOptions{settings: retryEventsSettings, contextWindow: 8_000_000})
				log := recordSessionEvents(h.session)
				retryPrompt(t, h, prompt)
				if got := requests.Load(); got != 2 {
					t.Fatalf("server requests = %d, want 2 (the ended request and one retry)", got)
				}
				if got := retrySummary(log); !slices.Equal(got, []string{"start:1", "end:true"}) {
					t.Fatalf("retry events = %v", got)
				}
				if starts := eventsOf[agent.AutoRetryStartEvent](log); len(starts) != 1 || starts[0].ErrorMessage != end.want {
					t.Fatalf("auto_retry_start = %#v, want the error %q", starts, end.want)
				}
				if text := h.session.LastAssistantText(); text == nil || *text != "recovered after retry" {
					t.Fatalf("last assistant text = %v", text)
				}
			})
		}
	}
}

// The texts a reset at the request headers was reported with before the connection kept its own end: the bare SDK message (the vectored
// write's errno was not named), the closed connection's, and the write's two. There is no migration: a turn stored with one of them is
// retried as it was, and is written, loaded and shown with the text it has.
func TestSessionKeepsAnErroredTurnWithAnEarlierConnectionResetText(t *testing.T) {
	for _, text := range []string{"Connection error.", "Connection error.: other side closed (UND_ERR_SOCKET)", "Connection error.: write ECONNRESET", "Connection error.: write EPIPE"} {
		t.Run(text, func(t *testing.T) {
			if !ai.IsRetryableAssistantError(ai.AssistantMessage{StopReason: ai.StopReasonError, ErrorMessage: text}) {
				t.Fatalf("%q is not retryable", text)
			}
			h := newRetryEventsHarness(t, retryEventsSettings, extension.Extension{}, nil, fauxError(text), fauxReply("recovered", ai.StopReasonStop, 0))
			log := recordSessionEvents(h.session)
			retryPrompt(t, h, "hello")
			if got := retrySummary(log); !slices.Equal(got, []string{"start:1", "end:true"}) {
				t.Fatalf("retry events = %v", got)
			}
			if starts := eventsOf[agent.AutoRetryStartEvent](log); len(starts) != 1 || starts[0].ErrorMessage != text {
				t.Fatalf("auto_retry_start = %#v, want the stored text %q", starts, text)
			}

			stored := newRecoveryHarness(t, harnessOptions{settings: `{"retry":{"enabled":false}}`}, fauxError(text))
			retryPrompt(t, stored, "hello")
			messages := stored.session.Messages()
			if last := messages[len(messages)-1].Assistant; last == nil || last.StopReason != ai.StopReasonError || last.ErrorMessage != text {
				t.Fatalf("last message = %#v, want the errored turn with %q", last, text)
			}
			path := stored.session.Path()
			if path == "" {
				t.Fatal("the session has no file")
			}
			entries, err := icodingagent.LoadEntriesFromFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var persisted []string
			for _, entry := range entries {
				var line struct {
					Message struct {
						Role         string `json:"role"`
						StopReason   string `json:"stopReason"`
						ErrorMessage string `json:"errorMessage"`
					} `json:"message"`
				}
				if json.Unmarshal(entry.Raw(), &line) == nil && line.Message.Role == "assistant" {
					persisted = append(persisted, line.Message.StopReason+": "+line.Message.ErrorMessage)
				}
			}
			if !slices.Equal(persisted, []string{"error: " + text}) {
				t.Fatalf("stored assistant turns = %q, want the errored turn with %q", persisted, text)
			}
			if _, err := icodingagent.NewSessionManagerWithDir(stored.session.services.CWD(), filepath.Dir(path)).Open(path); err != nil {
				t.Fatalf("the session file does not load: %v", err)
			}
		})
	}
}
