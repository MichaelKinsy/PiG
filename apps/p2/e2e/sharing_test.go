// Ports test/p2/sharing.test.ts (Pi main c23b9ec1): links, joining as a guest over HTTP and a cookie, what guests may reach,
// per-author drafts, live revocation, and the share store across a server restart. The guests are real joins: a POST /join
// on the tunnel Host, then a WebSocket with the guest cookie.
//
// The app (`/`, `/s/<id>`, the web bundle's files) is served by this server (hosts/web.go): both halves of "serves guests the
// app, never other sessions, sources or data" are here (what a guest may load, and what its HTTP requests are refused with).

package e2e_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	draftscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/drafts/contract"
	modelscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	sharingcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sharing/contract"
	statuscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/gorilla/websocket"
	_ "modernc.org/sqlite"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/sharing"
)

var tunnelHeaders = map[string]string{"Host": "share.test", "Origin": "https://share.test"}

type guest struct {
	host   host.Host
	conn   *websocket.Conn
	cookie string
	id     string
	name   string
}

func list(value delta.JsonValue) []any {
	items, _ := value.([]any)
	return items
}

func TestSharingFakeProvider(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{AllowedHosts: []string{"share.test"}})
	owner := fixture.Browser()
	sessions, err := p2test.HoldIn(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	create := func(title string) string {
		created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace, "title", title), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		return object(created).Value("id").(string)
	}
	session, other := create("Shared"), create("Private")
	sharing, err := p2test.HoldIn(owner, sharingcontract.Sharing, delta.JsonObjectOf("session", session), "owner")
	if err != nil {
		t.Fatal(err)
	}
	until := func(t *testing.T, source p2test.JSONSource, predicate func(delta.JsonValue) bool, timeout ...time.Duration) delta.JsonValue {
		t.Helper()
		var limit time.Duration
		if len(timeout) > 0 {
			limit = timeout[0]
		}
		value, err := p2test.Until(source, predicate, limit)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	tunnel := func(path, cookie, method, body string) p2test.HTTPResult {
		t.Helper()
		headers := map[string]string{"Content-Type": "application/json"}
		for name, value := range tunnelHeaders {
			headers[name] = value
		}
		if cookie != "" {
			headers["Cookie"] = cookie
		}
		options := p2test.HTTPOptions{Method: method, Headers: headers}
		if body != "" {
			options.Body = strings.NewReader(body)
		}
		result, err := p2test.HTTP(fixture.Server.Port, path, options)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	tokens := map[string]string{}
	linkOf := func(t *testing.T, mode string, reset bool) delta.JsonValue {
		t.Helper()
		if _, err := sharing.Call("link", delta.JsonObjectOf("mode", mode, "reset", reset), p2test.Background); err != nil {
			t.Fatal(err)
		}
		// The answer follows its state: the (new) link is on the page when the call resolves.
		links, err := sharing.Member("links").Value()
		if err != nil {
			t.Fatal(err)
		}
		for _, each := range list(links) {
			if object(each).Value("mode") == mode {
				if reset && object(each).Value("token") == tokens[mode] {
					t.Fatal("a reset gives a new token")
				}
				tokens[mode] = object(each).Value("token").(string)
				return each
			}
		}
		t.Fatalf("no %s link in %v", mode, links)
		return nil
	}
	setCookie := regexp.MustCompile(`HttpOnly; SameSite=Strict; Path=/; Max-Age=\d+; Secure`)
	// join joins through token with name; it connects a client with the guest cookie.
	join := func(t *testing.T, name, token string) *guest {
		t.Helper()
		response := tunnel("/join", "", http.MethodPost, `{"token":"`+token+`","name":"`+name+`"}`)
		if response.Status != 200 {
			t.Fatalf("join: %d %s", response.Status, response.Body)
		}
		header := response.Headers.Get("Set-Cookie")
		if !setCookie.MatchString(header) {
			t.Fatalf("Set-Cookie = %q", header)
		}
		cookie, _, _ := strings.Cut(header, ";")
		headers := map[string]string{"Cookie": cookie}
		for name, value := range tunnelHeaders {
			headers[name] = value
		}
		client, conn := fixture.Client(headers)
		access, err := p2test.HoldIn(client, p2runtime.Access, delta.NewJsonObject(0), "guest")
		if err != nil {
			t.Fatal(err)
		}
		grant := object(until(t, access.Member("grant"), func(value delta.JsonValue) bool { return value != nil }))
		if grant.Value("session") != session {
			t.Fatalf("grant session = %v, want %s", grant.Value("session"), session)
		}
		// The web host holds GuestPresence for the page's lifetime.
		presence, err := client.Hold(p2test.Background, sharingcontract.GuestPresence, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := p2runtime.WhenLive(p2test.Background, presence); err != nil {
			t.Fatal(err)
		}
		return &guest{host: client, conn: conn, cookie: cookie, id: grant.Value("id").(string), name: grant.Value("name").(string)}
	}
	chatOf := func(h host.Host, variant, id string) (*host.Reference, error) {
		return p2test.HoldIn(h, chatcontract.Chat, delta.JsonObjectOf("session", id, "conversation", float64(1)), variant)
	}
	mustChat := func(t *testing.T, h host.Host, variant string) *host.Reference {
		t.Helper()
		chat, err := chatOf(h, variant, session)
		if err != nil {
			t.Fatal(err)
		}
		return chat
	}
	send := func(t *testing.T, chat *host.Reference, text, requestID string, extra ...any) delta.JsonValue {
		t.Helper()
		result, err := chat.Call("send", delta.JsonObjectOf(append([]any{"text", text, "requestId", requestID}, extra...)...), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	users := func(view delta.JsonValue) []*delta.JsonObject {
		var items []*delta.JsonObject
		for _, each := range list(object(view).Value("entries")) {
			if entry := object(each); entry.Value("kind") == "p2.input" {
				items = append(items, object(entry.Value("data")))
			}
		}
		return items
	}
	guestsOf := func(count int) func(delta.JsonValue) bool {
		return func(value delta.JsonValue) bool { return len(list(value)) == count }
	}
	var alice, bob *guest

	t.Run("creates one link per mode and serves its join page", func(t *testing.T) {
		until(t, sharing.Member("links"), func(value delta.JsonValue) bool { return value != nil && len(list(value)) == 0 })
		view := linkOf(t, "view", false)
		token := object(view).Value("token").(string)
		if url := object(view).Value("url"); url != "https://share.test/join/"+token {
			t.Fatalf("url = %v", url)
		}
		if again := linkOf(t, "view", false); object(again).Value("token") != token {
			t.Fatal("copying again reuses the link")
		}
		page := tunnel("/join/"+token, "", http.MethodGet, "")
		if page.Status != 200 || !strings.Contains(page.Body, "Shared") {
			t.Fatalf("join page: %d %q", page.Status, page.Body)
		}
		if !strings.Contains(page.Headers.Get("Content-Security-Policy"), "script-src 'nonce-") {
			t.Fatalf("CSP = %q", page.Headers.Get("Content-Security-Policy"))
		}
		// A cookieless loopback browser joins (Pi Mobile); the owner's browser, with the loopback owner cookie it got loading
		// the app, goes straight to the session.
		loopback, err := p2test.HTTP(fixture.Server.Port, "/join/"+token, p2test.HTTPOptions{})
		if err != nil || loopback.Status != 200 {
			t.Fatalf("cookieless loopback join page: %v %+v", err, loopback)
		}
		app, err := p2test.HTTP(fixture.Server.Port, "/", p2test.HTTPOptions{})
		if err != nil {
			t.Fatal(err)
		}
		device, _, _ := strings.Cut(app.Headers.Get("Set-Cookie"), ";")
		if !regexp.MustCompile(`^p2_device_\d+=`).MatchString(device) {
			t.Fatalf("device cookie = %q", device)
		}
		ownerJoin, err := p2test.HTTP(fixture.Server.Port, "/join/"+token, p2test.HTTPOptions{Headers: map[string]string{"Cookie": device}})
		if err != nil || ownerJoin.Status != 303 || ownerJoin.Headers.Get("Location") != "/s/"+session {
			t.Fatalf("the owner goes to the session: %v %+v", err, ownerJoin)
		}
		if status := tunnel("/", "", http.MethodGet, "").Status; status != 401 {
			t.Fatalf("a tunnel request without a grant is not signed in: %d", status)
		}
		if status := tunnel("/join/"+strings.Repeat("x", 43), "", http.MethodGet, "").Status; status != 410 {
			t.Fatalf("an unknown token: %d", status)
		}
		crossSite, err := p2test.HTTP(fixture.Server.Port, "/join", p2test.HTTPOptions{Method: http.MethodPost, Headers: map[string]string{"Host": "share.test"}, Body: strings.NewReader(`{"token":"` + token + `","name":"x"}`)})
		if err != nil || crossSite.Status != 403 {
			t.Fatalf("joining needs a same-origin request: %v %+v", err, crossSite)
		}
		if status := tunnel("/join", "", http.MethodPost, `{"token":"`+token+`","name":" "}`).Status; status != 400 {
			t.Fatalf("a blank name: %d", status)
		}
	})

	t.Run("joins guests with their own identities", func(t *testing.T) {
		alice = join(t, "Alice", tokens["view"])
		bob = join(t, "Alice", tokens["view"])
		if alice.id == bob.id {
			t.Fatal("two browsers, one identity")
		}
		if bob.name != "Alice (2)" {
			t.Fatalf("name = %q", bob.name)
		}
		again := tunnel("/join", alice.cookie, http.MethodPost, `{"token":"`+tokens["view"]+`","name":"new"}`)
		if again.Status != 200 || again.Headers.Get("Set-Cookie") != "" {
			t.Fatalf("the same browser keeps its identity: %+v", again)
		}
		if status := tunnel("/join/"+tokens["view"], alice.cookie, http.MethodGet, "").Status; status != 303 {
			t.Fatalf("a joined guest goes straight to the session: %d", status)
		}
		guests := until(t, sharing.Member("guests"), guestsOf(2))
		for _, each := range list(guests) {
			if object(each).Value("mode") != "view" {
				t.Fatalf("guests = %v", guests)
			}
		}
	})

	t.Run("limits read-only guests to reading their session", func(t *testing.T) {
		listing, err := p2test.HoldIn(alice.host, sessionscontract.Sessions, delta.NewJsonObject(0), "guest")
		if err != nil {
			t.Fatal(err)
		}
		listed := until(t, listing.Member("sessions"), func(value delta.JsonValue) bool { return value != nil })
		if rows := list(listed); len(rows) != 1 || object(rows[0]).Value("id") != session {
			t.Fatalf("sessions = %v", listed)
		}
		statuses, err := p2test.HoldIn(alice.host, statuscontract.SessionStatus, delta.NewJsonObject(0), "reader")
		if err != nil {
			t.Fatal(err)
		}
		visible := until(t, statuses.Member("statuses"), func(value delta.JsonValue) bool { return value != nil })
		for _, id := range object(visible).Keys() {
			if id != session {
				t.Fatalf("a guest sees another session's status: %v", visible)
			}
		}
		models, err := alice.host.Hold(p2test.Background, modelscontract.Models, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := p2runtime.WhenLive(p2test.Background, models); err == nil || !regexp.MustCompile(`forbidden|owner-only`).MatchString(err.Error()) {
			t.Fatalf("Models for a guest: %v", err)
		}
		forbidden, err := alice.host.Hold(p2test.Background, sharingcontract.Sharing, delta.JsonObjectOf("session", session), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := p2runtime.WhenLive(p2test.Background, forbidden); err == nil || !strings.Contains(err.Error(), "owner-only") {
			t.Fatalf("Sharing for a guest: %v", err)
		}
		if _, err := chatOf(alice.host, "viewer", other); err == nil {
			t.Fatal("another session's Chat")
		} else {
			p2test.ExpectChord(t, err, data.CodeForbidden)
		}
		chat := mustChat(t, alice.host, "viewer")
		page, err := chat.Call("entries", delta.JsonObjectOf("limit", float64(10)), p2test.Background)
		if err != nil || object(page).Value("items") == nil {
			t.Fatalf("read-only guests page their session's entries: %v %v", page, err)
		}
		// `send` is a participant's: a viewer calling it anyway is refused.
		_, err = chat.Call("send", delta.JsonObjectOf("text", "x", "requestId", "view"), p2test.Background)
		p2test.ExpectChord(t, err, data.CodeInvalid)
		drafts, err := alice.host.Hold(p2test.Background, draftscontract.Drafts, delta.JsonObjectOf("session", session, "conversation", float64(1)), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := p2runtime.WhenLive(p2test.Background, drafts); err == nil {
			t.Fatal("Drafts for a viewer")
		}
	})

	// Pi's "serves guests the app, never other sessions, sources or data": the guest loads the app's pages of its session, polls
	// the manifest and fetches the current bundle's files, and is refused everything else.
	t.Run("serves guests the app, never other sessions, sources or data", func(t *testing.T) {
		for _, path := range []string{"/", "/s/" + session, "/s/" + session + "/c/2"} {
			if status := tunnel(path, alice.cookie, http.MethodGet, "").Status; status != 200 {
				t.Errorf("%s: %d, want 200", path, status)
			}
		}
		manifest := tunnel("/__p2_web", alice.cookie, http.MethodGet, "")
		var bundle struct {
			Script string  `json:"script"`
			Style  *string `json:"style"`
		}
		if manifest.Status != 200 || json.Unmarshal([]byte(manifest.Body), &bundle) != nil || bundle.Script == "" || bundle.Style == nil {
			t.Fatalf("manifest: %d %s", manifest.Status, manifest.Body)
		}
		for _, file := range []string{bundle.Script, *bundle.Style} {
			if status := tunnel(file, alice.cookie, http.MethodGet, "").Status; status != 200 {
				t.Errorf("%s: %d, want 200", file, status)
			}
		}
		// Another session's page is not the guest's.
		if status := tunnel("/s/"+other, alice.cookie, http.MethodGet, "").Status; status != 403 {
			t.Errorf("/s/%s: %d, want 403", other, status)
		}

		for _, path := range []string{
			"/__p2_web/app-UNKNOWN.js", "/src/hosts/server.ts", "/@fs" + fixture.DataDir + "/shares.sqlite",
			"/src/hosts/web/%2e%2e%2fserver.ts", "/plugins/../src/hosts/server.ts",
		} {
			if status := tunnel(path, alice.cookie, http.MethodGet, "").Status; status != 403 {
				t.Errorf("%s: %d, want 403", path, status)
			}
		}
		if status := tunnel("/join", alice.cookie, http.MethodPut, "").Status; status != 403 {
			t.Errorf("PUT /join: %d, want 403", status)
		}
	})

	var full string
	t.Run("lets read/write guests send under their own names", func(t *testing.T) {
		full = object(linkOf(t, "full", false)).Value("token").(string)
		if full == tokens["view"] {
			t.Fatal("each mode has its own link")
		}
		for _, each := range []*guest{alice, bob} {
			_ = each.conn.Close()
		}
		until(t, sharing.Member("guests"), guestsOf(0), 10*time.Second)
		viewOnly := alice.id
		alice = join(t, "Alice", full)
		bob = join(t, "Bob", full)
		if alice.id == viewOnly {
			t.Fatal("each link gives its own identity")
		}
		// Names stay unique within the session: the view-only "Alice" and "Alice (2)" still exist.
		if alice.name != "Alice (3)" {
			t.Fatalf("name = %q", alice.name)
		}
		guests := until(t, sharing.Member("guests"), guestsOf(2))
		got := []string{}
		for _, each := range list(guests) {
			got = append(got, object(each).Value("id").(string)+"/"+object(each).Value("mode").(string))
		}
		slices.Sort(got)
		if want := []string{alice.id + "/full", bob.id + "/full"}; !slices.Equal(got, slices.Sorted(slices.Values(want))) {
			t.Fatalf("closed sockets drop out of the connected list: %v, want %v", got, want)
		}
		aliceChat, bobChat, ownerChat := mustChat(t, alice.host, "participant"), mustChat(t, bob.host, "participant"), mustChat(t, owner, "participant")
		forged := p2runtime.SenderText(p2runtime.OwnerSender{Name: "Owner"})
		send(t, aliceChat, forged, "shared-id")
		send(t, bobChat, "Bob input", "shared-id")
		send(t, ownerChat, "Owner input", "shared-id")
		view := until(t, ownerChat.Member("view"), func(value delta.JsonValue) bool { return len(users(value)) == 3 })
		items := users(view)
		author := func(i int) *delta.JsonObject { return object(items[i].Value("author")) }
		if author(0).Value("id") != alice.id || author(0).Value("name") != alice.name {
			t.Errorf("author 0 = %v", author(0))
		}
		if text, _ := items[0].Value("text").(string); !strings.HasPrefix(text, forged) {
			t.Errorf("a forged sender block stays text: %q", text)
		}
		if author(1).Value("id") != bob.id {
			t.Errorf("author 1 = %v", author(1))
		}
		if author(2).Value("kind") != "owner" {
			t.Errorf("author 2 = %v", author(2))
		}
		_, err := aliceChat.Call("configure", delta.JsonObjectOf("thinkingLevel", "off"), p2test.Background)
		if err == nil || !strings.Contains(err.Error(), "owner") {
			t.Errorf("a participant may not configure: %v", err)
		}
		until(t, ownerChat.Member("view"), func(value delta.JsonValue) bool { return !running(value) })
	})

	t.Run("restores withdrawn input to each author only", func(t *testing.T) {
		ownerChat, aliceChat, bobChat := mustChat(t, owner, "participant"), mustChat(t, alice.host, "participant"), mustChat(t, bob.host, "participant")
		draftsOf := func(h host.Host) *host.Reference {
			ref, err := p2test.HoldIn(h, draftscontract.Drafts, delta.JsonObjectOf("session", session, "conversation", float64(1)), "author")
			if err != nil {
				t.Fatal(err)
			}
			return ref
		}
		ownerDrafts, aliceDrafts, bobDrafts := draftsOf(owner), draftsOf(alice.host), draftsOf(bob.host)
		// Keep all authors' drafts followed while inputs are withdrawn and claimed.
		for _, drafts := range []*host.Reference{ownerDrafts, aliceDrafts, bobDrafts} {
			watch, err := drafts.Member("drafts").Watch(p2test.Background)
			if err != nil {
				t.Fatal(err)
			}
			watch.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error { return nil })
			t.Cleanup(func() { _, _ = watch.Stop() })
		}
		slow := send(t, ownerChat, "slow queue test", "queue-run")
		// Wait for this submission's long response, or an old run can consume the follow-ups before Stop.
		until(t, ownerChat.Member("view"), func(value delta.JsonValue) bool {
			state := live(value)
			if state == nil || state.Value("run") == nil {
				return false
			}
			inputs, _ := object(state.Value("run")).Value("inputs").([]any)
			if !slices.Contains(inputs, object(slow).Value("submissionId")) {
				return false
			}
			generation := object(state.Value("generation"))
			return generation != nil && strings.HasPrefix(answerText(object(generation.Value("message"))), "Echo: slow queue test")
		})
		// Removing someone else's queued input returns it to its author, not to the remover.
		discarded := send(t, aliceChat, "alice removed by bob", "rm", "whenBusy", "followUp")
		removed, err := bobChat.Call("dequeue", delta.JsonObjectOf("submissionId", object(discarded).Value("submissionId"), "restore", true), p2test.Background)
		if err != nil || object(removed).Value("text") != nil {
			t.Fatalf("dequeue by bob: %v %v", removed, err)
		}
		aliceRemoved := list(until(t, aliceDrafts.Member("drafts"), func(value delta.JsonValue) bool { return len(list(value)) == 1 }))
		claimed, err := aliceDrafts.Call("claim", delta.JsonObjectOf("ids", []any{object(aliceRemoved[0]).Value("id")}), p2test.Background)
		if err != nil || !jsonEqual(object(claimed).Value("texts"), []any{"alice removed by bob"}) {
			t.Fatalf("claim: %v %v", claimed, err)
		}
		send(t, ownerChat, "owner queued", "oq")
		send(t, aliceChat, "alice queued", "aq", "whenBusy", "followUp")
		send(t, bobChat, "bob queued", "bq", "whenBusy", "followUp")
		stopped, err := ownerChat.Call("stop", nil, p2test.Background)
		if err != nil || !jsonEqual(object(stopped).Value("restored"), []any{"owner queued"}) {
			t.Fatalf("stop: %v %v", stopped, err)
		}
		for _, each := range []struct {
			drafts   *host.Reference
			expected string
		}{{aliceDrafts, "alice queued"}, {bobDrafts, "bob queued"}} {
			items := list(until(t, each.drafts.Member("drafts"), func(value delta.JsonValue) bool { return len(list(value)) > 0 }))
			if len(items) != 1 || object(items[0]).Value("text") != each.expected {
				t.Fatalf("drafts = %v, want [%s]", items, each.expected)
			}
			ids := []any{object(items[0]).Value("id")}
			first, err := each.drafts.Call("claim", delta.JsonObjectOf("ids", ids), p2test.Background)
			if err != nil || !jsonEqual(object(first).Value("texts"), []any{each.expected}) {
				t.Fatalf("claim: %v %v", first, err)
			}
			again, err := each.drafts.Call("claim", delta.JsonObjectOf("ids", ids), p2test.Background)
			if err != nil || !jsonEqual(object(again).Value("texts"), []any{}) {
				t.Fatalf("claimed once: %v %v", again, err)
			}
		}
		if drafts, err := ownerDrafts.Member("drafts").Value(); err != nil || !jsonEqual(drafts, []any{}) {
			t.Fatalf("the owner's drafts: %v %v", drafts, err)
		}
		until(t, ownerChat.Member("view"), func(value delta.JsonValue) bool { return !running(value) })
	})

	t.Run("revokes removed guests and reset links at once", func(t *testing.T) {
		mustChat(t, alice.host, "participant")
		if _, err := sharing.Call("remove", delta.JsonObjectOf("guestId", alice.id), p2test.Background); err != nil {
			t.Fatal(err)
		}
		if err := p2test.Poll(func() (bool, error) { return p2test.SocketClosed(alice.conn), nil }, 10*time.Second, "alice's socket closed"); err != nil {
			t.Fatal(err)
		}
		until(t, sharing.Member("guests"), guestsOf(1))
		if status := tunnel("/", alice.cookie, http.MethodGet, "").Status; status != 401 {
			t.Fatalf("a removed guest has no grant: %d", status)
		}
		rejoin := tunnel("/join", alice.cookie, http.MethodPost, `{"token":"`+full+`","name":"again"}`)
		if rejoin.Status != 400 || !strings.Contains(rejoin.Body, "removed") {
			t.Fatalf("a removed browser cannot mint a new identity with the same link: %+v", rejoin)
		}
		reset := linkOf(t, "full", true)
		if object(reset).Value("token") == full {
			t.Fatal("a reset replaces the token")
		}
		if err := p2test.Poll(func() (bool, error) { return p2test.SocketClosed(bob.conn), nil }, 10*time.Second, "bob's socket closed"); err != nil {
			t.Fatal(err)
		}
		until(t, sharing.Member("guests"), guestsOf(0))
		ended := tunnel("/join/"+full, "", http.MethodGet, "")
		if ended.Status != 410 || !strings.Contains(ended.Body, "This share has ended") {
			t.Fatalf("an ended share: %d %q", ended.Status, ended.Body)
		}
		if status := tunnel("/join/"+tokens["view"], "", http.MethodGet, "").Status; status != 200 {
			t.Fatalf("resetting one link leaves the other: %d", status)
		}
		if status := tunnel("/join/"+object(reset).Value("token").(string), "", http.MethodGet, "").Status; status != 200 {
			t.Fatalf("the new link: %d", status)
		}
	})

	t.Run("ends guest access while the session is archived, and restores it", func(t *testing.T) {
		carol := join(t, "Carol", tokens["view"])
		mustChat(t, carol.host, "viewer")
		if _, err := sessions.Call("archive", delta.JsonObjectOf("id", session), p2test.Background); err != nil {
			t.Fatal(err)
		}
		if err := p2test.Poll(func() (bool, error) { return p2test.SocketClosed(carol.conn), nil }, 10*time.Second, "carol's socket closed"); err != nil {
			t.Fatal(err)
		}
		if status := tunnel("/join/"+tokens["view"], "", http.MethodGet, "").Status; status != 410 {
			t.Fatalf("an archived session's link: %d", status)
		}
		if status := tunnel("/", carol.cookie, http.MethodGet, "").Status; status != 401 {
			t.Fatalf("an archived session's guest: %d", status)
		}
		if _, err := sessions.Call("restore", delta.JsonObjectOf("id", session), p2test.Background); err != nil {
			t.Fatal(err)
		}
		// Restore gives the grant back: the cookie authenticates again (the app page itself is the web lane's).
		headers := map[string]string{"Cookie": carol.cookie}
		for name, value := range tunnelHeaders {
			headers[name] = value
		}
		client, _ := fixture.Client(headers)
		mustChat(t, client, "viewer")
	})

	t.Run("gives a browser that joins another session's link both sessions", func(t *testing.T) {
		otherSharing, err := p2test.HoldIn(owner, sharingcontract.Sharing, delta.JsonObjectOf("session", other), "owner")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := otherSharing.Call("link", delta.JsonObjectOf("mode", "view"), p2test.Background); err != nil {
			t.Fatal(err)
		}
		links := list(until(t, otherSharing.Member("links"), func(value delta.JsonValue) bool { return len(list(value)) > 0 }))
		otherToken := object(links[0]).Value("token").(string)
		dave := join(t, "Dave", tokens["view"])
		again := tunnel("/join", dave.cookie, http.MethodPost, `{"token":"`+otherToken+`","name":"ignored"}`)
		if again.Status != 200 || again.Headers.Get("Set-Cookie") != "" {
			t.Fatalf("the browser keeps its identity: %+v", again)
		}
		headers := map[string]string{"Cookie": dave.cookie}
		for name, value := range tunnelHeaders {
			headers[name] = value
		}
		client, _ := fixture.Client(headers)
		access, err := p2test.HoldIn(client, p2runtime.Access, delta.NewJsonObject(0), "guest")
		if err != nil {
			t.Fatal(err)
		}
		grant := object(until(t, access.Member("grant"), func(value delta.JsonValue) bool { return value != nil }))
		if grant.Value("id") != dave.id {
			t.Fatalf("grant = %v", grant)
		}
		if modes := object(grant.Value("sessions")); modes.Len() != 2 || modes.Value(session) != "view" || modes.Value(other) != "view" {
			t.Fatalf("sessions = %v", modes)
		}
		if grant.Value("session") != other {
			t.Fatalf("it lands in the session it joined last: %v", grant.Value("session"))
		}
		listing, err := p2test.HoldIn(client, sessionscontract.Sessions, delta.NewJsonObject(0), "guest")
		if err != nil {
			t.Fatal(err)
		}
		listed := until(t, listing.Member("sessions"), func(value delta.JsonValue) bool { return len(list(value)) == 2 })
		ids := []string{}
		for _, row := range list(listed) {
			ids = append(ids, object(row).Value("id").(string))
		}
		slices.Sort(ids)
		if want := slices.Sorted(slices.Values([]string{session, other})); !slices.Equal(ids, want) {
			t.Fatalf("sessions = %v, want %v", ids, want)
		}
	})
}

func jsonEqual(a, b any) bool {
	left, _ := chordjson.Marshal(a)
	right, _ := chordjson.Marshal(b)
	return string(left) == string(right)
}

// revisionOf is the revision of the only guest in the share store.
func revisionOf(t *testing.T, dataDir string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "shares.sqlite")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var revision int
	if err := db.QueryRow("SELECT revision FROM guests").Scan(&revision); err != nil {
		t.Fatal(err)
	}
	return revision
}

// restart closes the fixture's server and starts one on the same data directory and port.
func restart(t *testing.T, fixture *p2test.Fixture) *hosts.P2Server {
	t.Helper()
	port, dataDir := fixture.Server.Port, fixture.DataDir
	if err := fixture.Server.Close(p2test.Background); err != nil {
		t.Fatal(err)
	}
	entry, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fake := true
	server, err := hosts.StartServer(hosts.ServerOptions{DataDir: dataDir, Port: &port, Fake: &fake, AllowedHosts: []string{"share.test"}, Entry: entry, ExecArgv: []string{hosts.WorkerArg}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(p2test.Background) })
	return server
}

func TestSharingAcrossRestartsDoesNotCountASessionArchivedBeforeTheRestartAsNewlyArchived(t *testing.T) {
	fixture := p2test.StartFixture(t, hosts.ServerOptions{AllowedHosts: []string{"share.test"}})
	owner := fixture.Browser()
	sessions, err := p2test.HoldIn(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace, "title", "Archived"), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	session := object(created).Value("id").(string)
	sharing, err := p2test.HoldIn(owner, sharingcontract.Sharing, delta.JsonObjectOf("session", session), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sharing.Call("link", delta.JsonObjectOf("mode", "full"), p2test.Background); err != nil {
		t.Fatal(err)
	}
	links, err := p2test.Until(sharing.Member("links"), func(value delta.JsonValue) bool { return len(list(value)) > 0 }, 0)
	if err != nil {
		t.Fatal(err)
	}
	token := object(list(links)[0]).Value("token").(string)
	headers := map[string]string{"Content-Type": "application/json"}
	for name, value := range tunnelHeaders {
		headers[name] = value
	}
	joined, err := p2test.HTTP(fixture.Server.Port, "/join", p2test.HTTPOptions{Method: http.MethodPost, Headers: headers, Body: strings.NewReader(`{"token":"` + token + `","name":"Archived guest"}`)})
	if err != nil || joined.Status != 200 {
		t.Fatalf("join: %v %+v", err, joined)
	}
	if _, err := sessions.Call("archive", delta.JsonObjectOf("id", session), p2test.Background); err != nil {
		t.Fatal(err)
	}
	if err := p2test.Poll(func() (bool, error) { return revisionOf(t, fixture.DataDir) == 2, nil }, 10*time.Second, "archiving bumped the grant"); err != nil {
		t.Fatal(err)
	}
	restarted := restart(t, fixture)
	// The archive listener's first value is the stored catalog, where the session is already archived: it bumps nothing at
	// startup. Only the restore bumps the grant.
	conn, err := p2test.Dial(restarted.Port, nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := p2test.ClientHost(conn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(p2test.Background) })
	again, err := p2test.HoldIn(client, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := again.Call("restore", delta.JsonObjectOf("id", session), p2test.Background); err != nil {
		t.Fatal(err)
	}
	if err := p2test.Poll(func() (bool, error) { return revisionOf(t, fixture.DataDir) == 3, nil }, 10*time.Second, "restoring bumped the grant once"); err != nil {
		t.Fatal(err)
	}
	// Once more, after the restore's own work settled: no later bump.
	if err := p2test.Poll(func() (bool, error) { return revisionOf(t, fixture.DataDir) == 3, nil }, 100*time.Millisecond, "the revision stays"); err != nil {
		t.Fatal(err)
	}
}

func TestSharingAcrossRestartsKeepsLinksAndGuestIdentities(t *testing.T) {
	fixture := p2test.StartFixture(t, hosts.ServerOptions{AllowedHosts: []string{"share.test"}})
	owner := fixture.Browser()
	sessions, err := p2test.HoldIn(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace, "title", "Kept"), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	session := object(created).Value("id").(string)
	sharing, err := p2test.HoldIn(owner, sharingcontract.Sharing, delta.JsonObjectOf("session", session), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sharing.Call("link", delta.JsonObjectOf("mode", "full"), p2test.Background); err != nil {
		t.Fatal(err)
	}
	links, err := p2test.Until(sharing.Member("links"), func(value delta.JsonValue) bool { return len(list(value)) > 0 }, 0)
	if err != nil {
		t.Fatal(err)
	}
	token := object(list(links)[0]).Value("token").(string)
	headers := map[string]string{"Content-Type": "application/json"}
	for name, value := range tunnelHeaders {
		headers[name] = value
	}
	joined, err := p2test.HTTP(fixture.Server.Port, "/join", p2test.HTTPOptions{Method: http.MethodPost, Headers: headers, Body: strings.NewReader(`{"token":"` + token + `","name":"Persistent"}`)})
	if err != nil || joined.Status != 200 {
		t.Fatalf("join: %v %+v", err, joined)
	}
	cookie, _, _ := strings.Cut(joined.Headers.Get("Set-Cookie"), ";")
	restarted := restart(t, fixture)
	guestHeaders := map[string]string{"Cookie": cookie}
	for name, value := range tunnelHeaders {
		guestHeaders[name] = value
	}
	conn, err := p2test.Dial(restarted.Port, guestHeaders)
	if err != nil {
		t.Fatal(err)
	}
	client, err := p2test.ClientHost(conn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(p2test.Background) })
	chat, err := p2test.HoldIn(client, chatcontract.Chat, delta.JsonObjectOf("session", session, "conversation", float64(1)), "participant")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Call("send", delta.JsonObjectOf("text", "survived restart", "requestId", "r"), p2test.Background); err != nil {
		t.Fatal(err)
	}
	if _, err := p2test.Until(chat.Member("view"), func(view delta.JsonValue) bool {
		encoded, _ := chordjson.Marshal(view)
		return strings.Contains(string(encoded), "Echo: survived restart")
	}, 0); err != nil {
		t.Fatal(err)
	}
}
