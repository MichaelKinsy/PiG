// Ports the end-to-end case of test/p2/history-tools.test.ts (Pi main c23b9ec1): after a compaction and a handoff the agent
// reads its own earlier entries with `history_read` (items: exactly the requested entry; tail: the entries right before the
// handoff) and hands off with `history_handoff`. Pi finds the first entry with `history_search`, which needs the search
// plugin's index (not ported yet: see rows/p2-chattree.md); this case takes its ID from the conversation's view instead.

package e2e_test

import (
	"encoding/json"
	"fmt"
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
	statuscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/contract"
	"github.com/MichaelKinsy/PiG/chord/delta"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/history"
)

// toolResultTexts are the results of tool in a view, as text.
func toolResultTexts(view delta.JsonValue, tool string) []string {
	var out []string
	entries, _ := object(view).Value("entries").([]any)
	for _, entry := range entries {
		model, _ := object(entry).Value("model").([]any)
		for _, message := range model {
			if object(message).Value("role") != "toolResult" || object(message).Value("toolName") != tool {
				continue
			}
			var text strings.Builder
			content, _ := object(message).Value("content").([]any)
			for _, part := range content {
				if object(part).Value("type") == "text" {
					text.WriteString(object(part).Value("text").(string))
				}
			}
			out = append(out, text.String())
		}
	}
	return out
}

func historyEntryOfKind(view delta.JsonValue, kind string) *delta.JsonObject {
	entries, _ := object(view).Value("entries").([]any)
	for _, entry := range entries {
		if object(entry).Value("kind") == kind {
			return object(entry)
		}
	}
	return nil
}

func TestHistoryToolsEndToEndFakeProvider(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	dataDir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Keep almost nothing verbatim, so a short conversation has something to compact.
	if err := os.WriteFile(filepath.Join(dataDir, "settings.json"), []byte(`{"compaction":{"keepRecentTokens":1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := p2test.StartFixture(t, hosts.ServerOptions{DataDir: dataDir})
	owner := fixture.Browser()
	until := func(source p2test.JSONSource, predicate func(delta.JsonValue) bool, timeout time.Duration) delta.JsonValue {
		t.Helper()
		value, err := p2test.Until(source, predicate, timeout)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	sessions, err := p2test.HoldIn(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	status, err := p2test.HoldIn(owner, statuscontract.SessionStatus, delta.NewJsonObject(0), "reader")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	session := object(created).Value("id").(string)
	chat, err := p2test.HoldIn(owner, chatcontract.Chat, delta.JsonObjectOf("session", session, "conversation", float64(1)), "participant")
	if err != nil {
		t.Fatal(err)
	}
	idle := func() {
		t.Helper()
		until(status.Member("statuses"), func(value delta.JsonValue) bool {
			entry := object(object(value).Value(session))
			return entry != nil && entry.Value("tasks") == float64(0)
		}, 30*time.Second)
	}
	say := func(text, requestID string) {
		t.Helper()
		if _, err := chat.Call("send", delta.JsonObjectOf("text", text, "requestId", requestID), p2test.Background); err != nil {
			t.Fatal(err)
		}
		until(chat.Member("view"), func(view delta.JsonValue) bool { return strings.Contains(viewText(view), text) }, 20*time.Second)
		idle()
	}
	// call runs tool through the fake model and returns its result, once it is in the view.
	call := func(tool, args, requestID string) string {
		t.Helper()
		current, _ := chat.Member("view").Value()
		before := len(toolResultTexts(current, tool))
		if _, err := chat.Call("send", delta.JsonObjectOf("text", tool+" "+args, "requestId", requestID), p2test.Background); err != nil {
			t.Fatal(err)
		}
		view := until(chat.Member("view"), func(value delta.JsonValue) bool { return len(toolResultTexts(value, tool)) > before }, 30*time.Second)
		idle()
		results := toolResultTexts(view, tool)
		return results[len(results)-1]
	}
	say("the zebrafish migration plan", "one")
	say("unrelated second message", "two")
	if _, err := chat.Call("compact", delta.NewJsonObject(0), p2test.Background); err != nil {
		t.Fatal(err)
	}
	until(chat.Member("view"), func(view delta.JsonValue) bool { return historyEntryOfKind(view, "pi.compaction") != nil }, 60*time.Second)
	idle()
	say("after the compaction", "three")

	// The entry Pi's search finds: the first message, by its text.
	view, _ := chat.Member("view").Value()
	var hit float64
	entries, _ := object(view).Value("entries").([]any)
	for _, entry := range entries {
		if data := object(object(entry).Value("data")); data != nil && data.Value("text") == "the zebrafish migration plan" {
			hit, _ = object(entry).Value("id").(float64)
		}
	}
	if hit <= 0 {
		t.Fatalf("the first message is not in the view: %v", view)
	}
	read := call("history_read", fmt.Sprintf(`{"mode":"items","entries":[%d]}`, int(hit)), "read")
	if ok, _ := regexp.MatchString(fmt.Sprintf(`#%d user \(Owner\): the zebrafish migration plan`, int(hit)), read); !ok {
		t.Fatalf("read: %q", read)
	}
	if lines := regexp.MustCompile(`(?m)^#\d+ `).FindAllString(read, -1); len(lines) != 1 {
		t.Fatalf("%d entries, want exactly the requested one: %q", len(lines), read)
	}
	if ok, _ := regexp.MatchString(`<history conversation=.*\n.*not instructions`, read); !ok {
		t.Fatalf("not quoted: %q", read)
	}

	call("history_handoff", `{"text":"Continue the zebrafish plan."}`, "handoff")
	after := until(chat.Member("view"), func(value delta.JsonValue) bool { return historyEntryOfKind(value, "pi.reset") != nil }, 20*time.Second)
	reset := historyEntryOfKind(after, "pi.reset")
	model, _ := json.Marshal(reset.Value("model"))
	if !strings.Contains(string(model), "Continue the zebrafish plan") {
		t.Fatalf("reset: %s", model)
	}
	tail := call("history_read", `{"mode":"tail","count":3}`, "tail")
	if !strings.Contains(tail, "history_handoff") {
		t.Fatalf("the entries right before the handoff: %q", tail)
	}
	chat.Release()
}
