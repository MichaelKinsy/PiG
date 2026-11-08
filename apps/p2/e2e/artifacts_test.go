// Ports test/p2/artifacts.test.ts (Pi main c23b9ec1): the `artifact` tool, the artifact index, the `/artifact` route and the
// inline preview contract, owner and guests.
//
// The long-preview case of "serves Markdown/text as non-executable documents and validates byte bounds" is PENDING the codemode
// plugin (see the case); the store's refusal of a large artifact before touching the files is `plugins/artifacts/store_test.go`.

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	artifactscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/artifacts/contract"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	sharingcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sharing/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/artifacts"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/files"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/sharing"
)

// toolResults are the tool result messages of tool toolName in a conversation view's entries.
func toolResults(view delta.JsonValue, toolName string) []*delta.JsonObject {
	var out []*delta.JsonObject
	entries, _ := object(view).Value("entries").([]any)
	for _, entry := range entries {
		model, _ := object(entry).Value("model").([]any)
		for _, each := range model {
			if message := object(each); message != nil && message.Value("role") == "toolResult" && message.Value("toolName") == toolName {
				out = append(out, message)
			}
		}
	}
	return out
}

// sameJSON is whether two JSON values are equal, an object's keys in any order (Node's `deepEqual`).
func sameJSON(a, b any) bool {
	canonical := func(value any) string {
		encoded, err := chordjson.Marshal(value)
		if err != nil {
			return err.Error()
		}
		var parsed any
		if err := json.Unmarshal(encoded, &parsed); err != nil {
			return err.Error()
		}
		again, _ := json.Marshal(parsed)
		return string(again)
	}
	return canonical(a) == canonical(b)
}

func versionsOf(artifacts delta.JsonValue) []float64 {
	var versions []float64
	for _, each := range list(artifacts) {
		versions = append(versions, object(each).Value("version").(float64))
	}
	return versions
}

func TestArtifactsFakeProvider(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	var rebuilds p2test.Rebuilds
	fixture := p2test.StartFixture(t, hosts.ServerOptions{AllowedHosts: []string{"share.test"}, BuildRevision: rebuilds.Revision})
	owner := fixture.Browser()
	sessions, err := p2test.HoldIn(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	create := func() string {
		created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		return object(created).Value("id").(string)
	}
	session, other := create(), create()
	holdChat := func() *host.Reference {
		chat, err := p2test.HoldIn(owner, chatcontract.Chat, delta.JsonObjectOf("session", session, "conversation", float64(1)), "participant")
		if err != nil {
			t.Fatal(err)
		}
		return chat
	}
	holdList := func(h host.Host, id string) *host.Reference {
		held, err := p2test.HoldIn(h, artifactscontract.ArtifactList, delta.JsonObjectOf("session", id, "conversation", float64(1)), "reader")
		if err != nil {
			t.Fatal(err)
		}
		return held
	}
	chat, index := holdChat(), holdList(owner, session)
	until := func(t *testing.T, source p2test.JSONSource, predicate func(delta.JsonValue) bool) delta.JsonValue {
		t.Helper()
		value, err := p2test.Until(source, predicate, 0)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	send := func(t *testing.T, text, requestID string) {
		t.Helper()
		if _, err := chat.Call("send", delta.JsonObjectOf("text", text, "requestId", requestID), p2test.Background); err != nil {
			t.Fatal(err)
		}
	}
	artifactURL := func(name string, version int, id string) string {
		return "/artifact?" + url.Values{"session": {id}, "name": {name}, "version": {strconv.Itoa(version)}}.Encode()
	}
	demo := func() string { return artifactURL("demo", 1, session) }
	get := func(path string, headers map[string]string, method ...string) p2test.HTTPResult {
		t.Helper()
		options := p2test.HTTPOptions{Headers: headers}
		if len(method) > 0 {
			options.Method = method[0]
		}
		result, err := p2test.HTTP(fixture.Server.Port, path, options)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	t.Run("publishes immutable numbered versions and indexes them outside the transcript", func(t *testing.T) {
		for version := 1; version <= 2; version++ {
			send(t, "artifact", "artifact-"+strconv.Itoa(version))
			view := until(t, chat.Member("view"), func(value delta.JsonValue) bool {
				return value != nil && len(toolResults(value, "artifact")) == version && !running(value)
			})
			last := toolResults(view, "artifact")[version-1]
			if last.Value("isError") != false {
				t.Fatalf("version %d: %v", version, last)
			}
			if got := object(last.Value("details")).Value("version"); got != float64(version) {
				t.Fatalf("details version = %v, want %d", got, version)
			}
		}
		listed := until(t, index.Member("artifacts"), func(value delta.JsonValue) bool { return len(list(value)) == 2 })
		if got := versionsOf(listed); len(got) != 2 || got[0] != 1 || got[1] != 2 {
			t.Fatalf("index versions = %v", got)
		}
		source := get(demo()+"&download=1", nil)
		if source.Status != 200 || source.Body != "<!doctype html><title>Demo</title><h1>Artifact demo</h1>" {
			t.Fatalf("source: %d %q", source.Status, source.Body)
		}
		if disposition := source.Headers.Get("Content-Disposition"); !regexp.MustCompile(`attachment; filename="demo.html"`).MatchString(disposition) {
			t.Fatalf("Content-Disposition = %q", disposition)
		}
	})

	t.Run("isolates executable previews and instruments only the preview, with correct HEAD", func(t *testing.T) {
		preview := get(demo(), nil)
		if preview.Status != 200 || !strings.Contains(preview.Body, "p2-artifact-diagnostic") {
			t.Fatalf("preview: %d %q", preview.Status, preview.Body)
		}
		if got := preview.Headers.Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Fatalf("Content-Type = %q", got)
		}
		csp := preview.Headers.Get("Content-Security-Policy")
		if !strings.Contains(csp, "sandbox allow-scripts allow-modals allow-pointer-lock") || strings.Contains(csp, "allow-same-origin") ||
			!regexp.MustCompile(`frame-src 'none'.*object-src 'none'.*base-uri 'none'.*form-action 'none'`).MatchString(csp) {
			t.Fatalf("Content-Security-Policy = %q", csp)
		}
		if got := preview.Headers.Get("Cross-Origin-Resource-Policy"); got != "same-origin" {
			t.Fatalf("Cross-Origin-Resource-Policy = %q", got)
		}
		head := get(demo(), nil, http.MethodHead)
		if head.Status != 200 || head.Body != "" || head.Headers.Get("Content-Length") != preview.Headers.Get("Content-Length") {
			t.Fatalf("HEAD: %d %q length %q, GET length %q", head.Status, head.Body, head.Headers.Get("Content-Length"), preview.Headers.Get("Content-Length"))
		}
		if got := get(demo(), map[string]string{"Sec-Fetch-Site": "cross-site"}).Status; got != 403 {
			t.Fatalf("cross-site: %d", got)
		}
		for _, path := range []string{artifactURL("../demo", 1, session), artifactURL("demo", 0, session), artifactURL("demo", 99, session), artifactURL("demo", 1, other)} {
			if got := get(path, nil).Status; got != 404 {
				t.Fatalf("%s: %d", path, got)
			}
		}
	})

	t.Run("retains both versions and the independent index across worker restart and compatible reload", func(t *testing.T) {
		rebuilds.Rebuild("artifacts")
		if _, err := fixture.Server.Reload([]string{"artifacts"}, context.Background()); err != nil {
			t.Fatal(err)
		}
		until(t, index.Member("artifacts"), func(value delta.JsonValue) bool { return len(list(value)) == 2 })
		if _, err := fixture.Server.Host.Root(func(ctx host.Ctx) (any, error) {
			workers, err := ctx.Get(p2runtime.Hosts, delta.NewJsonObject(0), host.GetOptions{})
			if err != nil {
				return nil, err
			}
			return workers.Call("stop", delta.JsonObjectOf("session", session), ctx)
		}, p2test.Background, host.RootOptions{}); err != nil {
			t.Fatal(err)
		}
		chat.Release()
		index.Release()
		chat, index = holdChat(), holdList(owner, session)
		listed := until(t, index.Member("artifacts"), func(value delta.JsonValue) bool { return len(list(value)) == 2 })
		if got := versionsOf(listed); len(got) != 2 || got[0] != 1 || got[1] != 2 {
			t.Fatalf("index versions = %v", got)
		}
		if first, second := get(artifactURL("demo", 1, session)+"&download=1", nil).Body, get(artifactURL("demo", 2, session)+"&download=1", nil).Body; first != second {
			t.Fatalf("versions differ: %q %q", first, second)
		}
	})

	t.Run("authorizes guests per session, including reads of the artifact index", func(t *testing.T) {
		sharing, err := p2test.HoldIn(owner, sharingcontract.Sharing, delta.JsonObjectOf("session", session), "owner")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sharing.Call("link", delta.JsonObjectOf("mode", "view"), p2test.Background); err != nil {
			t.Fatal(err)
		}
		links := until(t, sharing.Member("links"), func(value delta.JsonValue) bool { return len(list(value)) > 0 })
		body, _ := json.Marshal(map[string]string{"token": object(list(links)[0]).Value("token").(string), "name": "Reader"})
		join, err := p2test.HTTP(fixture.Server.Port, "/join", p2test.HTTPOptions{
			Method: http.MethodPost, Headers: map[string]string{"Host": "share.test", "Origin": "https://share.test", "Content-Type": "application/json"},
			Body: strings.NewReader(string(body)),
		})
		if err != nil || join.Status != 200 {
			t.Fatalf("join: %v %d %s", err, join.Status, join.Body)
		}
		cookie, _, _ := strings.Cut(join.Headers.Get("Set-Cookie"), ";")
		headers := map[string]string{"Host": "share.test", "Origin": "https://share.test", "Cookie": cookie}
		if got := get(demo(), headers).Status; got != 200 {
			t.Fatalf("guest preview: %d", got)
		}
		if got := get(artifactURL("demo", 1, other), headers).Status; got != 403 {
			t.Fatalf("a guest reads another session's artifact: %d", got)
		}
		if got := get(demo(), map[string]string{"Host": "share.test", "Origin": "https://share.test"}).Status; got != 403 {
			t.Fatalf("without the cookie: %d", got)
		}
		guest, _ := fixture.Client(headers)
		guestIndex := holdList(guest, session)
		until(t, guestIndex.Member("artifacts"), func(value delta.JsonValue) bool { return len(list(value)) == 2 })
		refused, err := guest.Hold(p2test.Background, artifactscontract.ArtifactList, delta.JsonObjectOf("session", other, "conversation", float64(1)), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		p2test.ExpectChord(t, p2runtime.WhenLive(p2test.Background, refused), data.CodeForbidden)
		refused.Release()
		// The TUI's inline view: the same authority as the route.
		content, err := p2test.HoldIn(guest, artifactscontract.ArtifactContent, delta.JsonObjectOf("session", session), "reader")
		if err != nil {
			t.Fatal(err)
		}
		read, err := content.Call("read", delta.JsonObjectOf("name", "demo", "version", float64(1)), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		if object(read).Value("content") != "<!doctype html><title>Demo</title><h1>Artifact demo</h1>" ||
			object(object(read).Value("info")).Value("type") != "html" || object(read).Value("truncated") != false {
			t.Fatalf("read = %v", read)
		}
		if _, err := content.Call("read", delta.JsonObjectOf("name", "demo", "version", float64(99)), p2test.Background); err == nil || !strings.Contains(strings.ToLower(err.Error()), "not found") {
			t.Fatalf("version 99: %v", err)
		}
		content.Release()
		elsewhere, err := guest.Hold(p2test.Background, artifactscontract.ArtifactContent, delta.JsonObjectOf("session", other), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		p2test.ExpectChord(t, p2runtime.WhenLive(p2test.Background, elsewhere), data.CodeForbidden)
		elsewhere.Release()
		guestIndex.Release()
		sharing.Release()
	})

	t.Run("carries inline in the result details only, and keeps the index and stored version unchanged", func(t *testing.T) {
		svg := "<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 200 100'><rect width='200' height='100'/></svg>"
		call, _ := json.Marshal(map[string]any{"name": "flow", "title": "Flow", "type": "svg", "content": svg, "inline": true})
		current, _ := chat.Member("view").Value()
		before := len(toolResults(current, "artifact"))
		send(t, "artifact "+string(call), "inline-svg")
		view := until(t, chat.Member("view"), func(value delta.JsonValue) bool {
			return value != nil && len(toolResults(value, "artifact")) == before+1 && !running(value)
		})
		results := toolResults(view, "artifact")
		result := results[len(results)-1]
		if result.Value("isError") != false {
			t.Fatalf("result: %v", result)
		}
		wantDetails := delta.JsonObjectOf("name", "flow", "title", "Flow", "type", "svg", "version", float64(1), "inline", true)
		if !sameJSON(result.Value("details"), wantDetails) {
			t.Fatalf("details = %v", result.Value("details"))
		}
		if encoded, _ := json.Marshal(result.Value("content")); !strings.Contains(string(encoded), "Shown inline in the transcript") {
			t.Fatalf("content = %s", encoded)
		}
		// The plain call has no flag at all.
		if !sameJSON(results[0].Value("details"), delta.JsonObjectOf("name", "demo", "title", "Demo artifact", "type", "html", "version", float64(1))) {
			t.Fatalf("plain details = %v", results[0].Value("details"))
		}
		listed := until(t, index.Member("artifacts"), func(value delta.JsonValue) bool {
			for _, each := range list(value) {
				if object(each).Value("name") == "flow" {
					return true
				}
			}
			return false
		})
		for _, each := range list(listed) {
			if object(each).Value("name") == "flow" && !sameJSON(each, delta.JsonObjectOf("name", "flow", "title", "Flow", "type", "svg", "version", float64(1))) {
				t.Fatalf("index entry = %v", each)
			}
		}
		if body := get(artifactURL("flow", 1, session)+"&download=1", nil).Body; body != svg {
			t.Fatalf("stored: %q", body)
		}
		// The preview's bridge is CDATA inside the root (SVG is XML) and reports the content height.
		preview := get(artifactURL("flow", 1, session), nil).Body
		if !strings.HasPrefix(preview, "<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 200 100'><script><![CDATA[") ||
			!strings.Contains(preview, `level:"size"`) || !strings.HasSuffix(preview, "]]></script><rect width='200' height='100'/></svg>") {
			t.Fatalf("preview: %.200q", preview)
		}
	})

	// save saves a new artifact (a name not saved before) through the tool on the session's worker: its info.
	saves := 0
	save := func(t *testing.T, call map[string]any, timeout time.Duration) *delta.JsonObject {
		t.Helper()
		saved := func(value delta.JsonValue) *delta.JsonObject {
			for _, result := range toolResults(value, "artifact") {
				if info := object(result.Value("details")); info != nil && info.Value("name") == call["name"] {
					return info
				}
			}
			return nil
		}
		encoded, _ := json.Marshal(call)
		saves++
		send(t, "artifact "+string(encoded), "save-"+strconv.Itoa(saves))
		view, err := p2test.Until(chat.Member("view"), func(value delta.JsonValue) bool {
			return value != nil && saved(value) != nil && !running(value)
		}, timeout)
		if err != nil {
			t.Fatal(err)
		}
		return saved(view)
	}

	t.Run("serves Markdown/text as non-executable documents and validates byte bounds", func(t *testing.T) {
		for _, kind := range []string{"markdown", "text", "svg"} {
			content := "# safe <script>no</script>"
			if kind == "svg" {
				content = "<svg xmlns='http://www.w3.org/2000/svg'/>"
			}
			info := save(t, map[string]any{"name": kind, "title": kind, "type": kind, "content": content}, 30*time.Second)
			response := get(artifactURL(kind, int(info.Value("version").(float64)), session), nil)
			if response.Status != 200 {
				t.Fatalf("%s: %d", kind, response.Status)
			}
			// An empty root has no inside for the bridge: served as stored, still well-formed XML.
			if kind == "svg" {
				if response.Body != "<svg xmlns='http://www.w3.org/2000/svg'/>" {
					t.Fatalf("svg: %q", response.Body)
				}
				continue
			}
			if got := response.Headers.Get("Content-Type"); got != "text/plain; charset=utf-8" {
				t.Fatalf("%s Content-Type = %q", kind, got)
			}
			if !strings.HasSuffix(response.Headers.Get("Content-Security-Policy"), "; sandbox") || strings.Contains(response.Body, "p2-artifact-diagnostic") {
				t.Fatalf("%s: CSP %q body %q", kind, response.Headers.Get("Content-Security-Policy"), response.Body)
			}
		}
		// PENDING (needs the codemode plugin, lane P2AgentTools): Pi saves a `long` artifact of MaxArtifactPreview+10 characters with
		// a code mode script and reads it through ArtifactContent: truncated, exactly MaxArtifactPreview characters. The fake
		// provider streams a tool call's arguments at 800 characters a second, so the same call as an `artifact {json}` prompt
		// takes eleven minutes. The cut itself (limit, UTF-16 units, no split pair) is `preview`'s unit test
		// (plugins/artifacts/store_test.go).
	})
}
