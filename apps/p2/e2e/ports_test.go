// Ports test/p2/ports.test.ts (Pi main c23b9ec1), its third case: "ports tool defaults private, lists public endpoints,
// fences guests and closes on archive". The store's own cases (the first two) are plugins/ports/contract/store_test.go.

package e2e_test

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	portscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/ports/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	sharingcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sharing/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/ports"
)

const portsOrigin = "https://0123456789abcdef0123456789abcdef.rdtun.com"

func TestPortsToolDefaultsPrivateListsPublicEndpointsFencesGuestsAndClosesOnArchive(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	var rebuilds p2test.Rebuilds
	fixture := p2test.StartFixture(t, hosts.ServerOptions{AllowedHosts: []string{"share.test"}, BuildRevision: rebuilds.Revision})
	owner := fixture.Browser()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	until := func(source p2test.JSONSource, predicate func(delta.JsonValue) bool) delta.JsonValue {
		t.Helper()
		value, err := p2test.Until(source, predicate, 0)
		must(err)
		return value
	}
	sessions, err := p2test.HoldIn(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	must(err)
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace, "title", "Web app"), p2test.Background)
	must(err)
	session := object(created).Value("id").(string)
	chatOf := func(h host.Host, id string) *host.Reference {
		t.Helper()
		chat, err := p2test.HoldIn(h, chatcontract.Chat, delta.JsonObjectOf("session", id, "conversation", float64(1)), "participant")
		must(err)
		return chat
	}
	chat := chatOf(owner, session)
	ports, err := p2test.HoldIn(owner, portscontract.Ports, delta.NewJsonObject(0), "owner")
	must(err)
	// The open store is read at each use (the native's `.forwards`): the object may be opened again.
	current := func() *portscontract.Forwards {
		registry, err := fixture.Server.Host.Registry(p2test.Background, portscontract.ForwardsNative, host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		parts, _ := registry.Value().(*portscontract.ForwardsParts)
		if parts == nil {
			return nil
		}
		return parts.Forwards()
	}
	must(p2test.Poll(func() (bool, error) { return current() != nil, nil }, 0, "the forwards native"))
	origin := portsOrigin
	current().SetRadiusOrigin(&origin)
	forwardOf := func(port int) *delta.JsonObject {
		value, _ := ports.Member("forwards").Value()
		for _, each := range list(value) {
			if object(each).Value("port") == float64(port) {
				return object(each)
			}
		}
		return nil
	}
	_, err = ports.Call("add", delta.JsonObjectOf("port", float64(3000), "name", "docs", "public", true), p2test.Background)
	must(err)
	// The answer follows its state: the new forward and its public link are on the page when the call resolves.
	docs := forwardOf(3000)
	if docs == nil || !strings.HasPrefix(docs.Value("publicUrl").(string), "https://pub--") {
		t.Fatalf("docs = %v", docs)
	}
	_, err = ports.Call("setPublic", delta.JsonObjectOf("id", docs.Value("id"), "public", false), p2test.Background)
	must(err)
	if forwardOf(3000).Value("publicUrl") != nil {
		t.Fatal("a private forward has a public link")
	}
	_, err = ports.Call("setPublic", delta.JsonObjectOf("id", docs.Value("id"), "public", true), p2test.Background)
	must(err)
	_, err = ports.Call("add", delta.JsonObjectOf("port", float64(3999)), p2test.Background)
	must(err)
	temporary := forwardOf(3999)
	_, err = ports.Call("remove", delta.JsonObjectOf("id", temporary.Value("id")), p2test.Background)
	must(err)
	if forwardOf(3999) != nil {
		t.Fatal("the removed forward is gone at the answer")
	}

	serial := 0
	call := func(query string, target *host.Reference) *delta.JsonObject {
		t.Helper()
		view := until(target.Member("view"), func(value delta.JsonValue) bool { return value != nil })
		count := len(toolResults(view, "ports"))
		serial++
		_, err := target.Call("send", delta.JsonObjectOf("text", "ports "+query, "requestId", "ports-"+strconv.Itoa(serial)), p2test.Background)
		must(err)
		view = until(target.Member("view"), func(value delta.JsonValue) bool {
			return len(toolResults(value, "ports")) > count && !running(value)
		})
		return toolResults(view, "ports")[len(toolResults(view, "ports"))-1]
	}
	isError := func(result *delta.JsonObject) bool { return result.Value("isError") == true }
	opened := call(`{"action":"open","port":5173,"name":"web"}`, chat)
	if isError(opened) {
		t.Fatalf("open: %s", resultText(opened))
	}
	web, found := forwardNamed(current(), 5173)
	if !found || web.Session != session || web.Public {
		t.Fatalf("web = %+v", web)
	}
	for _, pattern := range []string{
		`Forwarded port 5173, private: https://fwd--`,
		`port 3000 \("docs"\), PUBLIC, added by the owner`,
		`Public endpoints, open to anyone`,
	} {
		if !regexp.MustCompile(pattern).MatchString(resultText(opened)) {
			t.Errorf("the open result lacks %q:\n%s", pattern, resultText(opened))
		}
	}
	if details, _ := chordjson.Marshal(opened.Value("details")); !regexp.MustCompile(`sessionTitle.*Web app`).Match(details) {
		t.Errorf("details = %s", details)
	}
	if isError(call(`{"action":"open","port":5173,"public":true}`, chat)) {
		t.Fatal("opening a forwarded port again failed")
	}
	if web, _ := current().Get(web.ID); !web.Public || web.Name != "web" {
		t.Fatalf("web = %+v", web)
	}
	// The owner's forward can be changed too; it keeps its address and stays the owner's.
	shared, _ := forwardNamed(current(), 3000)
	if isError(call(`{"action":"open","port":3000,"public":false}`, chat)) {
		t.Fatal("changing the owner's forward failed")
	}
	if got, _ := current().Get(shared.ID); got != (portscontract.Forward{ID: shared.ID, Port: 3000, Name: "docs"}) {
		t.Fatalf("shared = %+v", got)
	}
	call(`{"action":"open","port":3000,"public":true}`, chat)
	if got, _ := current().Get(shared.ID); !got.Public {
		t.Fatal("the owner's forward did not become public")
	}
	if !isError(call(`{"action":"open","port":`+strconv.Itoa(fixture.Server.Port)+`}`, chat)) {
		t.Fatal("the server's own port was forwarded")
	}
	until(ports.Member("forwards"), func(value delta.JsonValue) bool {
		for _, each := range list(value) {
			if object(each).Value("port") == float64(5173) && object(each).Value("sessionTitle") == "Web app" {
				return true
			}
		}
		return false
	})
	call(`{"action":"close","port":5173}`, chat)
	if text := resultText(call(`{"action":"close","port":5173}`, chat)); !strings.Contains(text, "was not forwarded") {
		t.Fatalf("closing twice: %s", text)
	}
	call(`{"action":"open","port":5174}`, chat)
	_, err = fixture.Server.Reload([]string{"ports"}, p2test.Background)
	must(err)
	if isError(call(`{"action":"list"}`, chat)) {
		t.Fatal("list after a reload failed")
	}

	// A guest cannot make the agent forward ports, nor reach the registry.
	sharing, err := p2test.HoldIn(owner, sharingcontract.Sharing, delta.JsonObjectOf("session", session), "owner")
	must(err)
	_, err = sharing.Call("link", delta.JsonObjectOf("mode", "full"), p2test.Background)
	must(err)
	links := until(sharing.Member("links"), func(value delta.JsonValue) bool { return len(list(value)) > 0 })
	headers := map[string]string{"Content-Type": "application/json"}
	for key, value := range tunnelHeaders {
		headers[key] = value
	}
	body, _ := chordjson.Marshal(delta.JsonObjectOf("token", object(list(links)[0]).Value("token"), "name", "Owner"))
	joined, err := p2test.HTTP(fixture.Server.Port, "/join", p2test.HTTPOptions{Method: http.MethodPost, Headers: headers, Body: strings.NewReader(string(body))})
	if err != nil || joined.Status != 200 {
		t.Fatalf("join: %v %+v", err, joined)
	}
	cookie, _, _ := strings.Cut(joined.Headers.Get("Set-Cookie"), ";")
	guest, _ := fixture.Client(map[string]string{"Host": "share.test", "Origin": "https://share.test", "Cookie": cookie})
	guestChat := chatOf(guest, session)
	if text := resultText(call(`{"action":"open","port":8080}`, guestChat)); !strings.Contains(text, "needs a turn the owner started") {
		t.Fatalf("a guest's open: %s", text)
	}
	guestPorts, err := guest.Hold(p2test.Background, portscontract.Ports, delta.NewJsonObject(0), host.GetOptions{})
	must(err)
	p2test.ExpectChord(t, p2runtime.WhenLive(p2test.Background, guestPorts), data.CodeForbidden)
	// `query` is the worker's: the owner calling it anyway is refused.
	_, err = ports.Call("query", delta.JsonObjectOf("conversation", float64(1), "origin", "owner", "query", delta.JsonObjectOf("action", "list")), p2test.Background)
	p2test.ExpectChord(t, err, data.CodeInvalid)
	if _, found := forwardNamed(current(), 8080); found {
		t.Fatal("a guest forwarded a port")
	}
	_, err = sessions.Call("archive", delta.JsonObjectOf("id", session), p2test.Background)
	must(err)
	// Pi's archive job runs its closers before the test's next line; Go's job runs beside the call, so the closing is awaited.
	if err := p2test.Poll(func() (bool, error) { return equalInts(portsOf(current()), []int{3000}), nil }, 0, "the archived session's forwards to close"); err != nil {
		t.Fatalf("%v: forwards = %v", err, portsOf(current()))
	}

	// The agent may close any forward, also the owner's.
	other, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace, "title", "Other"), p2test.Background)
	must(err)
	otherID := object(other).Value("id").(string)
	otherChat := chatOf(owner, otherID)
	// A remote-environment session cannot open (Pi Mobile's message); it can still close.
	if isError(call(`{"action":"list"}`, otherChat)) {
		t.Fatal("list failed")
	}
	_, err = fixture.Server.Host.Root(func(ctx host.Ctx) (any, error) {
		catalog, err := ctx.Get(sessionscontract.Catalog, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			return nil, err
		}
		return catalog.Call("update", delta.JsonObjectOf("id", otherID, "env", "gpu"), ctx)
	}, p2test.Background, host.RootOptions{})
	must(err)
	refused := call(`{"action":"open","port":9090}`, otherChat)
	if !isError(refused) || !strings.Contains(resultText(refused), `This session runs on "gpu": ports of remote environments cannot be forwarded yet, only ports of this device`) {
		t.Fatalf("refused = %v", resultText(refused))
	}
	closedOwners := call(`{"action":"close","port":3000}`, otherChat)
	if isError(closedOwners) || !strings.Contains(resultText(closedOwners), "Stopped forwarding port 3000") {
		t.Fatalf("closing the owner's forward: %s", resultText(closedOwners))
	}
	if got := portsOf(current()); len(got) != 0 {
		t.Fatalf("forwards = %v", got)
	}
}

func forwardNamed(store *portscontract.Forwards, port int) (portscontract.Forward, bool) {
	for _, forward := range store.List() {
		if forward.Port == port {
			return forward, true
		}
	}
	return portscontract.Forward{}, false
}

func portsOf(store *portscontract.Forwards) []int {
	out := []int{}
	for _, forward := range store.List() {
		out = append(out, forward.Port)
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for at := range a {
		if a[at] != b[at] {
			return false
		}
	}
	return true
}
