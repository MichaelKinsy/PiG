package tui_test

// `!cmd`/`!!cmd` in the TUI (p2-tui-port.md section 10.8): pi's bash mode border, `Chat.shell`, the live command as pi's bash execution component at the end of
// the transcript, Escape cancels, one command at a time, and finished `p2.shell` entries rendered by the chat facet.
//
// pi: apps/p2/test/p2/tui-shell.test.ts

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	chattui "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/tui"
	statustui "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/tui"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	p2tui "github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tuitest"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
)

var meta = delta.JsonObjectOf("provider", "faux", "modelId", "faux-1", "name", "Faux", "contextWindow", float64(1000), "reasoning", false, "thinkingLevels", []any{"off"})

func owner() *delta.JsonObject {
	return delta.JsonObjectOf("kind", "owner", "id", "owner", "name", "Owner")
}

func TestBangAndDoubleBangRunCommandsTheLiveOneShowsAtTheEndEscapeCancelsEntriesRenderAsPi(t *testing.T) {
	finished := delta.JsonObjectOf(
		"author", delta.JsonObjectOf("kind", "guest", "id", "alice", "name", "Alice"), "command", "make test",
		"output", "FAIL parser\n\x1b]52;c;evil\x07done", "exitCode", float64(3), "cancelled", false, "truncated", false, "includeInContext", true)
	entry := tuitest.Entry(1, p2runtime.ShellEntry)
	entry.Data = finished
	view := tuitest.View(t, 1, []durable.EntryRecord{entry}, tuitest.DocsOf(t, "pi.agent", delta.JsonObjectOf("model", delta.JsonObjectOf("provider", "faux", "modelId", "faux-1"))), 0)
	var mu sync.Mutex
	var shells []*delta.JsonObject
	var aborts []string
	chat := tuitest.NewChatStub(t, tuitest.ChatStubOptions{
		Views: func(int) delta.JsonValue { return view }, Model: meta,
		Members: func(_ int, self p2test.StubSelf, members *p2test.StubMembers) {
			liveShell := self.Cell(nil)
			members.State["liveShell"] = liveShell
			var live *delta.JsonObject
			members.Methods["shell"] = func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
				object := args.(*delta.JsonObject)
				mu.Lock()
				shells = append(shells, object)
				id := fmt.Sprintf("sh%d", len(shells))
				mu.Unlock()
				live = delta.JsonObjectOf("id", id, "command", object.Value("command"), "author", owner(), "includeInContext", object.Value("includeInContext"),
					"output", "partial output", "truncated", false, "running", true, "cancelled", false)
				_ = liveShell.Replace(live)
				return delta.JsonObjectOf("shellId", id), nil
			}
			members.Methods["abortShell"] = func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
				id, _ := args.(*delta.JsonObject).Value("shellId").(string)
				mu.Lock()
				aborts = append(aborts, id)
				mu.Unlock()
				if live == nil {
					return delta.JsonObjectOf("aborted", false), nil
				}
				live = delta.JsonObjectOf("id", live.Value("id"), "command", live.Value("command"), "author", owner(), "includeInContext", live.Value("includeInContext"),
					"output", "partial output", "truncated", false, "running", false, "cancelled", true)
				_ = liveShell.Replace(live)
				return delta.JsonObjectOf("aborted", true), nil
			}
		},
	})
	top := tuitest.NewTopology(t, tuitest.TopologyOptions{
		Server: []p2test.Stub{tuitest.SessionsStub(tuitest.Session)}, Worker: []p2test.Stub{chat.Stub},
		Facets: []host.Facet{p2tui.CoreFacet, chattui.Facet, statustui.Facet}, Columns: 100,
	})
	shellCount := func() int { mu.Lock(); defer mu.Unlock(); return len(shells) }
	waitFor(t, top.Fixture, "$ make test · Alice (guest)")
	shown := top.Screen()
	if !regexp.MustCompile(`FAIL parser`).MatchString(shown) || !regexp.MustCompile(`\(exit 3\)`).MatchString(shown) || strings.Contains(shown, "52;c;evil") {
		t.Fatalf("a finished command shows its output sanitized and how it ended:\n%s", shown)
	}
	// Bash mode: the editor border takes pi's bash color while the text starts with `!`.
	typed(top.Fixture, "!ls -la")
	tuitest.Poll(t, func() bool {
		bash := false
		top.Do(func() {
			current := top.Runtime.Theme().Current()
			bash = top.Runtime.Surface("chat").(*chattui.ChatSurface).Editor.BorderColor("x") == current.GetBashModeBorderColor()("x")
		})
		return bash
	})
	top.Send(enter)
	tuitest.Poll(t, func() bool { return shellCount() == 1 })
	mu.Lock()
	first := shells[0]
	mu.Unlock()
	if first.Value("command") != "ls -la" || first.Value("includeInContext") != true || first.Value("requestId") == nil {
		t.Fatalf("shell args = %v", first)
	}
	tuitest.Poll(t, func() bool { screen := top.Screen(); return strings.Contains(screen, "$ ls -la") && strings.Contains(screen, "partial output") })
	if !strings.Contains(top.Screen(), "Running...") {
		t.Fatalf("no running indicator:\n%s", top.Screen())
	}
	editor := func() (text string) {
		top.Do(func() { text = top.Runtime.Surface("chat").(*chattui.ChatSurface).Editor.Text() })
		return text
	}
	tuitest.Poll(t, func() bool { return editor() == "" })
	// One at a time: a second command is refused with pi's text and stays in the editor.
	typed(top.Fixture, "!!pwd")
	top.Send(enter)
	tuitest.Poll(t, func() bool {
		busy := false
		top.Do(func() {
			for _, notice := range top.Runtime.Notices {
				busy = busy || notice.Text == chatcontract.ShellBusy
			}
		})
		return busy
	})
	if shellCount() != 1 || editor() != "!!pwd" {
		t.Fatalf("shell calls %d, editor %q", shellCount(), editor())
	}
	// Escape cancels the running command (not the agent).
	top.Send(escape)
	tuitest.Poll(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(aborts) == 1 })
	if aborts[0] != "sh1" {
		t.Fatalf("aborts = %v", aborts)
	}
	waitFor(t, top.Fixture, "(cancelled)")
	if strings.Contains(top.Screen(), "Running...") {
		t.Fatal("still running")
	}
	// `!!`: excluded from the context.
	top.Send(enter)
	tuitest.Poll(t, func() bool { return shellCount() == 2 })
	mu.Lock()
	second := shells[1]
	mu.Unlock()
	if second.Value("includeInContext") != false || second.Value("command") != "pwd" {
		t.Fatalf("second shell args = %v", second)
	}
	tuitest.Poll(t, func() bool { return editor() == "" })
}
