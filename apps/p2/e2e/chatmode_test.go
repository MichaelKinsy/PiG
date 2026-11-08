// Ports test/p2/chat-mode-fixture.test.ts and test/p2/chat-mode-reload.test.ts (Pi main c23b9ec1): chat mode in a real server
// with a plugin subset: what the fake provider receives (P2_FAKE_REQUESTS), mentions with their digest and attachments, a chat
// message during a run, guests, and the switch and the agent's name across a compatible reload of the plugin. Guests join
// through a loopback server with an allowed tunnel Host (share.test).

package e2e_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/chat-mode/contract"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	sharingcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sharing/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat-mode"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/search"
)

var chatModePlugins = []string{"chat", "chat-mode", "conversations", "drafts", "envs", "files", "models", "search", "sessions", "sharing", "status", "subagents"}

func TestChatModeServerSharingFakeProvider(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	scratch := t.TempDir()
	requestLog := filepath.Join(scratch, "requests.jsonl")
	fixture := p2test.StartFixture(t, hosts.ServerOptions{
		Plugins: chatModePlugins, AllowedHosts: []string{"share.test"}, WorkerEnv: map[string]string{"P2_FAKE_REQUESTS": requestLog},
	})
	owner := fixture.Browser()
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(processLogText())
		}
	})
	until := func(t *testing.T, source p2test.JSONSource, predicate func(delta.JsonValue) bool) delta.JsonValue {
		t.Helper()
		value, err := p2test.Until(source, predicate, 30*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	sessions, err := p2test.HoldIn(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace, "title", "Chat"), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	session := object(created).Value("id").(string)
	chat, err := p2test.HoldIn(owner, chatcontract.Chat, delta.JsonObjectOf("session", session, "conversation", float64(1)), "participant")
	if err != nil {
		t.Fatal(err)
	}
	mode, err := p2test.HoldIn(owner, contract.ChatMode, delta.JsonObjectOf("session", session, "conversation", float64(1)), "owner")
	if err != nil {
		t.Fatal(err)
	}
	// Stay subscribed: an unsubscribed remote member restarts from undefined (requests #8).
	keep, err := chat.Member("view").Watch(p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	keep.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error { return nil })
	_ = keep

	// requests is every request the fake provider received so far: one list of messages per request.
	requests := func() [][]map[string]any {
		file, err := os.Open(requestLog)
		if err != nil {
			return nil
		}
		defer file.Close()
		var all [][]map[string]any
		scanner := bufio.NewScanner(file)
		scanner.Buffer(nil, 1<<26)
		for scanner.Scan() {
			var messages []map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &messages); err != nil {
				t.Fatal(err)
			}
			all = append(all, messages)
		}
		return all
	}
	upload := func(name string, body []byte, headers map[string]string) string {
		t.Helper()
		all := map[string]string{"Origin": fmt.Sprintf("http://127.0.0.1:%d", fixture.Server.Port)}
		for key, value := range headers {
			all[key] = value
		}
		response, err := p2test.HTTP(fixture.Server.Port, "/upload?"+url.Values{"session": {session}, "name": {name}}.Encode(), p2test.HTTPOptions{
			Method: http.MethodPost, Headers: all, Body: strings.NewReader(string(body)),
		})
		if err != nil || response.Status != 200 {
			t.Fatalf("upload: %v %d %s", err, response.Status, response.Body)
		}
		var parsed struct{ ID string }
		if err := json.Unmarshal([]byte(response.Body), &parsed); err != nil {
			t.Fatal(err)
		}
		return parsed.ID
	}
	tunnel := map[string]string{"Host": "share.test", "Origin": "https://share.test"}
	// joinAs joins through the session's link of the mode as name: a WebSocket client carrying the guest cookie.
	joinAs := func(linkMode, name string) (host.Host, string, string) {
		t.Helper()
		sharing, err := p2test.HoldIn(owner, sharingcontract.Sharing, delta.JsonObjectOf("session", session), "owner")
		if err != nil {
			t.Fatal(err)
		}
		defer sharing.Release()
		if _, err := sharing.Call("link", delta.JsonObjectOf("mode", linkMode), p2test.Background); err != nil {
			t.Fatal(err)
		}
		var token string
		until(t, sharing.Member("links"), func(value delta.JsonValue) bool {
			links, _ := value.([]any)
			for _, link := range links {
				if object(link).Value("mode") == linkMode {
					token, _ = object(link).Value("token").(string)
					return true
				}
			}
			return false
		})
		body, _ := json.Marshal(map[string]any{"token": token, "name": name})
		headers := map[string]string{"Content-Type": "application/json"}
		for key, value := range tunnel {
			headers[key] = value
		}
		response, err := p2test.HTTP(fixture.Server.Port, "/join", p2test.HTTPOptions{Method: http.MethodPost, Headers: headers, Body: strings.NewReader(string(body))})
		if err != nil || response.Status != 200 {
			t.Fatalf("join: %v %d %s", err, response.Status, response.Body)
		}
		cookie := strings.SplitN(response.Headers.Get("Set-Cookie"), ";", 2)[0]
		guestHeaders := map[string]string{"Cookie": cookie}
		for key, value := range tunnel {
			guestHeaders[key] = value
		}
		guest, _ := fixture.Client(guestHeaders)
		access, err := p2test.HoldIn(guest, p2runtime.Access, delta.NewJsonObject(0), "guest")
		if err != nil {
			t.Fatal(err)
		}
		grant := object(until(t, access.Member("grant"), func(value delta.JsonValue) bool { return value != nil }))
		return guest, cookie, grant.Value("id").(string)
	}
	chatEntryList := func(view delta.JsonValue) []*delta.JsonObject { return entriesOfKindE2E(view, "chat.message") }
	idle := func() delta.JsonValue {
		return until(t, chat.Member("view"), func(view delta.JsonValue) bool {
			if view == nil || runOf(view) != nil {
				return false
			}
			tools, _ := object(object(object(view).Value("docs")).Value("pi.live")).Value("tools").([]any)
			return len(tools) == 0
		})
	}

	t.Run("loads exactly the named plugins", func(t *testing.T) {
		if !slices.Contains(fixture.Server.Plugins, "chat-mode") || slices.Contains(fixture.Server.Plugins, "terminals") {
			t.Fatalf("plugins %v", fixture.Server.Plugins)
		}
	})

	t.Run("keeps chat messages out of every model request, idle or during a run", func(t *testing.T) {
		if variant, _, _ := mode.Variant(); variant != "owner" {
			t.Fatalf("variant %q", variant)
		}
		setMode := func(enabled bool) {
			if _, err := mode.Call("set", delta.JsonObjectOf("enabled", enabled), p2test.Background); err != nil {
				t.Fatal(err)
			}
			until(t, mode.Member("enabled"), func(value delta.JsonValue) bool { return value == enabled })
		}
		setMode(true)
		if _, err := chat.Call("send", delta.JsonObjectOf("text", "hello people", "requestId", "c1"), p2test.Background); err != nil {
			t.Fatal(err)
		}
		entry := chatEntryList(until(t, chat.Member("view"), func(view delta.JsonValue) bool { return len(chatEntryList(view)) == 1 }))[0]
		if jsonText(entry.Value("data")) != `{"author":{"kind":"owner","id":"owner","name":"Owner"},"text":"hello people","attachments":[]}` {
			t.Fatalf("data %s", jsonText(entry.Value("data")))
		}
		if entry.Value("model") != nil {
			t.Fatal("a chat entry has a model message")
		}
		if got := requests(); len(got) != 0 {
			t.Fatalf("something was sent to the model: %v", got)
		}
		// Chat mode off: an ordinary input. It starts a run whose tool sleeps; chat mode goes on meanwhile.
		setMode(false)
		if _, err := chat.Call("send", delta.JsonObjectOf("text", "slow-tool", "requestId", "run"), p2test.Background); err != nil {
			t.Fatal(err)
		}
		until(t, chat.Member("view"), func(view delta.JsonValue) bool {
			tools, _ := object(object(object(view).Value("docs")).Value("pi.live")).Value("tools").([]any)
			return len(tools) > 0
		})
		setMode(true)
		if _, err := chat.Call("send", delta.JsonObjectOf("text", "while the tool runs", "requestId", "c2"), p2test.Background); err != nil {
			t.Fatal(err)
		}
		// A data-only write is placed at once, even while the tool runs.
		during := until(t, chat.Member("view"), func(view delta.JsonValue) bool { return len(chatEntryList(view)) == 2 })
		if runOf(during) == nil {
			t.Fatal("not placed during the run")
		}
		until(t, mode.Member("waiting"), func(value delta.JsonValue) bool { return value == float64(2) })
		view := idle()
		all := jsonText(requests())
		if strings.Contains(all, "hello people") || strings.Contains(all, "while the tool runs") {
			t.Fatalf("chat reached the model: %s", all)
		}
		// The run went on as if nobody had chatted: the tool result, then the usual echo.
		var roles []string
		for _, request := range requests() {
			roles = append(roles, fmt.Sprint(request[len(request)-1]["role"]))
		}
		if !slices.Equal(roles, []string{"user", "toolResult"}) {
			t.Fatalf("roles %v", roles)
		}
		entries, _ := object(view).Value("entries").([]any)
		if !strings.Contains(jsonText(object(entries[len(entries)-1]).Value("model")), "Echo: slow-tool") &&
			!strings.Contains(jsonText(object(entries[len(entries)-2]).Value("model")), "Echo: slow-tool") {
			t.Fatal("the usual echo is missing")
		}
	})

	t.Run("gives a mention the chat no mention passed on yet, with authors, attachments and capped images", func(t *testing.T) {
		png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
		alice, aliceCookie, aliceID := joinAs("full", "Alice")
		aliceChat, err := p2test.HoldIn(alice, chatcontract.Chat, delta.JsonObjectOf("session", session, "conversation", float64(1)), "participant")
		if err != nil {
			t.Fatal(err)
		}
		aliceMode, err := p2test.HoldIn(alice, contract.ChatMode, delta.JsonObjectOf("session", session, "conversation", float64(1)), "reader")
		if err != nil {
			t.Fatal(err)
		}
		// `set` is the owner's: a reader calling it anyway is refused.
		_, err = aliceMode.Call("set", delta.JsonObjectOf("enabled", false), p2test.Background)
		p2test.ExpectChord(t, err, data.CodeInvalid)

		guestUpload := func(name string) string {
			headers := map[string]string{"Cookie": aliceCookie}
			for key, value := range tunnel {
				headers[key] = value
			}
			return upload(name, png, headers)
		}
		images := []any{guestUpload("a.png"), guestUpload("b.png"), guestUpload("c.png")}
		if _, err := aliceChat.Call("send", delta.JsonObjectOf("text", "screenshots", "requestId", "a1", "attachments", images), p2test.Background); err != nil {
			t.Fatal(err)
		}
		notes := upload("notes.txt", []byte("remember the milk"), nil)
		more := []any{upload("d.png", png, nil), upload("e.png", png, nil)}
		if _, err := chat.Call("send", delta.JsonObjectOf("text", "my notes", "requestId", "o1", "attachments", append([]any{notes}, more...)), p2test.Background); err != nil {
			t.Fatal(err)
		}
		until(t, chat.Member("view"), func(view delta.JsonValue) bool { return len(chatEntryList(view)) == 4 })
		before := len(requests())

		// A client cannot choose its author: the arguments are closed, so the extra field is refused; the principal decides who
		// wrote what is sent.
		_, err = aliceChat.Call("send", delta.JsonObjectOf("text", "@Pi, what do you think?", "requestId", "m0",
			"author", delta.JsonObjectOf("kind", "owner", "id", "owner", "name", "Owner")), p2test.Background)
		p2test.ExpectChord(t, err, data.CodeInvalid)
		if _, err := aliceChat.Call("send", delta.JsonObjectOf("text", "@Pi, what do you think?", "requestId", "m1"), p2test.Background); err != nil {
			t.Fatal(err)
		}
		if err := p2test.Poll(func() (bool, error) { return len(requests()) > before, nil }, 20*time.Second, "the mention's request"); err != nil {
			t.Fatal(err)
		}
		request := requests()[before]
		asked := request[len(request)-1]
		if asked["role"] != "user" {
			t.Fatalf("role %v", asked["role"])
		}
		var text strings.Builder
		inlined := 0
		switch content := asked["content"].(type) {
		case string:
			text.WriteString(content)
		case []any:
			var parts []string
			for _, part := range content {
				block, _ := part.(map[string]any)
				switch block["type"] {
				case "text":
					parts = append(parts, fmt.Sprint(block["text"]))
				case "image":
					inlined++
				}
			}
			text.WriteString(strings.Join(parts, "\n"))
		}
		for _, pattern := range []string{
			`<sender kind="guest" id="[^"]+" name="Alice"/>`,
			`(?m)^Alice \(guest\): screenshots \[attached: a\.png, b\.png, c\.png\]$`,
			`(?m)^Owner: my notes \[attached: notes\.txt, d\.png, e\.png\]$`,
			`Alice \(guest\) mentioned you:\n@Pi, what do you think\?`,
			`(?m)^Owner: hello people$`,
		} {
			if !regexp.MustCompile(pattern).MatchString(text.String()) {
				t.Fatalf("%q does not match %s", text.String(), pattern)
			}
		}
		for _, name := range []string{"a.png", "b.png", "c.png", "notes.txt", "d.png", "e.png"} {
			if !strings.Contains(text.String(), "("+name+",") {
				t.Fatalf("%s is not listed by path in %q", name, text.String())
			}
		}
		if inlined != 4 {
			t.Fatalf("%d images inline, want the newest 4", inlined)
		}
		// People see the mention from its entry's data: Alice's own words, and which chat went with it.
		view := idle()
		placed := entriesOfKindE2E(view, "chat.mention")[0]
		placedData := object(placed.Value("data"))
		if jsonText(placedData.Value("author")) != fmt.Sprintf(`{"kind":"guest","id":"%s","name":"Alice"}`, aliceID) || placedData.Value("text") != "@Pi, what do you think?" {
			t.Fatalf("mention data %s", jsonText(placedData))
		}
		if included, _ := placedData.Value("included").([]any); len(included) != 4 {
			t.Fatalf("included %v", included)
		}
	})

	t.Run("lets read-only guests read chat mode but not write", func(t *testing.T) {
		bob, _, _ := joinAs("view", "Bob")
		bobChat, err := p2test.HoldIn(bob, chatcontract.Chat, delta.JsonObjectOf("session", session, "conversation", float64(1)), "viewer")
		if err != nil {
			t.Fatal(err)
		}
		bobMode, err := p2test.HoldIn(bob, contract.ChatMode, delta.JsonObjectOf("session", session, "conversation", float64(1)), "reader")
		if err != nil {
			t.Fatal(err)
		}
		until(t, bobMode.Member("enabled"), func(value delta.JsonValue) bool { return value == true })
		// `send` is a participant's: a viewer calling it anyway is refused.
		_, err = bobChat.Call("send", delta.JsonObjectOf("text", "hi", "requestId", "b1"), p2test.Background)
		p2test.ExpectChord(t, err, data.CodeInvalid)
	})
}

func entriesOfKindE2E(view delta.JsonValue, kind string) []*delta.JsonObject {
	var out []*delta.JsonObject
	entries, _ := object(view).Value("entries").([]any)
	for _, entry := range entries {
		if object(entry).Value("kind") == kind {
			out = append(out, object(entry))
		}
	}
	return out
}

// TestChatModeReloadKeepsTheModeAndItsName ports chat-mode-reload.test.ts: the switch and the agent's name stay across a
// compatible reload of the plugin (they live in a Durable document), the held reference stays live, and the reloaded code
// serves it.
func TestChatModeReloadKeepsTheModeAndItsName(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{Plugins: chatModePlugins})
	browser := fixture.Browser()
	sessions, err := p2test.HoldIn(browser, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	mode, err := p2test.HoldIn(browser, contract.ChatMode, delta.JsonObjectOf("session", object(created).Value("id"), "conversation", float64(1)), "owner")
	if err != nil {
		t.Fatal(err)
	}
	until := func(source p2test.JSONSource, predicate func(delta.JsonValue) bool) {
		t.Helper()
		if _, err := p2test.Until(source, predicate, 30*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := mode.Call("set", delta.JsonObjectOf("enabled", true), p2test.Background); err != nil {
		t.Fatal(err)
	}
	if _, err := mode.Call("rename", delta.JsonObjectOf("name", "Claude"), p2test.Background); err != nil {
		t.Fatal(err)
	}
	until(mode.Member("name"), func(value delta.JsonValue) bool { return value == "Claude" })
	// Pi asserts the reload succeeds (its server step always lists the host); an unchanged reload is a no-op in both.
	if _, err := fixture.Server.Reload([]string{"chat-mode"}, p2test.Background); err != nil {
		t.Fatal(err)
	}
	// The same reference follows the reloaded object: the state is the document's, and it still takes changes.
	until(mode.Member("enabled"), func(value delta.JsonValue) bool { return value == true })
	if name, _ := mode.Member("name").Value(); name != "Claude" {
		t.Fatalf("name %v", name)
	}
	if _, err := mode.Call("set", delta.JsonObjectOf("enabled", false), p2test.Background); err != nil {
		t.Fatal(err)
	}
	until(mode.Member("enabled"), func(value delta.JsonValue) bool { return value == false })
}
