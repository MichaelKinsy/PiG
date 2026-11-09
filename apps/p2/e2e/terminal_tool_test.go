// Ports test/p2/terminal-tool.test.ts (Pi main c23b9ec1): the agent's terminal tool end to end with the fake provider and a
// real terminal host: the tool drives the session's terminals, which are the owner's (listed, labeled, typed into by both);
// guest turns may use it; tmux errors come back as tool errors; the server serves only the session's current worker, on the
// session's environments, and archiving closes the session's terminals. Without code mode, `tool_search` loads the tool.
//
// Pi's cases drive the tool from code mode scripts (`codemode <code>` prompts) and read the script's returned value; so do
// these. The last case, without code mode, has the model call the tool directly after `tool_search` loaded it.

package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/terminals"
	terminalscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/terminals/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/gorilla/websocket"
)

// terminalToolResults are the tool result messages of a conversation view.
func terminalToolResults(view delta.JsonValue) []*delta.JsonObject {
	var out []*delta.JsonObject
	for _, entry := range list(object(view).Value("entries")) {
		for _, message := range list(object(entry).Value("model")) {
			if object(message).Value("role") == "toolResult" {
				out = append(out, object(message))
			}
		}
	}
	return out
}

func resultText(result *delta.JsonObject) string {
	var text strings.Builder
	for _, part := range list(result.Value("content")) {
		if object(part).Value("type") == "text" {
			text.WriteString(object(part).Value("text").(string))
		}
	}
	return text.String()
}

func TestTheAgentsTerminalToolOnTheSessionsSharedTerminals(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	p2test.UseTerminalDaemon(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{AllowedHosts: []string{"share.test"}})
	t.Cleanup(func() { shutdownTerminalHost(t, fixture.DataDir) })
	workspace, err := realpathOf(fixture.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	owner := fixture.Browser()
	sessions, err := p2test.HoldIn(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", workspace, "title", "Terminals"), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	session := object(created).Value("id").(string)
	chatOf := func(h host.Host, id string, variant string) *host.Reference {
		t.Helper()
		ref, err := p2test.HoldIn(h, chatcontract.Chat, delta.JsonObjectOf("session", id, "conversation", float64(1)), variant)
		if err != nil {
			t.Fatal(err)
		}
		return ref
	}
	chat := chatOf(owner, session, "participant")
	serial := 0
	// call sends a prompt and waits for the tool result it makes.
	call := func(prompt string, target *host.Reference) *delta.JsonObject {
		t.Helper()
		view, err := p2test.Until(target.Member("view"), func(view delta.JsonValue) bool { return view != nil }, 0)
		if err != nil {
			t.Fatal(err)
		}
		count := len(terminalToolResults(view))
		serial++
		if _, err := target.Call("send", delta.JsonObjectOf("text", prompt, "requestId", "terminal-"+string(rune('a'+serial))), p2test.Background); err != nil {
			t.Fatal(err)
		}
		view, err = p2test.Until(target.Member("view"), func(view delta.JsonValue) bool {
			return view != nil && len(terminalToolResults(view)) > count && !running(view)
		}, 60*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		results := terminalToolResults(view)
		return results[len(results)-1]
	}
	tool := func(command string, args ...string) string {
		quoted := make([]string, len(args))
		for index, each := range args {
			quoted[index] = jsonString(each)
		}
		return "terminal " + `{"command":` + jsonString(command) + `,"args":[` + strings.Join(quoted, ",") + `]}`
	}
	// script runs code in a `codemode` turn and decodes the value it returns.
	script := func(code string, target *host.Reference, into any) {
		t.Helper()
		result := call("codemode "+code, target)
		if result.Value("isError") == true {
			t.Fatalf("script: %s", resultText(result))
		}
		decode(t, scriptOutput(t, resultText(result)), into)
	}
	// The owner's terminal panel: its byte channel hears the session's terminals change.
	view, err := p2test.HoldIn(owner, terminalscontract.Terminals, delta.JsonObjectOf("session", session, "view", randomView()), "owner")
	if err != nil {
		t.Fatal(err)
	}
	granted, err := view.Call("channel", delta.NewJsonObject(0), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	bytes := openByteChannel(t, fixture.Server.Port, object(granted).Value("token").(string), fixture.Owner().OwnerHeaders(nil))
	if _, err := view.Call("list", delta.NewJsonObject(0), p2test.Background); err != nil {
		t.Fatal(err)
	}
	listed := func() []terminals.TerminalInfo {
		bytes.mu.Lock()
		defer bytes.mu.Unlock()
		var latest []terminals.TerminalInfo
		for _, event := range bytes.events {
			if event.Type == terminals.EventTerminals || event.Type == terminals.EventList {
				latest = event.Terminals
			}
		}
		return latest
	}

	t.Run("a script opens a window, types into it and reads its output", func(t *testing.T) {
		var value struct {
			Opened struct {
				Window  int    `json:"window"`
				Output  string `json:"output"`
				Running bool   `json:"running"`
			} `json:"opened"`
			Sent struct {
				Window int    `json:"window"`
				Output string `json:"output"`
			} `json:"sent"`
			Windows struct {
				Windows []struct {
					Window   int    `json:"window"`
					Name     string `json:"name"`
					Opener   string `json:"opener"`
					Location string `json:"location"`
				} `json:"windows"`
			} `json:"windows"`
		}
		script(strings.Join([]string{
			`const opened = await tools.terminal({ command: "new-window", args: ["-n", "dev", "-w", "/^hello-agent$/", "echo hello-age''nt"] });`,
			`const sent = await tools.terminal({ command: "send-keys", args: ["-t", "dev", "-w", "/^two$/", "echo t''wo", "Enter"] });`,
			`const windows = await tools.terminal({ command: "list-windows" });`,
			`return { opened, sent, windows };`,
		}, "\n"), chat, &value)
		if value.Opened.Window != 1 || !value.Opened.Running || !regexp.MustCompile(`(?m)^hello-agent$`).MatchString(value.Opened.Output) {
			t.Fatalf("opened = %+v", value.Opened)
		}
		if !regexp.MustCompile(`(?m)^two$`).MatchString(value.Sent.Output) || strings.Contains(value.Sent.Output, "hello-agent") {
			t.Fatalf("sent = %+v", value.Sent)
		}
		if len(value.Windows.Windows) != 1 {
			t.Fatalf("windows = %+v", value.Windows)
		}
		if got := value.Windows.Windows[0]; got.Window != 1 || got.Name != "dev" || got.Opener != "agent" || got.Location != workspace {
			t.Fatalf("window = %+v", got)
		}
		// The owner's panel lists it, marked as the agent's.
		pollUntil(t, func() bool {
			for _, terminal := range listed() {
				if terminal.Name == "dev" && terminal.Agent {
					return true
				}
			}
			return false
		})
	})

	t.Run("the owner types into the agent's window, the agent into the owner's", func(t *testing.T) {
		var dev terminals.TerminalInfo
		for _, terminal := range listed() {
			if terminal.Name == "dev" {
				dev = terminal
			}
		}
		if _, err := view.Call("attach", delta.JsonObjectOf("terminalId", dev.ID), p2test.Background); err != nil {
			t.Fatal(err)
		}
		pollUntil(t, func() bool {
			_, ok := bytes.find(func(event terminals.TerminalEvent) bool { return event.Type == terminals.EventAttached })
			return ok
		})
		_ = bytes.conn.WriteMessage(websocket.BinaryMessage, []byte("echo from-own''er\n"))
		var read struct {
			Output   string `json:"output"`
			TimedOut *bool  `json:"timedOut"`
		}
		script(`return await tools.terminal({ command: "read", args: ["-t", "1", "-w", "/^from-owner$/"] });`, chat, &read)
		if read.TimedOut != nil || !regexp.MustCompile(`(?m)^from-owner$`).MatchString(read.Output) {
			t.Fatalf("read = %+v", read)
		}
		// The owner opens a terminal; the agent sees and drives it.
		if _, err := view.Call("open", delta.JsonObjectOf("cols", float64(80), "rows", float64(10), "conversation", float64(1)), p2test.Background); err != nil {
			t.Fatal(err)
		}
		pollUntil(t, func() bool { return len(listed()) == 2 })
		var value struct {
			Read struct {
				Output   string `json:"output"`
				TimedOut *bool  `json:"timedOut"`
			} `json:"read"`
			Windows []struct {
				Window int    `json:"window"`
				Opener string `json:"opener"`
			} `json:"windows"`
		}
		script(strings.Join([]string{
			`const read = await tools.terminal({ command: "send-keys", args: ["-t", "2", "-w", "/^owned-by-user$/", "-l", "echo own''ed-by-user\n"] });`,
			`const { windows } = await tools.terminal({ command: "list-windows" });`,
			`return { read, windows };`,
		}, "\n"), chat, &value)
		if value.Read.TimedOut != nil || !regexp.MustCompile(`(?m)^owned-by-user$`).MatchString(value.Read.Output) {
			t.Fatalf("read = %+v", value.Read)
		}
		if len(value.Windows) != 2 || value.Windows[0].Window != 1 || value.Windows[0].Opener != "agent" || value.Windows[1].Window != 2 || value.Windows[1].Opener != "user" {
			t.Fatalf("windows = %+v", value.Windows)
		}
	})

	t.Run("tmux errors and a missing window are tool errors", func(t *testing.T) {
		var value []string
		script(strings.Join([]string{
			`const errors = [];`,
			`for (const [command, args] of [["send-keys", ["-x"]], ["send-keys", ["Enter"]], ["read", ["-t", "9"]], ["capture-pane", ["-t", "1", "-S", "x"]]])`,
			`  try { await tools.terminal({ command, args }); errors.push('no error'); } catch (error) { errors.push(String(error.message ?? error)); }`,
			`return errors;`,
		}, "\n"), chat, &value)
		wants := []string{"send-keys: unknown option -- x", "usage: send-keys", "can't find window: 9", "capture-pane: invalid start line: x"}
		if len(value) != len(wants) {
			t.Fatalf("errors = %q", value)
		}
		for index, want := range wants {
			if !strings.Contains(value[index], want) {
				t.Fatalf("error %d = %q, want %q", index, value[index], want)
			}
		}
	})

	t.Run("guest turns may use it, like bash", func(t *testing.T) {
		guest := guestOf(t, fixture, owner, session, "full", "Guest")
		guestChat := chatOf(guest, session, "participant")
		var value struct {
			Window int    `json:"window"`
			Output string `json:"output"`
		}
		script(`return await tools.terminal({ command: "capture-pane", args: ["-p", "-t", "dev"] });`, guestChat, &value)
		if value.Window != 1 || !strings.Contains(value.Output, "from-owner") {
			t.Fatalf("capture-pane = %+v", value)
		}
		// Guests still have no terminal panel.
		denied, err := guest.Hold(p2test.Background, terminalscontract.Terminals, delta.JsonObjectOf("session", session, "view", randomView()), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		p2test.ExpectChord(t, p2runtime.WhenLive(p2test.Background, denied), data.CodeForbidden)
		denied.Release()
	})

	t.Run("the server serves the session's current worker only, on its environments", func(t *testing.T) {
		generation := fixture.Server.Workers().Generation(session)
		if generation == 0 {
			t.Fatal("no worker generation")
		}
		as := func(generation int) *host.Reference {
			t.Helper()
			ref, err := p2test.HoldIn(fixture.As(p2runtime.WorkerPrincipal{Session: session, Generation: generation}), terminalscontract.AgentTerminals, delta.NewJsonObject(0), "worker")
			if err != nil {
				t.Fatal(err)
			}
			return ref
		}
		_, err := as(generation-1).Call("list", delta.NewJsonObject(0), p2test.Background)
		p2test.ExpectChord(t, err, data.CodeForbidden)
		current := as(generation)
		windows, err := current.Call("list", delta.NewJsonObject(0), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(list(object(windows).Value("windows"))); got != 2 {
			t.Fatalf("windows = %d", got)
		}
		_, err = current.Call("open", delta.JsonObjectOf("key", "foreign", "location", delta.JsonObjectOf("cwd", "/", "env", "gpu")), p2test.Background)
		p2test.ExpectChord(t, err, data.CodeForbidden)
		// The owner is no worker.
		ownerView, err := owner.Hold(p2test.Background, terminalscontract.AgentTerminals, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		p2test.ExpectChord(t, p2runtime.WhenLive(p2test.Background, ownerView), data.CodeForbidden)
		ownerView.Release()
	})

	t.Run("a subagent's script opens windows at the subagent's own location", func(t *testing.T) {
		own := filepath.Join(workspace, "own")
		if err := os.Mkdir(own, 0o755); err != nil {
			t.Fatal(err)
		}
		opener := `return await tools.terminal({ command: "new-window", args: ["-n", "sub", "-w", "/^in-sub$/", "echo in-s''ub"] });`
		spawn, err := json.Marshal(map[string]any{"action": "spawn", "name": "own", "message": "codemode " + opener, "cwd": "own"})
		if err != nil {
			t.Fatal(err)
		}
		if spawned := call("subagent "+string(spawn), chat); spawned.Value("isError") == true {
			t.Fatalf("spawn: %s", resultText(spawned))
		}
		// Its answer is reported to the main conversation; that turn ends before the next script.
		if _, err := p2test.Until(chat.Member("view"), func(view delta.JsonValue) bool {
			return view != nil && hasText(view, func(text string) bool { return text == `<sender kind="subagent" name="own"/>` }) && !running(view)
		}, 60*time.Second); err != nil {
			t.Fatal(err)
		}
		var value struct {
			Windows []struct {
				Name     string `json:"name"`
				Opener   string `json:"opener"`
				Location string `json:"location"`
			} `json:"windows"`
		}
		script(`return await tools.terminal({ command: "list-windows" });`, chat, &value)
		found := 0
		for _, window := range value.Windows {
			if window.Name == "sub" {
				found++
				if window.Opener != "agent" || window.Location != own {
					t.Fatalf("window = %+v, want opener agent at %s", window, own)
				}
			}
		}
		if found != 1 {
			t.Fatalf("windows = %+v", value.Windows)
		}
	})

	t.Run("archiving the session closes its terminals", func(t *testing.T) {
		if _, err := sessions.Call("archive", delta.JsonObjectOf("id", session), p2test.Background); err != nil {
			t.Fatal(err)
		}
		pollUntil(t, func() bool { return len(listed()) == 0 })
		conn, err := terminals.ConnectTerminalHost(p2test.Background, fixture.DataDir)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		frame, _ := terminals.EncodeFrame(terminals.TerminalRequest{Type: terminals.RequestCall, ID: 1, Call: &terminals.HostCall{Op: terminals.OpList, SessionID: session}}.JSON())
		if _, err := conn.Write(frame); err != nil {
			t.Fatal(err)
		}
		var reply terminals.TerminalEvent
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		err = terminals.ReadFrames(conn, func(frame terminals.Frame) {
			if event, err := terminals.ParseEvent(frame.JSON); err == nil && event.Type == terminals.EventReply {
				reply = event
				_ = conn.Close()
			}
		})
		_ = err
		if reply.Result == nil || reply.Result.Kind != terminals.KindList || len(reply.Result.Windows) != 0 {
			t.Fatalf("the archived session's terminals: %+v", reply)
		}
	})

	t.Run("without code mode, tool_search loads the tool for the model", func(t *testing.T) {
		other, err := sessions.Call("create", delta.JsonObjectOf("cwd", workspace, "title", "No code mode"), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		otherChat := chatOf(owner, object(other).Value("id").(string), "participant")
		// Not loaded: the model is not offered it.
		if result := call(tool("list-windows"), otherChat); result.Value("isError") != true {
			t.Fatalf("an unloaded tool answered: %s", resultText(result))
		}
		if err := os.WriteFile(fixture.DataDir+"/settings.json", []byte(`{"codemode":false}`), 0o600); err != nil {
			t.Fatal(err)
		}
		var found string
		for attempt := 0; attempt < 20; attempt++ {
			if attempt > 0 {
				time.Sleep(200 * time.Millisecond)
			}
			result := call(`tool_search {"query":"terminal tmux windows"}`, otherChat)
			if found = resultText(result); result.Value("isError") != true {
				break
			}
		}
		if !strings.Contains(found, "- terminal: ") {
			t.Fatalf("tool_search: %q", found)
		}
		direct := call(tool("list-windows"), otherChat)
		if direct.Value("isError") == true || resultText(direct) != "No windows." {
			t.Fatalf("direct: %v %q", direct.Value("isError"), resultText(direct))
		}
	})
}
