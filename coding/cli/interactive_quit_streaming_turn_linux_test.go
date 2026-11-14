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
	"testing"
)

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

// qsEventRecorder is a Node extension that appends the events of a turn's end and of the quit to the file @EVENTS@, one line each.
const qsEventRecorder = `import { appendFileSync } from "node:fs";
export default function (pi) {
  const log = (line) => appendFileSync("@EVENTS@", line + "\n");
  pi.on("message_end", (event) => { if (event.message?.role === "assistant") log("message_end " + event.message.stopReason); });
  for (const name of ["turn_end", "agent_end"]) pi.on(name, () => log(name));
  pi.on("session_shutdown", async (event) => {
    await new Promise((resolve) => setTimeout(resolve, 200));
    log("session_shutdown " + event.reason);
  });
}
`

// qsHoldingListener serves an openai-completions stream that shows "alpha beta " and then stays open until the client closes it; closed is closed when it does.
func qsHoldingListener(t *testing.T) (url string, closed <-chan struct{}) {
	t.Helper()
	done := make(chan struct{})
	listener := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, delta := range []string{`{"role":"assistant","content":""}`, `{"content":"alpha "}`, `{"content":"beta "}`} {
			_, _ = fmt.Fprintf(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"hold-model\",\"choices\":[{\"index\":0,\"delta\":%s,\"finish_reason\":null}]}\n\n", delta)
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
	return listener.URL + "/v1", done
}

// TestInteractiveQuitWhileABuiltInProviderTurnStreams: the user quits (ctrl+c twice, ctrl+d or /quit) while a turn of a built-in provider kind (openai-completions through
// models.json, served by a listener that shows "alpha beta " and holds its stream open) streams. No Escape precedes the quit: the quit's own dispose aborts the turn. Pi
// 1.5.2, run under tmux against the same listener, exits 0 within 0.6 s for each key; its session file ends with the assistant message "alpha beta ", stopReason aborted,
// errorMessage "Request was aborted"; an extension sees message_end, turn_end, agent_end and then session_shutdown; the provider's connection is closed and the resume hint
// is printed. pig 0.7.0 ended the process without settling the turn: the session file held no assistant message of it.
func TestInteractiveQuitWhileABuiltInProviderTurnStreams(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not on PATH:", err)
	}
	binary := buildPigBinaryForSignalTest(t)
	for _, key := range []string{"ctrl-c-twice", "ctrl-d", "quit"} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			baseURL, closed := qsHoldingListener(t)
			root, _, project, sessions, env := qsScratch(t, baseURL)
			events := filepath.Join(root, "events")
			recorder := filepath.Join(root, "recorder.mjs")
			if err := os.WriteFile(recorder, []byte(strings.ReplaceAll(qsEventRecorder, "@EVENTS@", events)), 0o600); err != nil {
				t.Fatal(err)
			}
			argv := []string{binary, "--no-extensions", "-e", recorder, "--model", "localhold/hold-model", "--session-dir", sessions}
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
			want := `{"errorMessage":"Request was aborted","stopReason":"aborted","text":"alpha beta "}`
			if got := qsAssistantMessages(t, sessions); strings.Join(got, "\n") != want {
				t.Errorf("the session files hold the assistant messages %q, want %q", got, want)
			}
			seen, _ := os.ReadFile(events)
			if want := "message_end aborted\nturn_end\nagent_end\nsession_shutdown quit\n"; string(seen) != want {
				t.Errorf("the extension saw %q, want %q", seen, want)
			}
			if screen := p.screen(); !strings.Contains(screen, "To resume this session") {
				t.Errorf("pig did not print the resume hint of a completed dispose:\n%s", screen)
			}
		})
	}
}
