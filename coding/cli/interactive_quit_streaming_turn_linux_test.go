//go:build linux

package cli

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// The quit of the interactive mode while a turn of a built-in provider API streams, for each of the provider API shapes a models.json provider can name: the dispose settles the
// interrupted turn, the extensions see its end before session_shutdown, and a message that an agent_settled handler asks for during the quit starts its turn, which the quit waits for (the pinned Pi
// does the same: scenario 115-quit-settled-send-user-message-*). Pi's quit stops the TUI and then awaits runtimeHost.dispose() (interactive-mode.ts shutdown, agent-session-runtime.ts).
// TestInteractiveQuitWhileABuiltInProviderTurnStreams (interactive_provider_abort_quit_linux_test.go) holds the openai-completions row with the terminal's bytes.

// quitShapeRecorder is a Node extension that appends the events of a turn's end and of the quit to the file @EVENTS@, one line each. With @SETTLED@ set to "send" its agent_settled
// handler also asks for a new turn, once, with sendUserMessage: the deferred start of a settled action (a message sent while the settled handlers run starts its run only after they
// all returned).
const quitShapeRecorder = `import { appendFileSync } from "node:fs";
export default function (pi) {
  const log = (line) => appendFileSync("@EVENTS@", line + "\n");
  let sent = false;
  pi.on("message_end", (event) => { if (event.message?.role === "assistant") log("message_end " + event.message.stopReason); });
  for (const name of ["turn_end", "agent_end"]) pi.on(name, () => log(name));
  pi.on("agent_settled", () => {
    log("agent_settled");
    if ("@SETTLED@" === "send" && !sent) { sent = true; pi.sendUserMessage("DEFERRED again"); }
  });
  pi.on("session_shutdown", async (event) => {
    await new Promise((resolve) => setTimeout(resolve, 200));
    log("session_shutdown " + event.reason);
  });
}
`

// quitShape is one provider API shape of the quit table: the models.json api, the base path under the listener, the frames its stream shows ("alpha " and "beta ") before the listener
// holds it open, and the errorMessage of the assistant message the abort leaves. The frames are the wire form the kind's own decoder reads; the errorMessage is the adapter's own text for
// an abort that ends the stream: Pi's openai-completions reads "Request was aborted" (openai-completions.ts:676-680), anthropic-messages the SDK's "This operation was aborted"
// (ai/anthropic.go:1210), and openai-responses processResponsesStream's "stream ended before a terminal response event", which throws before the outer abort check
// (openai-responses-shared.ts:815-816, openai-responses.ts:239).
type quitShape struct {
	name         string
	api          string
	basePath     string
	frames       []string
	finish       []string // the frames that end a stream after frames: the answer of every request after the first
	abortMessage string
}

var quitShapes = []quitShape{
	{
		name: "openai-completions", api: "openai-completions", basePath: "/v1", abortMessage: "Request was aborted",
		frames: []string{
			`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"hold-model","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}` + "\n\n",
			`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"hold-model","choices":[{"index":0,"delta":{"content":"alpha "},"finish_reason":null}]}` + "\n\n",
			`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"hold-model","choices":[{"index":0,"delta":{"content":"beta "},"finish_reason":null}]}` + "\n\n",
		},
		finish: []string{
			`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"hold-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n",
			"data: [DONE]\n\n",
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
		finish: []string{
			"event: content_block_stop\n" + `data: {"type":"content_block_stop","index":0}` + "\n\n",
			"event: message_delta\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}` + "\n\n",
			"event: message_stop\n" + `data: {"type":"message_stop"}` + "\n\n",
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
		finish: []string{
			`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_hold","role":"assistant","status":"completed","content":[{"type":"output_text","text":"alpha beta "}]}}` + "\n\n",
			`data: {"type":"response.completed","response":{"id":"resp_hold","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":2}}}` + "\n\n",
		},
	},
}

// quitHoldingListener serves the shape's stream to the first request: the shape's frames, then the stream stays open until the client closes it. Every later request gets the frames and
// the shape's finish frames, a stream that ends. closed is closed when the first request's connection ends; requests counts the requests the listener has received.
func quitHoldingListener(t *testing.T, shape quitShape) (url string, closed <-chan struct{}, requests *atomic.Int32) {
	t.Helper()
	done := make(chan struct{})
	requests = new(atomic.Int32)
	listener := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range shape.frames {
			_, _ = io.WriteString(w, frame)
			w.(http.Flusher).Flush()
		}
		if n > 1 {
			for _, frame := range shape.finish {
				_, _ = io.WriteString(w, frame)
			}
			w.(http.Flusher).Flush()
			return
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

// quitScratch writes the agent directory of a pig that serves the model localhold/hold-model of the provider API api from baseURL, and returns the directories and the environment of the run.
func quitScratch(t *testing.T, baseURL, api string) (root, project, sessions string, env []string) {
	t.Helper()
	root = t.TempDir()
	agent := filepath.Join(root, "agent")
	project, sessions = filepath.Join(root, "project"), filepath.Join(root, "sessions")
	for _, dir := range []string{agent, project} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	seedFirstRunDone(t, agent)
	models := fmt.Sprintf(`{"providers":{"localhold":{"baseUrl":%q,"api":%q,"apiKey":"dummy","models":[{"id":"hold-model"}]}}}`, baseURL, api)
	if err := os.WriteFile(filepath.Join(agent, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	env = []string{"HOME=" + root, "PIG_HOME=" + root, "PI_HOME=" + root, "PIG_CODING_AGENT_DIR=" + agent, "PI_CODING_AGENT_DIR=" + agent, "PIG_OFFLINE=1", "PI_OFFLINE=1"}
	return root, project, sessions, env
}

// quitAwaitGone fails the test for each pid still alive after the test budget.
func quitAwaitGone(t *testing.T, pids []int, what string) {
	t.Helper()
	budget := testbudget.Wait(t)
	deadline := time.Now().Add(budget)
	for _, pid := range pids {
		for !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			if time.Now().After(deadline) {
				t.Errorf("process %d (%s) is alive %s after pig exited", pid, what, budget)
				_ = syscall.Kill(pid, syscall.SIGKILL)
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// quitKeys are the forms of the user's quit the table covers.
var quitKeys = []string{"ctrl-c-twice", "ctrl-d", "quit"}

// quitWhileStreaming starts pig in a pane against the shape's holding listener, submits a prompt, waits until its partial text shows, and quits with key. settled "send" adds an
// agent_settled handler that asks for a new turn. It returns the listener's request count, the events the extension saw, and the session directory once pig has exited with status 0, its
// children are gone and the provider's connection is closed.
func quitWhileStreaming(t *testing.T, key string, shape quitShape, settled string) (requests int32, events string, sessions string) {
	t.Helper()
	baseURL, closed, count := quitHoldingListener(t, shape)
	root, project, sessions, env := quitScratch(t, baseURL, shape.api)
	eventsFile := filepath.Join(root, "events")
	recorder := filepath.Join(root, "recorder.mjs")
	source := strings.NewReplacer("@EVENTS@", eventsFile, "@SETTLED@", settled).Replace(quitShapeRecorder)
	if err := os.WriteFile(recorder, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	argv := []string{buildPigBinaryForSignalTest(t), "--no-extensions", "-e", recorder, "--model", "localhold/hold-model", "--session-dir", sessions}
	p := startQuitPane(t, project, filepath.Join(root, "stderr.log"), env, argv...)
	p.await("the model in the footer", func() bool { return strings.Contains(p.screen(), "hold-model") })
	p.text("HOLD the stream")
	p.await("the typed prompt", func() bool { return strings.Contains(p.screen(), "HOLD the stream") })
	p.keys("Enter")
	p.await("the partial text", func() bool { return strings.Contains(p.screen(), "alpha beta") })
	children := childProcesses(p.pid())
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
	quitAwaitGone(t, children, "child of pig")
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
func (s quitShape) abortedTurn() string {
	return providerAbortWant("alpha beta ", "aborted", s.abortMessage)
}

// answeredTurn is the assistant message of a request after the first: the same text, ended by the shape's finish frames.
func (s quitShape) answeredTurn() string {
	return providerAbortWant("alpha beta ", "stop", "")
}

// TestInteractiveQuitSettlesTheTurnOfEveryBuiltInProviderAPI: the user quits (ctrl+c twice, ctrl+d or /quit) while a turn of a built-in provider kind (openai-completions,
// anthropic-messages or openai-responses through models.json, served by a listener that shows "alpha beta " and holds its stream open) streams. No Escape precedes the quit: the quit's
// own dispose aborts the turn. Pi 1.5.2, run under tmux against the same listener (openai-completions), exits 0 within 0.6 s for each key; its session file ends with the assistant
// message "alpha beta ", stopReason aborted, errorMessage "Request was aborted"; an extension sees message_end, turn_end, agent_end, agent_settled and then session_shutdown; the
// provider's connection is closed and the resume hint is printed. pig 0.7.0 ended the process without settling the turn: the session file held no assistant message of it.
func TestInteractiveQuitSettlesTheTurnOfEveryBuiltInProviderAPI(t *testing.T) {
	t.Parallel()
	for _, shape := range quitShapes {
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()
			for _, key := range quitKeys {
				t.Run(key, func(t *testing.T) {
					t.Parallel()
					_, events, sessions := quitWhileStreaming(t, key, shape, "")
					if got, want := quitPersisted(t, sessions), []string{shape.abortedTurn()}; strings.Join(got, "\n") != strings.Join(want, "\n") {
						t.Errorf("the session files hold the assistant messages %q, want %q", got, want)
					}
					if want := "message_end aborted\nturn_end\nagent_end\nagent_settled\nsession_shutdown quit\n"; events != want {
						t.Errorf("the extension saw %q, want %q", events, want)
					}
				})
			}
		})
	}
}

// TestInteractiveQuitRunsTheDeferredStartOfASettledAction: an extension's agent_settled handler sends a user message while the user's quit aborts the streaming turn. The message
// is a deferred settled action: it starts a run once the settled handlers returned. The pinned Pi, quit under tmux in this situation (ctrl+d, ctrl+c twice and /quit, 5 runs each),
// runs the turn of that message, emits session_shutdown after its agent_settled, exits 0 and leaves the message and its answer in the session file (scenario
// 115-quit-settled-send-user-message-*). pig's quit settles the interrupted turn first, then waits for the new turn: the listener sees two requests, and the session holds the aborted
// turn, the message and its answer.
func TestInteractiveQuitRunsTheDeferredStartOfASettledAction(t *testing.T) {
	t.Parallel()
	for _, shape := range quitShapes {
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()
			for _, key := range quitKeys {
				t.Run(key, func(t *testing.T) {
					t.Parallel()
					requests, events, sessions := quitWhileStreaming(t, key, shape, "send")
					if requests != 2 {
						t.Errorf("the provider received %d requests, want 2: the message of the deferred settled action must start a turn during the quit", requests)
					}
					if got, want := quitPersisted(t, sessions), []string{shape.abortedTurn(), shape.answeredTurn()}; strings.Join(got, "\n") != strings.Join(want, "\n") {
						t.Errorf("the session files hold the assistant messages %q, want %q", got, want)
					}
					files, err := filepath.Glob(filepath.Join(sessions, "*.jsonl"))
					if err != nil {
						t.Fatal(err)
					}
					held := false
					for _, file := range files {
						if data, err := os.ReadFile(file); err != nil {
							t.Fatal(err)
						} else if strings.Contains(string(data), "DEFERRED again") {
							held = true
						}
					}
					if !held {
						t.Errorf("no session file under %s holds the deferred message", sessions)
					}
					if want := "message_end aborted\nturn_end\nagent_end\nagent_settled\nmessage_end stop\nturn_end\nagent_end\nagent_settled\nsession_shutdown quit\n"; events != want {
						t.Errorf("the extension saw %q, want %q", events, want)
					}
				})
			}
		})
	}
}
