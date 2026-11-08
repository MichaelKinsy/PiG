package native

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/surfacehost"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/egoist/mygo/ui"
)

// transferOf holds the session's SessionTransfer as the server (the only principal it serves), as `Sessions.fork` does.
func transferOf(t *testing.T, server *hosts.P2Server, session string) *host.Reference {
	t.Helper()
	down, up := link.MemoryLinkPair()
	if err := server.Host.Accept(down, host.LinkOptions{Principal: p2runtime.ToChord(p2runtime.Server)}, p2test.Background); err != nil {
		t.Fatal(err)
	}
	client, err := p2runtime.NewHost(host.HostOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(p2test.Background) })
	if err := client.Connect(up, host.LinkOptions{Principal: p2runtime.ToChord(p2runtime.Server)}, p2test.Background); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Load(nil, p2test.Background); err != nil {
		t.Fatal(err)
	}
	ref, err := p2test.HoldIn(client, contract.SessionTransfer, delta.JsonObjectOf("session", session), "server")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ref.Release)
	return ref
}

func startFixtureApp(t *testing.T, data string) (*hosts.P2Server, *App, *ui.Tester) {
	t.Helper()
	fake, zero := true, 0
	server, err := hosts.StartServer(hosts.ServerOptions{DataDir: data, Port: &zero, Fake: &fake, Facets: []host.HostFacet{surfacehost.ServerFacet(nil)}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(p2test.Background) })
	app := New()
	h, err := surfacehost.Open(surfacehost.Options{Server: server.Host, Changed: app.Invalidate})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	if err := app.Start(h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Detach)
	tt := ui.NewTester(app.Build, 1280, 812)
	until(t, tt, "the sessions", app.sessions.Ready)
	return server, app, tt
}

// TestMakeFixture writes the long-session fixture of the measurement without a model: the entries of one real exchange (made
// through Chat.send on a scratch server and read back as Sessions.fork reads a history: `SessionTransfer.history`) are
// repeated P2_FIXTURE_EXCHANGES times with the user's text numbered, and appended to a new session's main conversation by
// `SessionTransfer.seed`, the way a fork seeds its copy. P2_FIXTURE_DIR is the data directory it makes; the session is titled
// as TestMeasureLong finds it. Skipped unless P2_FIXTURE_DIR is set.
func TestMakeFixture(t *testing.T) {
	dir := os.Getenv("P2_FIXTURE_DIR")
	if dir == "" {
		t.Skip("P2_FIXTURE_DIR is not set")
	}
	exchanges, _ := strconv.Atoi(os.Getenv("P2_FIXTURE_EXCHANGES"))
	if exchanges == 0 {
		exchanges = 5000
	}
	const text = "message %d: please answer with a short paragraph"
	// One real exchange.
	var template []any
	{
		server, app, tt := startFixtureApp(t, t.TempDir())
		id, err := app.sessions.Create(os.TempDir(), "template")
		if err != nil {
			t.Fatal(err)
		}
		app.setSel(id)
		until(t, tt, "the chat", func() bool { return app.chat != nil && tt.HasText("Say something to start.") })
		for n := range 2 {
			if err := app.chat.Send(fmt.Sprintf(text, n)); err != nil {
				t.Fatal(err)
			}
			until(t, tt, "the answer", func() bool {
				tr, _ := app.chat.Transcript()
				return !tr.Busy && len(tr.Queued) == 0 && tt.HasText(fmt.Sprintf(text, n)) && !tt.HasText("Working…")
			})
			time.Sleep(time.Second)
		}
		ref := transferOf(t, server, id)
		// The history before the second message is the whole first exchange (what a fork of it would copy).
		for before := 1; before <= 200; before++ {
			out, err := ref.Call("history", delta.JsonObjectOf("conversation", float64(1), "before", float64(before)), context.Background())
			if err != nil {
				continue
			}
			entries, _ := out.(*delta.JsonObject).Value("entries").([]any)
			shown, _ := json.Marshal(entries)
			if strings.Contains(string(shown), fmt.Sprintf(text, 1)) {
				break
			}
			template = entries
		}
		kinds := []string{}
		for _, entry := range template {
			kind, _ := entry.(*delta.JsonObject).Value("kind").(string)
			kinds = append(kinds, kind)
		}
		t.Logf("template: %d entries, kinds %v", len(template), kinds)
		if len(template) < 3 {
			t.Fatal("the template exchange is missing its answer")
		}
	}
	// The system entry (prompt and tools) is the first exchange's only; every exchange after is its input and what the run made of it.
	full := template
	var exchange []any
	for _, entry := range template {
		if kind, _ := entry.(*delta.JsonObject).Value("kind").(string); kind == "pi.system" {
			continue
		} else {
			exchange = append(exchange, entry)
		}
	}
	template = exchange
	encoded, err := json.Marshal(template)
	if err != nil {
		t.Fatal(err)
	}
	encodedFull, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	server, app, tt := startFixtureApp(t, dir)
	id, err := app.sessions.Create(os.TempDir(), fixtureTitle)
	if err != nil {
		t.Fatal(err)
	}
	ref := transferOf(t, server, id)
	entries := make([]any, 0, exchanges*len(template))
	for n := range exchanges {
		source := encoded
		if n == 0 {
			source = encodedFull // the session's first exchange carries the system entry, as a real session's does
		}
		numbered := strings.ReplaceAll(string(source), fmt.Sprintf(text, 0), fmt.Sprintf(text, n))
		decoded, err := chordjson.Decode([]byte(numbered))
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, decoded.([]any)...)
	}
	started := time.Now()
	if _, err := ref.Call("seed", delta.JsonObjectOf("entries", entries), context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Logf("seeded %d entries (%d exchanges) in %s", len(entries), exchanges, time.Since(started).Round(time.Millisecond))
	_ = tt
}
