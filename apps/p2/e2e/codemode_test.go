// Ports test/p2/codemode.test.ts (Pi main c23b9ec1): durable code mode in p2 (fake provider): a script calling two tools
// end to end, its listed calls, a worker killed or stopped mid-script that resumes without rerunning finished calls, and
// the plugin reloaded mid-script.
//
// The transcript's codemode card (Pi's `codemodeCard`: buildTranscript and `scriptCalls`) is vendored TypeScript with no Go
// form (web/upstream/plugins/codemode/nested.ts, tested by Pi's codemode-card.test.ts): the cases assert the data the card
// is made of (the result's `details.calls` and Durable's live nested slots) in its place.

package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

func codemodeResults(view delta.JsonValue) []*delta.JsonObject { return toolResults(view, "codemode") }

// codemodeText is a result's text parts joined by a line break (Pi's `text`).
func codemodeText(result *delta.JsonObject) string {
	if result == nil {
		return ""
	}
	var parts []string
	for _, part := range list(result.Value("content")) {
		if object(part).Value("type") == "text" {
			text, _ := object(part).Value("text").(string)
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

// detailCalls is the settled list of a code mode result: its script's tool calls (Durable writes it with the result).
func detailCalls(result *delta.JsonObject) []*delta.JsonObject {
	var calls []*delta.JsonObject
	for _, call := range list(object(result.Value("details")).Value("calls")) {
		calls = append(calls, object(call))
	}
	return calls
}

// nestedSleeping is the live nested slot running a `sleep` command, if any.
func nestedSleeping(view delta.JsonValue) *delta.JsonObject {
	for _, slot := range list(live(view).Value("nestedTools")) {
		if object(slot).Value("status") != "running" {
			continue
		}
		command, _ := object(object(slot).Value("arguments")).Value("command").(string)
		if strings.HasPrefix(command, "sleep") {
			return object(slot)
		}
	}
	return nil
}

func TestCodeModeFakeProvider(t *testing.T) {
	f := newAgentFixture(t, hosts.ServerOptions{})
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(f.Fixture.Workspace, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	send := func(chat *host.Reference, text, id string) {
		t.Helper()
		if _, err := chat.Call("send", delta.JsonObjectOf("text", text, "requestId", id), p2test.Background); err != nil {
			t.Fatal(err)
		}
	}
	settled := func(count int) func(delta.JsonValue) bool {
		return func(view delta.JsonValue) bool { return len(codemodeResults(view)) == count && !running(view) }
	}
	failedCallPattern := regexp.MustCompile(`^one \| two failed: .+ \| three$`)

	t.Run("runs a script whose tool calls are nested durable calls, and lists them in its result", func(t *testing.T) {
		if !slices.Contains(f.Fixture.Server.Plugins, "codemode") {
			t.Fatal("the codemode plugin is not loaded")
		}
		send(f.Chat, "codemode", "c1")
		view := f.until(f.Chat.Member("view"), settled(1))
		result := codemodeResults(view)[0]
		if result.Value("isError") != false {
			t.Fatal(codemodeText(result))
		}
		// Durable's coding-agent layout: a header, then each text item marked.
		if got := scriptOutput(t, codemodeText(result)); got != "==> text 1/2 <==\nbash: written\n==> text 2/2 <==\nfirst\n" {
			t.Fatalf("output = %q", got)
		}
		if got := read("codemode.log"); got != "first\n" {
			t.Fatalf("codemode.log = %q", got)
		}
		// Durable dropped the nested slots with the settled call; its result details list the calls.
		if live(view).Value("nestedTools") != nil {
			t.Fatalf("nestedTools = %v", live(view).Value("nestedTools"))
		}
		var got []any
		for _, call := range detailCalls(result) {
			got = append(got, []any{call.Value("id"), call.Value("name"), call.Value("status")})
		}
		if !sameJSON(got, []any{[]any{float64(1), "bash", "ok"}, []any{float64(2), "read", "ok"}}) {
			t.Fatalf("calls = %v", got)
		}
		calls := detailCalls(result)
		if args := jsonOf(t, calls[1].Value("args").(string)).(map[string]any); args["path"] != "codemode.log" {
			t.Fatalf("read args = %v", args)
		}
	})

	t.Run("shows nested calls live while the script runs", func(t *testing.T) {
		send(f.Chat, "codemode-restart", "c2")
		running := f.until(f.Chat.Member("view"), func(view delta.JsonValue) bool { return nestedSleeping(view) != nil })
		// The script's calls in call order: the finished bash, then the running one.
		var got []any
		for _, slot := range list(live(running).Value("nestedTools")) {
			got = append(got, []any{object(slot).Value("name"), object(slot).Value("status")})
		}
		if !sameJSON(got, []any{[]any{"bash", "done"}, []any{"bash", "running"}}) {
			t.Fatalf("live calls = %v", got)
		}
		// A compatible reload of code mode (and chat) keeps the sandbox: the script keeps waiting on its call.
		sleeping := nestedSleeping(running)
		result, err := f.Fixture.Server.Reload([]string{"codemode", "chat"}, p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		for _, each := range result.Workers {
			if each.Kind == plugins.RolloutStopped {
				t.Fatalf("a worker stopped: %v", result.Workers)
			}
		}
		// The same nested call is still running after the commit (the view resets, then follows Durable again).
		after := f.until(f.Chat.Member("view"), func(view delta.JsonValue) bool {
			slot := nestedSleeping(view)
			return slot != nil && slot.Value("taskId") == sleeping.Value("taskId")
		})
		if len(codemodeResults(after)) != 1 {
			t.Fatalf("codemode results = %d", len(codemodeResults(after)))
		}
	})

	t.Run("resumes the script after the worker is killed mid-call; finished calls do not run again", func(t *testing.T) {
		// The previous test left the script waiting in `sleep 5`: kill the worker process there.
		workers, err := f.Fixture.Server.Host.Get(p2test.Background, p2runtime.Hosts, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer workers.Release()
		states := f.until(workers.Member("workers"), func(value delta.JsonValue) bool {
			state := object(object(value).Value(f.Session))
			return state != nil && state.Value("running") == true && state.Value("pid") != nil
		})
		pid := int(object(object(states).Value(f.Session)).Value("pid").(float64))
		// The live slot shows before the nested bash is spawned (under -race a while before): kill once the worker has its
		// shell, so that the call is interrupted where Pi's (faster) run kills it. The shell outlives the worker's SIGKILL
		// (its own session) and `sleep 5; echo two >> codemode.log` would write `two` into the log the cases read later:
		// Pi's run finishes before that; the shell goes down after the worker here, so the interrupted call stays unwritten.
		var shells []*os.Process
		deadline := time.Now().Add(30 * time.Second)
		for len(shells) == 0 {
			if time.Now().After(deadline) {
				t.Fatal("the worker has no shell for the sleeping call")
			}
			listed, _ := exec.Command("pgrep", "-P", strconv.Itoa(pid)).Output()
			for _, field := range strings.Fields(string(listed)) {
				child, err := strconv.Atoi(field)
				if err != nil {
					t.Fatal(err)
				}
				shell, err := os.FindProcess(child)
				if err != nil {
					t.Fatal(err)
				}
				shells = append(shells, shell)
			}
			if len(shells) == 0 {
				time.Sleep(10 * time.Millisecond)
			}
		}
		worker, err := os.FindProcess(pid)
		if err != nil {
			t.Fatal(err)
		}
		if err := worker.Kill(); err != nil {
			t.Fatal(err)
		}
		for _, shell := range shells {
			shell.Kill()
		}
		f.until(workers.Member("workers"), func(value delta.JsonValue) bool {
			state := object(object(value).Value(f.Session))
			return state != nil && state.Value("running") == false
		})
		f.Chat.Release()
		// Opening the session again starts a worker; Durable resumes the replay-safe codemode call from its journal.
		f.Chat = f.hold(1)
		view := f.until(f.Chat.Member("view"), settled(2), 30*time.Second)
		result := codemodeResults(view)[1]
		if result.Value("isError") != false {
			t.Fatal(codemodeText(result))
		}
		if got := scriptOutput(t, codemodeText(result)); !failedCallPattern.MatchString(got) {
			t.Fatalf("output = %q", got)
		}
		// `one` ran once, before the restart; the interrupted `two` was not rerun (bash is not replay-safe).
		if got := read("codemode.log"); got != "first\none\nthree\n" {
			t.Fatalf("codemode.log = %q", got)
		}
		// The result lists each call once, including the one finished before the restart.
		var got []any
		for _, call := range detailCalls(result) {
			got = append(got, []any{call.Value("id"), call.Value("status")})
		}
		if !sameJSON(got, []any{[]any{float64(1), "ok"}, []any{float64(2), "error"}, []any{float64(3), "ok"}}) {
			t.Fatalf("calls = %v", got)
		}
	})

	t.Run("resumes the script after a graceful server and worker restart", func(t *testing.T) {
		send(f.Chat, "codemode-restart", "c3")
		f.until(f.Chat.Member("view"), func(view delta.JsonValue) bool { return nestedSleeping(view) != nil })
		f.Chat.Release()
		// Stopping closes the sandbox while the Harness is still open (holders are disposed first). Durable stores no
		// outcome for a run the sandbox ended on its own, so the restarted worker replays the script.
		server := f.Fixture.Server
		port, dataDir, selected := server.Port, server.DataDir, server.Plugins
		if err := server.Close(p2test.Background); err != nil {
			t.Fatal(err)
		}
		entry, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		fake, web := true, false
		restarted, err := hosts.StartServer(hosts.ServerOptions{DataDir: dataDir, Port: &port, Fake: &fake, Web: &web, Plugins: selected, Entry: entry, ExecArgv: []string{hosts.WorkerArg}})
		if err != nil {
			t.Fatal(err)
		}
		defer restarted.Close(p2test.Background)
		conn, err := p2test.Dial(restarted.Port, nil)
		if err != nil {
			t.Fatal(err)
		}
		client, err := p2test.ClientHost(conn)
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close(p2test.Background)
		again, err := p2test.HoldIn(client, chatcontract.Chat, delta.JsonObjectOf("session", f.Session, "conversation", float64(1)), "participant")
		if err != nil {
			t.Fatal(err)
		}
		defer again.Release()
		view := f.until(again.Member("view"), settled(3), 30*time.Second)
		result := codemodeResults(view)[2]
		if result.Value("isError") != false {
			t.Fatal(codemodeText(result))
		}
		if got := scriptOutput(t, codemodeText(result)); !failedCallPattern.MatchString(got) {
			t.Fatalf("output = %q", got)
		}
		if got := read("codemode.log"); got != "first\none\nthree\none\nthree\n" {
			t.Fatalf("codemode.log = %q", got)
		}
	})
}
