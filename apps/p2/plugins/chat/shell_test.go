package chat_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports apps/p2/test/p2/shell.test.ts: user shell commands (`!cmd`/`!!cmd`) on a bare session host.

// shellEntries are the `p2.shell` entries of a view: their data and model messages.
func shellEntries(view delta.JsonValue) []*delta.JsonObject {
	var found []*delta.JsonObject
	for _, each := range list(obj(view).Value("entries")) {
		if entry := obj(each); entry.Value("kind") == "p2.shell" {
			found = append(found, entry)
		}
	}
	return found
}

func hasShells(count int) func(delta.JsonValue) bool {
	return func(view delta.JsonValue) bool { return len(shellEntries(view)) == count }
}

func answered(view delta.JsonValue) bool {
	for _, each := range list(obj(view).Value("entries")) {
		model := list(obj(each).Value("model"))
		if len(model) > 0 && obj(model[0]).Value("role") == "assistant" {
			return true
		}
	}
	return false
}

func TestShellBangPlacesTheOutputInTheContextWithoutARunAndDoubleBangKeepsItOut(t *testing.T) {
	fixture := newSessionHost(t, delta.NewJsonObject(0))
	chat := fixture.hold(t, owner, "participant")
	mustCall(t, chat, "shell", delta.JsonObjectOf("command", "echo out; echo err >&2; exit 3", "includeInContext", true, "requestId", "1"))
	view := until(t, chat.Member("view"), hasShells(1))
	first := obj(shellEntries(view)[0].Value("data"))
	if got := text(first, "command"); got != "echo out; echo err >&2; exit 3" {
		t.Errorf("command = %q", got)
	}
	output := text(first, "output")
	if !strings.Contains(output, "out") || !strings.Contains(output, "err") {
		t.Errorf("output = %q", output)
	}
	if first.Value("exitCode") != float64(3) {
		t.Errorf("exitCode = %v", first.Value("exitCode"))
	}
	author := obj(first.Value("author"))
	if text(author, "kind") != "owner" || text(author, "id") != "owner" || text(author, "name") != "Owner" {
		t.Errorf("author = %v", author)
	}
	if first.Value("cancelled") != false {
		t.Errorf("cancelled = %v", first.Value("cancelled"))
	}
	model := marshal(t, shellEntries(view)[0].Value("model"))
	if !strings.Contains(model, "Ran `echo out; echo err >&2; exit 3`") || !strings.Contains(model, "Command exited with code 3") {
		t.Errorf("model = %s", model)
	}
	if answered(view) {
		t.Error("a command never starts a run")
	}
	// The live state clears once the entry is placed.
	until(t, chat.Member("liveShell"), func(value delta.JsonValue) bool { return value == nil })

	mustCall(t, chat, "shell", delta.JsonObjectOf("command", "echo private", "includeInContext", false, "requestId", "2"))
	view = until(t, chat.Member("view"), hasShells(2))
	second := shellEntries(view)[1]
	if obj(second.Value("data")).Value("includeInContext") != false {
		t.Error("includeInContext of the second command must be false")
	}
	if second.Value("model") != nil {
		t.Errorf("`!!` is display-only, model = %v", second.Value("model"))
	}
	// The model's next context has the `!` command and not the `!!` one.
	conversation, err := fixture.durable.Conversation(p2test.Background, 1)
	if err != nil || conversation == nil {
		t.Fatalf("conversation: %v", err)
	}
	whole, err := conversation.Context(p2test.Background, nil)
	if err != nil {
		t.Fatal(err)
	}
	messages := marshal(t, whole.Messages)
	if !strings.Contains(messages, "Ran `echo out;") || strings.Contains(messages, "echo private") {
		t.Errorf("messages = %s", messages)
	}
	// A retried call with the same request ID runs once; the command's ID is the caller's request ID.
	again := mustCall(t, chat, "shell", delta.JsonObjectOf("command", "echo private", "includeInContext", false, "requestId", "2"))
	if text(again, "shellId") != "2" {
		t.Errorf("shellId = %v", again)
	}
	// Settled: the retried call placed nothing (a third command would show once placed).
	mustCall(t, chat, "shell", delta.JsonObjectOf("command", "true", "includeInContext", false, "requestId", "3"))
	view = until(t, chat.Member("view"), hasShells(3))
	if len(shellEntries(view)) != 3 {
		t.Errorf("shell entries = %d, want 3 (the retried call ran once)", len(shellEntries(view)))
	}
	// The plain-notice text of the web: the output fenced, how it ended.
	if !regexp.MustCompile("(?s)```\nout\nerr\n```|```\nerr\nout\n```").MatchString(text(first, "output")) {
		// Pi shows the transcript marker; here the entry's own output holds both lines in arrival order.
		if !strings.Contains(output, "out") {
			t.Errorf("output = %q", output)
		}
	}
}

func TestShellOneCommandAtATimeEscapeCancelsFullGuestsRunViewersWatch(t *testing.T) {
	fixture := newSessionHost(t, delta.NewJsonObject(0))
	aliceChat := fixture.hold(t, alice, "participant")
	ownerChat := fixture.hold(t, owner, "participant")
	// View-only guests cannot run commands.
	bobChat := fixture.hold(t, bob, "viewer")
	started := mustCall(t, aliceChat, "shell", delta.JsonObjectOf("command", "echo started; sleep 20; echo late", "includeInContext", true, "requestId", "a"))
	shellID := text(started, "shellId")
	// Everyone sees it run, with its output so far. It is a job and one active call while it runs (a move or a clone waits
	// for it).
	live := until(t, bobChat.Member("liveShell"), func(value delta.JsonValue) bool {
		shell := obj(value)
		return shell != nil && shell.Value("running") == true && strings.Contains(text(shell, "output"), "started")
	})
	author := obj(obj(live).Value("author"))
	if text(author, "kind") != "guest" || text(author, "id") != "alice" || text(author, "name") != "Alice" {
		t.Errorf("author = %v", author)
	}
	if got := fixture.liveness.Counts()["activeCalls"]; got != 1 {
		t.Errorf("activeCalls = %d, want 1", got)
	}
	_, err := call(ownerChat, "shell", delta.JsonObjectOf("command", "true", "includeInContext", true, "requestId", "b"))
	if err == nil || !strings.Contains(err.Error(), chatcontract.ShellBusy) {
		t.Errorf("a second command must be refused with the busy text, got %v", err)
	}
	// The owner may cancel anyone's command; a cancelled one is placed as cancelled.
	if aborted := mustCall(t, ownerChat, "abortShell", delta.JsonObjectOf("shellId", shellID)); obj(aborted).Value("aborted") != true {
		t.Errorf("abortShell = %v", aborted)
	}
	view := until(t, ownerChat.Member("view"), hasShells(1))
	cancelled := obj(shellEntries(view)[0].Value("data"))
	if cancelled.Value("cancelled") != true {
		t.Errorf("cancelled = %v", cancelled.Value("cancelled"))
	}
	if strings.Contains(text(cancelled, "output"), "late") {
		t.Errorf("output = %q", text(cancelled, "output"))
	}
	if text(obj(cancelled.Value("author")), "id") != "alice" {
		t.Errorf("author = %v", cancelled.Value("author"))
	}
	until(t, ownerChat.Member("liveShell"), func(value delta.JsonValue) bool { return value == nil })
	if err := p2test.Poll(func() (bool, error) { return fixture.liveness.Counts()["activeCalls"] == 0, nil }, 0, "the command's active call ends"); err != nil {
		t.Fatal(err)
	}
	// Only its author or the owner cancels a running command.
	second := mustCall(t, ownerChat, "shell", delta.JsonObjectOf("command", "sleep 20", "includeInContext", false, "requestId", "c"))
	until(t, aliceChat.Member("liveShell"), func(value delta.JsonValue) bool { return obj(value) != nil && obj(value).Value("running") == true })
	_, err = call(aliceChat, "abortShell", delta.JsonObjectOf("shellId", text(second, "shellId")))
	p2test.ExpectChord(t, err, data.CodeForbidden)
	if aborted := mustCall(t, ownerChat, "abortShell", delta.JsonObjectOf("shellId", text(second, "shellId"))); obj(aborted).Value("aborted") != true {
		t.Errorf("abortShell = %v", aborted)
	}
	until(t, ownerChat.Member("view"), hasShells(2))
}
