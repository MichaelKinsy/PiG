// Ports test/p2/terminal-tool.test.ts (Pi main c23b9ec1): the agent's terminal tool end to end with the fake provider and a
// real terminal host: the tool drives the session's terminals, which are the owner's (listed, labeled, typed into by both);
// guest turns may use it; tmux errors come back as tool errors; the server serves only the session's current worker, on the
// session's environments, and archiving closes the session's terminals. Without code mode, `tool_search` loads the tool.
//
// Go forced difference: Pi's cases drive the tool from code mode scripts (`codemode <code>` prompts), and the code mode
// plugin is not ported to apps/p2 yet (no `plugins/codemode`), so the fake provider's one fixed script is all a script can
// be. The model calls the tool directly instead, after `tool_search` loaded it, as Pi's last case does: the same commands,
// the same terminals, the result as the model reads it (the text) instead of a script's returned value. The case "a
// subagent's script opens windows at the subagent's own location" needs the subagent tools and code mode: not ported (the
// subagents plugin has no `subagent` tool yet, rows/p2-A.md).

package e2e_test

import (
	"os"
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
	// The e2e binary links the code mode plugin, which on (its default) leaves `tool_search` uninstalled: the model reaches the
	// tool through it only with code mode off. The worker applies the setting asynchronously: the first search goes until it
	// is found.
	if err := os.WriteFile(fixture.DataDir+"/settings.json", []byte(`{"codemode":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
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
	succeeds := func(prompt string, target *host.Reference) string {
		t.Helper()
		result := call(prompt, target)
		if result.Value("isError") == true {
			t.Fatalf("%s: %s", prompt, resultText(result))
		}
		return resultText(result)
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
	bytes := openByteChannel(t, fixture.Server.Port, object(granted).Value("token").(string))
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

	t.Run("tool_search loads the tool; a window opens, takes keys and reads its output", func(t *testing.T) {
		var found string
		for attempt := 0; attempt < 20; attempt++ {
			if attempt > 0 {
				time.Sleep(200 * time.Millisecond)
			}
			result := call(`tool_search {"query":"terminal tmux windows"}`, chat)
			if found = resultText(result); result.Value("isError") != true {
				break
			}
		}
		if !strings.Contains(found, "- terminal: ") {
			t.Fatalf("tool_search: %s", found)
		}
		opened := succeeds(tool("new-window", "-n", "dev", "-w", "/^hello-agent$/", "echo hello-age''nt"), chat)
		if !strings.HasPrefix(opened, "window 1 · ") || !regexp.MustCompile(`(?m)^hello-agent$`).MatchString(opened) {
			t.Fatalf("new-window: %q", opened)
		}
		sent := succeeds(tool("send-keys", "-t", "dev", "-w", "/^two$/", "echo t''wo", "Enter"), chat)
		if !regexp.MustCompile(`(?m)^two$`).MatchString(sent) || strings.Contains(sent, "hello-agent") {
			t.Fatalf("send-keys: %q", sent)
		}
		windows := succeeds(tool("list-windows"), chat)
		if !strings.HasPrefix(windows, "1: dev · "+workspace+" · opened by agent · ") {
			t.Fatalf("list-windows: %q", windows)
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
		read := succeeds(tool("read", "-t", "1", "-w", "/^from-owner$/"), chat)
		if strings.Contains(read, "timed out") || !regexp.MustCompile(`(?m)^from-owner$`).MatchString(read) {
			t.Fatalf("read: %q", read)
		}
		// The owner opens a terminal; the agent sees and drives it.
		if _, err := view.Call("open", delta.JsonObjectOf("cols", float64(80), "rows", float64(10), "conversation", float64(1)), p2test.Background); err != nil {
			t.Fatal(err)
		}
		pollUntil(t, func() bool { return len(listed()) == 2 })
		owned := succeeds(tool("send-keys", "-t", "2", "-w", "/^owned-by-user$/", "-l", "echo own''ed-by-user\n"), chat)
		if strings.Contains(owned, "timed out") || !regexp.MustCompile(`(?m)^owned-by-user$`).MatchString(owned) {
			t.Fatalf("send-keys: %q", owned)
		}
		windows := succeeds(tool("list-windows"), chat)
		lines := strings.Split(windows, "\n")
		if len(lines) != 2 || !strings.Contains(lines[0], "opened by agent") || !strings.HasPrefix(lines[1], "2: ") || !strings.Contains(lines[1], "opened by user") {
			t.Fatalf("list-windows: %q", windows)
		}
	})

	t.Run("tmux errors and a missing window are tool errors", func(t *testing.T) {
		for _, each := range []struct {
			args []string
			want string
		}{
			{[]string{"send-keys", "-x"}, "send-keys: unknown option -- x"},
			{[]string{"send-keys", "Enter"}, "usage: send-keys"},
			{[]string{"read", "-t", "9"}, "can't find window: 9"},
			{[]string{"capture-pane", "-t", "1", "-S", "x"}, "capture-pane: invalid start line: x"},
		} {
			result := call(tool(each.args[0], each.args[1:]...), chat)
			if result.Value("isError") != true || !strings.Contains(resultText(result), each.want) {
				t.Fatalf("%v: %v %q", each.args, result.Value("isError"), resultText(result))
			}
		}
	})

	t.Run("guest turns may use it, like bash", func(t *testing.T) {
		guest := guestOf(t, fixture, owner, session, "full", "Guest")
		guestChat := chatOf(guest, session, "participant")
		captured := succeeds(tool("capture-pane", "-p", "-t", "dev"), guestChat)
		if !strings.HasPrefix(captured, "window 1 · ") || !strings.Contains(captured, "from-owner") {
			t.Fatalf("capture-pane: %q", captured)
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
