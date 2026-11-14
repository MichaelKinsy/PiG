//go:build linux

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// text sends literal text to the pane.
func (p *qsPane) text(literal string) { p.must("send-keys", "-t", "pig", "-l", literal) }

// qsAssistantMessages is the text, stopReason and errorMessage of each assistant message in the session files under sessions.
func qsAssistantMessages(t *testing.T, sessions string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(sessions, "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	persisted := []string{}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
			var entry struct {
				Type    string         `json:"type"`
				Message map[string]any `json:"message"`
			}
			if json.Unmarshal([]byte(line), &entry) != nil || entry.Type != "message" || entry.Message["role"] != "assistant" {
				continue
			}
			var text strings.Builder
			blocks, _ := entry.Message["content"].([]any)
			for _, block := range blocks {
				if b, ok := block.(map[string]any); ok {
					value, _ := b["text"].(string)
					text.WriteString(value)
				}
			}
			stopReason, _ := entry.Message["stopReason"].(string)
			errorMessage, _ := entry.Message["errorMessage"].(string)
			state, _ := json.Marshal(map[string]string{"text": text.String(), "stopReason": stopReason, "errorMessage": errorMessage})
			persisted = append(persisted, string(state))
		}
	}
	return persisted
}

// qsEventRecorder is a Node extension that appends the events of a turn's end and of the quit to the file @EVENTS@, one line each. With @SETTLED@ set to "send" its agent_settled handler also asks for a
// new turn, once, with sendUserMessage: the deferred start of a settled action (a message sent while the settled handlers run starts its run only after they all returned).
const qsEventRecorder = `import { appendFileSync } from "node:fs";
export default function (pi) {
  const log = (line) => appendFileSync("@EVENTS@", line + "\n");
  let sent = false;
  pi.on("message_end", (event) => { if (event.message?.role === "assistant") log("message_end " + event.message.stopReason); });
  for (const name of ["turn_end", "agent_end"]) pi.on(name, () => log(name));
  if ("@SETTLED@" === "send") {
    pi.on("agent_settled", () => {
      log("agent_settled");
      if (!sent) { sent = true; pi.sendUserMessage("DEFERRED again"); }
    });
  }
  pi.on("session_shutdown", async (event) => {
    await new Promise((resolve) => setTimeout(resolve, 200));
    log("session_shutdown " + event.reason);
  });
}
`

// qsShape is one provider API shape of the quit table: the models.json api, the base path under the listener, the frames its stream shows ("alpha " and "beta ") before the listener holds
// it open, and the errorMessage of the assistant message the abort leaves. The frames are the wire form the kind's own decoder reads; the errorMessage is the adapter's own text for an abort
// that ends the stream: Pi's openai-completions reads "Request was aborted" (openai-completions.ts:676-680), anthropic-messages the SDK's "This operation was aborted" (ai/anthropic.go:1210),
// and openai-responses processResponsesStream's "stream ended before a terminal response event", which throws before the outer abort check (openai-responses-shared.ts:815-816, openai-responses.ts:239).
type qsShape struct {
	name         string
	api          string
	basePath     string
	frames       []string
	abortMessage string
}

var qsShapes = []qsShape{
	{
		name: "openai-completions", api: "openai-completions", basePath: "/v1", abortMessage: "Request was aborted",
		frames: []string{
			`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"hold-model","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}` + "\n\n",
			`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"hold-model","choices":[{"index":0,"delta":{"content":"alpha "},"finish_reason":null}]}` + "\n\n",
			`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"hold-model","choices":[{"index":0,"delta":{"content":"beta "},"finish_reason":null}]}` + "\n\n",
		},
	},
	{
		name: "anthropic-messages", api: "anthropic-messages", basePath: "", abortMessage: "This operation was aborted",
		frames: []string{
			"event: message_start\n" + `data: {"type":"message_start","message":{"id":"msg_hold","usage":{"input_tokens":25,"output_tokens":0}}}` + "\n\n",
			"event: content_block_start\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n",
			"event: content_block_delta\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"alpha "}}` + "\n\n",
			"event: content_block_delta\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"beta "}}` + "\n\n",
		},
	},
	{
		name: "openai-responses", api: "openai-responses", basePath: "/v1", abortMessage: "OpenAI Responses stream ended before a terminal response event",
		frames: []string{
			`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_hold","role":"assistant","content":[]}}` + "\n\n",
			`data: {"type":"response.content_part.added","output_index":0,"part":{"type":"output_text","text":""}}` + "\n\n",
			`data: {"type":"response.output_text.delta","output_index":0,"delta":"alpha "}` + "\n\n",
			`data: {"type":"response.output_text.delta","output_index":0,"delta":"beta "}` + "\n\n",
		},
	},
}

// qsHoldingListener serves the shape's stream to every request: the shape's frames, then the stream stays open until the client closes it. closed is closed when the first request's
// connection ends; requests counts the requests the listener has received.
func qsHoldingListener(t *testing.T, shape qsShape) (url string, closed <-chan struct{}, requests *atomic.Int32) {
	t.Helper()
	done := make(chan struct{})
	requests = new(atomic.Int32)
	listener := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range shape.frames {
			_, _ = io.WriteString(w, frame)
			w.(http.Flusher).Flush()
		}
		<-r.Context().Done()
		select {
		case <-done:
		default:
			close(done)
		}
	}))
	t.Cleanup(listener.Close)
	return listener.URL + shape.basePath, done, requests
}

// qsQuitKeys are the forms of the user's quit the table covers.
var qsQuitKeys = []string{"ctrl-c-twice", "ctrl-d", "quit"}

// qsRunQuitWhileStreaming starts pig in a pane against the shape's holding listener, submits a prompt, waits until its partial text shows, and quits with key. settled "send" adds an
// agent_settled handler that asks for a new turn. It returns the listener's request count, the events the extension saw, and the session directory once pig has exited with status 0, its
// children are gone and the provider's connection is closed.
func qsRunQuitWhileStreaming(t *testing.T, key string, shape qsShape, settled string) (requests int32, events string, sessions string) {
	t.Helper()
	baseURL, closed, count := qsHoldingListener(t, shape)
	root, _, project, sessions, env := qsScratch(t, baseURL, shape.api)
	eventsFile := filepath.Join(root, "events")
	recorder := filepath.Join(root, "recorder.mjs")
	source := strings.NewReplacer("@EVENTS@", eventsFile, "@SETTLED@", settled).Replace(qsEventRecorder)
	if err := os.WriteFile(recorder, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	argv := []string{buildPigBinaryForSignalTest(t), "--no-extensions", "-e", recorder, "--model", "localhold/hold-model", "--session-dir", sessions}
	p := startQSPane(t, project, filepath.Join(root, "stderr.log"), env, argv...)
	p.await("the model in the footer", func() bool { return strings.Contains(p.screen(), "hold-model") })
	p.text("HOLD the stream")
	p.await("the typed prompt", func() bool { return strings.Contains(p.screen(), "HOLD the stream") })
	p.keys("Enter")
	p.await("the partial text", func() bool { return strings.Contains(p.screen(), "alpha beta") })
	children := qsChildren(p.pigPID())
	if len(children) == 0 {
		t.Fatalf("pig %d has no extension process", p.pig)
	}
	switch key {
	case "ctrl-c-twice":
		p.keys("C-c", "C-c")
	case "ctrl-d":
		p.keys("C-d")
	case "quit":
		p.text("/quit")
		p.await("the typed /quit", func() bool { return strings.Contains(p.screen(), "/quit") })
		p.keys("Enter")
	}
	status := 0
	p.await("pig's exit after "+key+" while the turn streams", func() bool {
		code, exited := p.status()
		status = code
		return exited
	})
	if status != 0 {
		t.Errorf("pig exited with status %d after %s, want 0", status, key)
	}
	qsAwaitGone(t, children, "child of pig")
	p.await("the end of the provider's connection", func() bool {
		select {
		case <-closed:
			return true
		default:
			return false
		}
	})
	if screen := p.screen(); !strings.Contains(screen, "To resume this session") {
		t.Errorf("pig did not print the resume hint of a completed dispose:\n%s", screen)
	}
	seen, _ := os.ReadFile(eventsFile)
	return count.Load(), string(seen), sessions
}

// abortedTurn is the one assistant message a session file holds after the quit: the text the stream showed, aborted, with the shape's abort text.
func (s qsShape) abortedTurn() string {
	return fmt.Sprintf(`{"errorMessage":%q,"stopReason":"aborted","text":"alpha beta "}`, s.abortMessage)
}

// TestInteractiveQuitWhileABuiltInProviderTurnStreams: the user quits (ctrl+c twice, ctrl+d or /quit) while a turn of a built-in provider kind (openai-completions, anthropic-messages
// or openai-responses through models.json, served by a listener that shows "alpha beta " and holds its stream open) streams. No Escape precedes the quit: the quit's own dispose
// aborts the turn. Pi 1.5.2, run under tmux against the same listener (openai-completions), exits 0 within 0.6 s for each key; its session file ends with the assistant message
// "alpha beta ", stopReason aborted, errorMessage "Request was aborted"; an extension sees message_end, turn_end, agent_end and then session_shutdown; the provider's connection is
// closed and the resume hint is printed. pig 0.7.0 ended the process without settling the turn: the session file held no assistant message of it.
func TestInteractiveQuitWhileABuiltInProviderTurnStreams(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not on PATH:", err)
	}
	for _, shape := range qsShapes {
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()
			for _, key := range qsQuitKeys {
				t.Run(key, func(t *testing.T) {
					t.Parallel()
					_, events, sessions := qsRunQuitWhileStreaming(t, key, shape, "")
					if got, want := qsAssistantMessages(t, sessions), shape.abortedTurn(); strings.Join(got, "\n") != want {
						t.Errorf("the session files hold the assistant messages %q, want %q", got, want)
					}
					if want := "message_end aborted\nturn_end\nagent_end\nsession_shutdown quit\n"; events != want {
						t.Errorf("the extension saw %q, want %q", events, want)
					}
				})
			}
		})
	}
}

// TestInteractiveQuitSkipsTheDeferredStartOfASettledAction: an extension's agent_settled handler sends a user message while the user's quit aborts the streaming turn. The message
// is a deferred settled action: it starts a run only after the settled handlers returned. Pi's quit does not wait for the interrupted run (1.5.0 agent-session-runtime.ts dispose,
// interactive-mode.ts shutdown ends with process.exit(0)), so no run of that message ever reaches the provider and the session file never holds it; pig's quit, which settles the
// interrupted turn first (D247), starts no new turn either: the listener sees the one request, pig exits 0 promptly, and the session holds the aborted turn and nothing of the message.
func TestInteractiveQuitSkipsTheDeferredStartOfASettledAction(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not on PATH:", err)
	}
	for _, shape := range qsShapes {
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()
			for _, key := range qsQuitKeys {
				t.Run(key, func(t *testing.T) {
					t.Parallel()
					requests, events, sessions := qsRunQuitWhileStreaming(t, key, shape, "send")
					if requests != 1 {
						t.Errorf("the provider received %d requests, want 1: the message of the deferred settled action started a turn during the quit", requests)
					}
					if got, want := qsAssistantMessages(t, sessions), shape.abortedTurn(); strings.Join(got, "\n") != want {
						t.Errorf("the session files hold the assistant messages %q, want %q", got, want)
					}
					files, err := filepath.Glob(filepath.Join(sessions, "*.jsonl"))
					if err != nil {
						t.Fatal(err)
					}
					for _, file := range files {
						if data, err := os.ReadFile(file); err != nil {
							t.Fatal(err)
						} else if strings.Contains(string(data), "DEFERRED") {
							t.Errorf("the session file %s holds the deferred message:\n%s", file, data)
						}
					}
					if want := "message_end aborted\nturn_end\nagent_end\nagent_settled\nsession_shutdown quit\n"; events != want {
						t.Errorf("the extension saw %q, want %q", events, want)
					}
				})
			}
		})
	}
}
