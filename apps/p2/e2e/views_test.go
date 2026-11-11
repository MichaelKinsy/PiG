// Ports test/p2/views.test.ts (Pi main da57c166), with the fake provider: the agent's `view` tool opens, reads, changes and
// closes views; people select and annotate, and their next message carries that once; a custom view's page is served with
// its bridge to the session's viewers only; the owner's library keeps views for other sessions.

package e2e_test

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	viewscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/views/contract"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/files"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/views"
)

const viewsPage = "<!doctype html><html><head><title>Glyph</title></head><body><p id=p>glyph</p></body></html>"

func toolText(message *delta.JsonObject) string {
	if message == nil {
		return ""
	}
	var out strings.Builder
	parts, _ := message.Value("content").([]any)
	for _, part := range parts {
		if object(part).Value("type") == "text" {
			out.WriteString(object(part).Value("text").(string))
		}
	}
	return out.String()
}

func viewsJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := chordjson.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestViewsFakeProvider(t *testing.T) {
	f := newAgentFixture(t, hosts.ServerOptions{})
	if err := os.WriteFile(filepath.Join(f.Fixture.Workspace, "notes.md"), []byte("# Notes\n\nThe letter a.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	owner := f.Owner
	views, err := p2test.HoldIn(owner, viewscontract.Views, delta.JsonObjectOf("session", f.Session), "editor")
	if err != nil {
		t.Fatal(err)
	}
	sent := 0
	// tool has the agent call `view` with args on chat; its result's text.
	tool := func(args map[string]any, on ...*host.Reference) string {
		t.Helper()
		chat := f.Chat
		if len(on) > 0 {
			chat = on[0]
		}
		current, _ := chat.Member("view").Value()
		before := len(toolResults(current, "view"))
		encoded, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		sent++
		if _, err := chat.Call("send", delta.JsonObjectOf("text", "view "+string(encoded), "requestId", "view-"+strconv.Itoa(sent)), p2test.Background); err != nil {
			t.Fatal(err)
		}
		view := f.until(chat.Member("view"), func(value delta.JsonValue) bool {
			return value != nil && len(toolResults(value, "view")) == before+1 && idle(value)
		})
		results := toolResults(view, "view")
		return toolText(results[len(results)-1])
	}
	matches := func(t *testing.T, text, pattern string) {
		t.Helper()
		if !regexp.MustCompile(pattern).MatchString(text) {
			t.Fatalf("%q does not match %s", text, pattern)
		}
	}
	listLen := func(n int) func(delta.JsonValue) bool {
		return func(v delta.JsonValue) bool { return len(list(v)) == n }
	}
	byID := func(value delta.JsonValue, id string) *delta.JsonObject {
		for _, each := range list(value) {
			if object(each).Value("id") == id {
				return object(each)
			}
		}
		return nil
	}

	t.Run("opens views, reads and changes their state, and closes them", func(t *testing.T) {
		matches(t, tool(map[string]any{"action": "open", "kind": "markdown", "title": "Notes", "path": "notes.md"}), `Opened view "notes"`)
		matches(t, tool(map[string]any{"action": "open", "kind": "custom", "title": "Glyph", "id": "glyph", "html": viewsPage, "state": map[string]any{"points": 3}}), `Opened view "glyph"`)
		listed := f.until(views.Member("views"), listLen(2))
		for i, want := range [][3]any{{"notes", "markdown", nil}, {"glyph", "custom", 1.0}} {
			view := object(list(listed)[i])
			if view.Value("id") != want[0] || view.Value("kind") != want[1] || view.Value("source") != want[2] {
				t.Fatalf("view %d = %v, want %v", i, view, want)
			}
		}
		state, err := p2test.HoldIn(owner, viewscontract.ViewState, delta.JsonObjectOf("session", f.Session, "view", "glyph"), "editor")
		if err != nil {
			t.Fatal(err)
		}
		defer state.Release()
		f.until(state.Member("value"), func(v delta.JsonValue) bool { return viewsJSON(t, v) == `{"points":3}` })
		tool(map[string]any{"action": "update", "id": "glyph", "merge": map[string]any{"selected": 1}})
		f.until(state.Member("value"), func(v delta.JsonValue) bool { return viewsJSON(t, v) == `{"points":3,"selected":1}` })
		matches(t, tool(map[string]any{"action": "update", "id": "glyph", "html": strings.Replace(viewsPage, "glyph", "glyph v2", 1)}), `html \(version 2\)`)
		f.until(views.Member("views"), func(v delta.JsonValue) bool {
			return byID(v, "glyph") != nil && byID(v, "glyph").Value("source") == 2.0
		})
		matches(t, tool(map[string]any{"action": "read", "id": "glyph"}), `State: \{"points":3,"selected":1\}`)
		// Opening an existing ID updates it and brings it to the front; another kind under it is refused.
		current, _ := views.Member("views").Value()
		focus, _ := byID(current, "notes").Value("focus").(float64)
		tool(map[string]any{"action": "open", "kind": "markdown", "title": "Notes", "id": "notes"})
		f.until(views.Member("views"), func(v delta.JsonValue) bool {
			next, _ := byID(v, "notes").Value("focus").(float64)
			return next > focus
		})
		matches(t, tool(map[string]any{"action": "open", "kind": "changes", "title": "x", "id": "notes"}), `is a markdown view`)
		matches(t, tool(map[string]any{"action": "close", "id": "notes"}), `Closed view "notes"`)
		f.until(views.Member("views"), listLen(1))
	})

	t.Run("carries selections and annotations with the next message, once", func(t *testing.T) {
		call := func(method string, args *delta.JsonObject) {
			t.Helper()
			if _, err := views.Call(method, args, p2test.Background); err != nil {
				t.Fatal(err)
			}
		}
		call("select", delta.JsonObjectOf("id", "glyph", "selection", delta.JsonObjectOf("summary", "point 2 of glyph a", "data", delta.JsonObjectOf("point", 2.0))))
		call("annotate", delta.JsonObjectOf("kind", "comment", "view", "transcript", "quote", "Echo", "note", "too loud"))
		call("annotate", delta.JsonObjectOf("kind", "delete", "view", "glyph", "quote", "glyph v2"))
		if _, err := f.Chat.Call("send", delta.JsonObjectOf("text", "make it bolder", "requestId", "context-1"), p2test.Background); err != nil {
			t.Fatal(err)
		}
		hasInput := func(view delta.JsonValue) *delta.JsonObject {
			entries, _ := object(view).Value("entries").([]any)
			for _, entry := range entries {
				if object(entry).Value("kind") == viewscontract.ViewInput {
					return object(entry)
				}
			}
			return nil
		}
		view := f.until(f.Chat.Member("view"), func(v delta.JsonValue) bool { return v != nil && hasInput(v) != nil && idle(v) })
		entry := hasInput(view)
		if got := object(entry.Value("data")).Value("text"); got != "make it bolder" {
			t.Fatalf("entry text = %v", got)
		}
		model, _ := entry.Value("model").([]any)
		var sentText strings.Builder
		if len(model) > 0 {
			switch content := object(model[0]).Value("content").(type) {
			case string:
				sentText.WriteString(content)
			case []any:
				for _, part := range content {
					if object(part).Value("type") == "text" {
						sentText.WriteString(object(part).Value("text").(string))
					}
				}
			}
		}
		matches(t, sentText.String(), `make it bolder\n\n<views>`)
		matches(t, sentText.String(), `Selected in view "glyph" \(custom, "Glyph"\): point 2 of glyph a`)
		matches(t, sentText.String(), `Comment in the transcript on «Echo»: too loud`)
		matches(t, sentText.String(), `Remove from view "glyph".*«glyph v2»`)
		f.until(views.Member("annotations"), func(v delta.JsonValue) bool {
			all := len(list(v)) == 2
			for _, each := range list(v) {
				all = all && object(each).Value("sent") == true
			}
			return all
		})
		current, _ := views.Member("views").Value()
		if object(byID(current, "glyph").Value("selection")).Value("sent") != true {
			t.Fatal("the selection is not marked sent")
		}
		// Nothing new: the next message is a plain input.
		if _, err := f.Chat.Call("send", delta.JsonObjectOf("text", "and again", "requestId", "context-2"), p2test.Background); err != nil {
			t.Fatal(err)
		}
		f.until(f.Chat.Member("view"), func(v delta.JsonValue) bool {
			entries, _ := object(v).Value("entries").([]any)
			for _, each := range entries {
				if object(each).Value("kind") == "p2.input" && object(object(each).Value("data")).Value("text") == "and again" {
					return true
				}
			}
			return false
		})
		// The agent reads annotations and resolves them.
		matches(t, tool(map[string]any{"action": "read", "id": "transcript"}), `too loud`)
		matches(t, tool(map[string]any{"action": "resolve", "id": "transcript"}), `Resolved 1 annotation`)
		f.until(views.Member("annotations"), listLen(1))
		// What the person drops never goes.
		call("annotate", delta.JsonObjectOf("kind", "comment", "view", "glyph", "quote", "glyph", "note", "drop me"))
		call("discard", nil)
		f.until(views.Member("annotations"), func(v delta.JsonValue) bool {
			all := true
			for _, each := range list(v) {
				all = all && object(each).Value("sent") == true
			}
			return all
		})
	})

	t.Run("arranges the person's panes and refuses layouts that name no view", func(t *testing.T) {
		layout := map[string]any{"column": []any{"glyph", map[string]any{"tabs": []any{"transcript"}}}, "sizes": []any{2, 1}}
		matches(t, tool(map[string]any{"action": "arrange", "layout": layout}), `Arranged`)
		arranged := f.until(views.Member("arrangement"), func(v delta.JsonValue) bool { return v != nil })
		if got := viewsJSON(t, object(arranged).Value("layout")); got != `{"column":["glyph",{"tabs":["transcript"]}],"sizes":[2,1]}` {
			t.Fatalf("layout = %s", got)
		}
		matches(t, tool(map[string]any{"action": "arrange", "layout": map[string]any{"row": []any{"transcript", "nope"}}}), `no view "nope"`)
		matches(t, tool(map[string]any{"action": "list"}), `Layout you arranged last`)
	})

	t.Run("saves people's state changes within bounds", func(t *testing.T) {
		state, err := p2test.HoldIn(owner, viewscontract.ViewState, delta.JsonObjectOf("session", f.Session, "view", "glyph"), "editor")
		if err != nil {
			t.Fatal(err)
		}
		defer state.Release()
		if _, err := state.Call("set", delta.JsonObjectOf("value", delta.JsonObjectOf("points", 4.0)), p2test.Background); err != nil {
			t.Fatal(err)
		}
		f.until(state.Member("value"), func(v delta.JsonValue) bool { return viewsJSON(t, v) == `{"points":4}` })
		_, err = state.Call("set", delta.JsonObjectOf("value", strings.Repeat("x", 300*1024)), p2test.Background)
		if err == nil || !strings.Contains(err.Error(), "at most") {
			t.Fatalf("an oversized state: %v", err)
		}
	})

	t.Run("serves a custom view's page with its bridge, sandboxed, to the session's viewers only", func(t *testing.T) {
		frame := func(version int, id string) string {
			return "/view-frame?" + url.Values{"session": {id}, "view": {"glyph"}, "version": {strconv.Itoa(version)}}.Encode()
		}
		page, err := f.Fixture.HTTP(frame(2, f.Session), p2test.HTTPOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if page.Status != 200 {
			t.Fatalf("status = %d", page.Status)
		}
		matches(t, page.Body, `window\.p2view`)
		matches(t, page.Body, `glyph v2`)
		csp := page.Headers.Get("Content-Security-Policy")
		matches(t, csp, `sandbox allow-scripts allow-modals allow-pointer-lock`)
		if strings.Contains(csp, "allow-same-origin") {
			t.Fatalf("csp = %s", csp)
		}
		for _, check := range []struct {
			path    string
			headers map[string]string
			status  int
		}{
			{frame(3, f.Session), nil, 404},
			{frame(1, f.Session), map[string]string{"Sec-Fetch-Site": "cross-site"}, 403},
		} {
			result, err := f.Fixture.HTTP(check.path, p2test.HTTPOptions{Headers: check.headers})
			if err != nil || result.Status != check.status {
				t.Fatalf("%s = %d %v, want %d", check.path, result.Status, err, check.status)
			}
		}
		sessions, err := p2test.HoldIn(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
		if err != nil {
			t.Fatal(err)
		}
		created, err := sessions.Call("create", delta.JsonObjectOf("cwd", f.Fixture.Workspace), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		if result, err := f.Fixture.HTTP(frame(1, object(created).Value("id").(string)), p2test.HTTPOptions{}); err != nil || result.Status != 404 {
			t.Fatalf("another session's view = %d %v", result.Status, err)
		}
	})

	t.Run("saves views to the owner's library and opens them in another session", func(t *testing.T) {
		matches(t, tool(map[string]any{"action": "save", "id": "glyph", "name": "glyph-editor", "description": "Edits glyphs"}), `Saved`)
		library, err := p2test.HoldIn(owner, viewscontract.SavedViews, delta.NewJsonObject(0), "owner")
		if err != nil {
			t.Fatal(err)
		}
		named := func(want bool) func(delta.JsonValue) bool {
			return func(v delta.JsonValue) bool {
				found := false
				for _, each := range list(v) {
					found = found || object(each).Value("name") == "glyph-editor"
				}
				return found == want
			}
		}
		f.until(library.Member("saved"), named(true))
		sessions, err := p2test.HoldIn(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
		if err != nil {
			t.Fatal(err)
		}
		created, err := sessions.Call("create", delta.JsonObjectOf("cwd", f.Fixture.Workspace), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		other := object(created).Value("id").(string)
		otherChat, err := p2test.HoldIn(owner, chatcontract.Chat, delta.JsonObjectOf("session", other, "conversation", float64(1)), "participant")
		if err != nil {
			t.Fatal(err)
		}
		defer otherChat.Release()
		matches(t, tool(map[string]any{"action": "load", "name": "glyph-editor"}, otherChat), `Opened view "glyph-editor"`)
		state, err := p2test.HoldIn(owner, viewscontract.ViewState, delta.JsonObjectOf("session", other, "view", "glyph-editor"), "editor")
		if err != nil {
			t.Fatal(err)
		}
		defer state.Release()
		f.until(state.Member("value"), func(v delta.JsonValue) bool { return viewsJSON(t, v) == `{"points":4}` })
		// From the Views sheet too, under a free ID.
		otherViews, err := p2test.HoldIn(owner, viewscontract.Views, delta.JsonObjectOf("session", other), "editor")
		if err != nil {
			t.Fatal(err)
		}
		opened, err := otherViews.Call("open", delta.JsonObjectOf("saved", "glyph-editor"), p2test.Background)
		if err != nil || object(opened).Value("id") != "glyph-editor-2" {
			t.Fatalf("open saved = %v %v", opened, err)
		}
		if _, err := library.Call("remove", delta.JsonObjectOf("name", "glyph-editor"), p2test.Background); err != nil {
			t.Fatal(err)
		}
		f.until(library.Member("saved"), named(false))
		matches(t, tool(map[string]any{"action": "saved"}, otherChat), `No saved views`)
	})
}
