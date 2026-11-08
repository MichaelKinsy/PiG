package host_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/host"
)

const (
	sessionID = "11111111-2222-4333-8444-555555555555"
	otherID   = "99999999-2222-4333-8444-555555555555"
	fileID    = "aaaaaaaa-2222-4333-8444-555555555555"
)

var (
	target    = &host.GrantTarget{Session: sessionID, Conversation: 1}
	serverURL = mustURL("https://server.test")
)

func mustURL(text string) *url.URL {
	u, err := url.Parse(text)
	if err != nil {
		panic(err)
	}
	return u
}

// recordingUI is the launcher UI of an action under test: it confirms what it is shown and records the notices.
type recordingUI struct {
	mu      sync.Mutex
	notices []string
}

func (r *recordingUI) Confirm(host.ConsentRequest) bool { return true }
func (r *recordingUI) Notice(text string, _ time.Duration) {
	r.mu.Lock()
	r.notices = append(r.notices, text)
	r.mu.Unlock()
}
func (r *recordingUI) Pick(string, string) (string, bool) { return "", false }

type testContext struct {
	host.ActionContext
	ui *recordingUI
}

func newContext(grants *host.Grants, mutate func(*host.ActionContext)) testContext {
	ui := &recordingUI{}
	ctx := host.ActionContext{
		Grant: grants.Create("test", host.GrantC, new(int), target), UI: ui, Origin: serverURL,
		Ask:             func(string, any) (any, error) { return nil, nil },
		WriteTerminal:   func(string) {},
		SendInput:       func(string) {},
		ReleaseTerminal: func(run func() error) error { return run() },
	}
	if mutate != nil {
		mutate(&ctx)
	}
	return testContext{ctx, ui}
}

func context_(mutate func(*host.ActionContext)) testContext {
	return newContext(host.NewGrants(serverURL.String()), mutate)
}

func actionC(t *testing.T, action host.LauncherAction) host.LauncherAction {
	t.Helper()
	if action.Kind != host.GrantC {
		t.Fatalf("not a C action: %+v", action)
	}
	return action
}

func actionK(t *testing.T, action host.LauncherAction) host.LauncherAction {
	t.Helper()
	if action.Kind != host.GrantK {
		t.Fatalf("not a K action: %+v", action)
	}
	return action
}

func consentOf(action host.LauncherAction, payload any, base *url.URL) *host.ConsentRequest {
	return action.Consent(payload, host.ConsentContext{Origin: base, Target: target})
}

func TestCopyCShowsTheFullTextAndWritesOnlyOnRunKAsksTheWorkerThenSaysWhatItCopied(t *testing.T) {
	var copies []string
	actions := host.LauncherActions(host.LauncherActionOptions{
		URL: serverURL, Role: "guest",
		Clipboard: func(text string, _ func(string)) error { copies = append(copies, text); return nil },
	})
	copy := actionC(t, actions["copy"])
	for _, payload := range []any{nil, map[string]any{}, map[string]any{"text": ""}, map[string]any{"text": 5.0}, map[string]any{"text": strings.Repeat("x", 1024*1024+1)}} {
		if consentOf(copy, payload, serverURL) != nil {
			t.Fatalf("consented to %v", payload)
		}
	}
	consent := consentOf(copy, map[string]any{"text": "line one\nline two", "label": "the last answer"}, serverURL)
	if consent == nil || consent.Action != "Copy 17 characters to the clipboard" || consent.Preview != "line one\nline two" ||
		!reflect.DeepEqual(consent.Facts, []host.ConsentFact{{Label: "What", Value: "the last answer (server-reported)"}}) {
		t.Fatalf("consent = %+v", consent)
	}
	if len(copies) != 0 {
		t.Fatal("nothing is written before confirmation")
	}
	ctx := context_(nil)
	if _, err := copy.Run(map[string]any{"text": "line one\nline two"}, ctx.ActionContext); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(copies, []string{"line one\nline two"}) || !slices.Equal(ctx.ui.notices, []string{"Copied 17 characters: line one"}) {
		t.Fatalf("copies %v notices %v", copies, ctx.ui.notices)
	}

	key := actionK(t, actions["copy.key"])
	if key.Key != "app.message.copy" {
		t.Fatalf("key = %q", key.Key)
	}
	var asked []string
	if err := key.RunKey(context_(func(c *host.ActionContext) {
		c.Ask = func(kind string, _ any) (any, error) {
			asked = append(asked, kind)
			return map[string]any{"text": "answer"}, nil
		}
	}).ActionContext); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(asked, []string{"copyText"}) || copies[len(copies)-1] != "answer" {
		t.Fatalf("asked %v copies %v", asked, copies)
	}
	empty := context_(func(c *host.ActionContext) {
		c.Ask = func(string, any) (any, error) { return map[string]any{"text": ""}, nil }
	})
	if err := key.RunKey(empty.ActionContext); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(empty.ui.notices, []string{"Nothing to copy"}) || len(copies) != 2 {
		t.Fatalf("notices %v copies %v", empty.ui.notices, copies)
	}
}

func TestOpenURLHTTPSExternalsAndAllowlistedOriginPathsOnlyNeverWithCredentials(t *testing.T) {
	var opened []string
	actions := host.LauncherActions(host.LauncherActionOptions{
		URL: serverURL, Credentials: host.Credentials{Cookie: "owner=secret"}, Role: "owner",
		OpenBrowser: func(target string) { opened = append(opened, target) },
	})
	open := actionC(t, actions["openUrl"])
	ok := func(payload any) bool { return consentOf(open, payload, serverURL) != nil }
	if !ok(map[string]any{"kind": "external", "url": "https://provider.test/oauth?x=1"}) {
		t.Fatal("https external")
	}
	for _, u := range []string{"http://provider.test", "https://user:pass@provider.test", "javascript:alert(1)", "file:///etc"} {
		if ok(map[string]any{"kind": "external", "url": u}) {
			t.Fatal(u)
		}
	}
	origin := func(path string) any { return map[string]any{"kind": "origin", "path": path} }
	for _, path := range []string{"/s/" + otherID, "/artifact?session=" + sessionID + "&name=chart&version=2", "/download?session=" + sessionID + "&id=" + fileID} {
		if !ok(origin(path)) {
			t.Fatal(path)
		}
	}
	for _, path := range []string{
		"/artifact?session=" + otherID + "&name=chart&version=2", "/download?session=" + sessionID + "&id=" + fileID + "&cookie=1",
		"/owner", "/__p2_dev", "//evil.test/s/x", "https://evil.test/", "/s/" + sessionID + "#x", "/s/../owner",
	} {
		if ok(origin(path)) {
			t.Fatal(path)
		}
	}
	consent := consentOf(open, origin("/s/"+sessionID), serverURL)
	if consent.Preview != "https://server.test/s/"+sessionID || !strings.Contains(consent.Facts[0].Value, "own sign-in") {
		t.Fatalf("consent = %+v", consent)
	}
	for _, payload := range []any{origin("/s/" + sessionID), map[string]any{"kind": "external", "url": "https://provider.test/oauth?x=1"}} {
		if _, err := open.Run(payload, context_(nil).ActionContext); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(opened, []string{"https://server.test/s/" + sessionID, "https://provider.test/oauth?x=1"}) || strings.Contains(strings.Join(opened, ""), "secret") {
		t.Fatalf("opened %v", opened)
	}
	if got := host.OriginPath(serverURL, "/s/"+sessionID); got == nil || host.OriginOf(got) != "https://server.test" {
		t.Fatalf("originPath = %v", got)
	}
}

func TestDownloadFixedRouteTheLaunchersCookieBoundedSanitizedNeverOverwritesNoRedirects(t *testing.T) {
	folder := t.TempDir()
	type seenRequest struct {
		url    string
		header http.Header
	}
	var mu sync.Mutex
	var seen []seenRequest
	mode := "file"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, seenRequest{r.URL.RequestURI(), r.Header.Clone()})
		current := mode
		mu.Unlock()
		switch current {
		case "redirect":
			w.Header().Set("Location", "https://evil.test/")
			w.WriteHeader(http.StatusFound)
		case "huge":
			chunk := make([]byte, 1024*1024)
			for range 30 {
				if _, err := w.Write(chunk); err != nil {
					return
				}
			}
		default:
			w.Header().Set("Content-Disposition", `attachment; filename="x"; filename*=UTF-8''`+url.PathEscape("../../.evil\x1b.txt"))
			_, _ = io.WriteString(w, "content")
		}
	}))
	t.Cleanup(server.Close)
	address := mustURL(server.URL)
	actions := host.LauncherActions(host.LauncherActionOptions{URL: address, Credentials: host.Credentials{Cookie: "p2_guest_1=a.b"}, Role: "guest", Downloads: folder})
	download := actionC(t, actions["download"])
	if consentOf(download, map[string]any{"kind": "file", "session": otherID, "id": fileID}, address) != nil ||
		consentOf(download, map[string]any{"kind": "file", "session": sessionID, "id": "../x"}, address) != nil {
		t.Fatal("consented to a foreign session or an invalid id")
	}
	consent := consentOf(download, map[string]any{"kind": "file", "session": sessionID, "id": fileID, "name": "report.pdf"}, address)
	if consent.Facts[0] != (host.ConsentFact{Label: "File", Value: "report.pdf (server-reported)"}) || consent.Facts[1] != (host.ConsentFact{Label: "Folder", Value: folder}) {
		t.Fatalf("facts = %+v", consent.Facts)
	}
	payload := map[string]any{"kind": "file", "session": sessionID, "id": fileID}
	for range 2 {
		value, err := download.Run(payload, context_(nil).ActionContext)
		if err != nil || !reflect.DeepEqual(value, map[string]any{"saved": true}) {
			t.Fatalf("download: %v %v", value, err)
		}
	}
	entries, _ := os.ReadDir(folder)
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"evil_ (1).txt", "evil_.txt"}) {
		t.Fatalf("names = %v", names)
	}
	if body, _ := os.ReadFile(filepath.Join(folder, "evil_.txt")); string(body) != "content" {
		t.Fatalf("body = %q", body)
	}
	if seen[0].url != "/download?session="+sessionID+"&id="+fileID || seen[0].header.Get("Cookie") != "p2_guest_1=a.b" || seen[0].header.Get("Origin") != host.OriginOf(address) {
		t.Fatalf("request = %+v", seen[0])
	}
	mu.Lock()
	mode = "huge"
	mu.Unlock()
	if _, err := download.Run(payload, context_(nil).ActionContext); err == nil || !strings.Contains(err.Error(), "25 MiB") {
		t.Fatalf("huge: %v", err)
	}
	mu.Lock()
	mode = "redirect"
	mu.Unlock()
	if _, err := download.Run(payload, context_(nil).ActionContext); err == nil {
		t.Fatal("a redirect was followed")
	}
	if entries, _ := os.ReadDir(folder); len(entries) != 2 {
		t.Fatalf("failed downloads left %d files", len(entries))
	}
	artifact := map[string]any{"kind": "artifact", "session": sessionID, "name": "chart", "version": 3.0}
	if consentOf(download, artifact, address) == nil || consentOf(download, map[string]any{"kind": "artifact", "session": sessionID, "name": "../x", "version": 3.0}, address) != nil {
		t.Fatal("artifact consent")
	}
	if host.SafeFileName("..\\..\\secret") != "secret" || host.SafeFileName("") != "download" {
		t.Fatal("safeFileName")
	}
}

func TestExternalEditorAndSuspendLendTheTerminalDevCommandAndSignOutAreOwnerActionsOnFixedRoutes(t *testing.T) {
	var editorCalls [][]string
	var lent []string
	actions := host.LauncherActions(host.LauncherActionOptions{
		URL: serverURL, Credentials: host.Credentials{Cookie: "owner=c"}, Role: "owner",
		Editor: host.EditorArgv(`code --wait "--flag with space"`),
		RunEditor: func(argv []string, file string) (int, bool) {
			editorCalls = append(editorCalls, slices.Clone(argv))
			text, _ := os.ReadFile(file)
			_ = os.WriteFile(file, append(text, " edited\n"...), 0o600)
			return 0, true
		},
		SuspendProcess: func() error { lent = append(lent, "suspended"); return nil },
	})
	editor := actionK(t, actions["editor.external"])
	if editor.Key != "app.editor.external" {
		t.Fatalf("key = %q", editor.Key)
	}
	type ask struct {
		kind    string
		payload any
	}
	var asks []ask
	ctx := context_(func(c *host.ActionContext) {
		c.Ask = func(kind string, payload any) (any, error) {
			asks = append(asks, ask{kind, payload})
			if kind == "editorText" {
				return map[string]any{"text": "draft"}, nil
			}
			return map[string]any{"applied": true}, nil
		}
		c.ReleaseTerminal = func(run func() error) error {
			lent = append(lent, "released")
			defer func() { lent = append(lent, "restored") }()
			return run()
		}
	})
	if err := editor.RunKey(ctx.ActionContext); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(editorCalls[0][:3], []string{"code", "--wait", "--flag with space"}) ||
		!reflect.DeepEqual(asks, []ask{{"editorText", nil}, {"editorResult", map[string]any{"text": "draft edited"}}}) ||
		!slices.Equal(lent, []string{"released", "restored"}) {
		t.Fatalf("editor %v asks %v lent %v", editorCalls, asks, lent)
	}
	if err := actionK(t, actions["suspend"]).RunKey(ctx.ActionContext); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(lent[2:], []string{"released", "suspended", "restored"}) {
		t.Fatalf("lent = %v", lent)
	}
	windows := context_(nil)
	_ = actionK(t, host.LauncherActions(host.LauncherActionOptions{URL: serverURL, Role: "owner", Platform: "win32"})["suspend"]).RunKey(windows.ActionContext)
	if len(windows.ui.notices) == 0 || !strings.Contains(windows.ui.notices[0], "not supported on Windows") {
		t.Fatalf("windows notices %v", windows.ui.notices)
	}

	type seenRequest struct{ path, method, origin, cookie, body string }
	var mu sync.Mutex
	var requests []seenRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, seenRequest{r.URL.Path, r.Method, r.Header.Get("Origin"), r.Header.Get("Cookie"), string(body)})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"message":"Reloaded: chat"}`)
	}))
	t.Cleanup(server.Close)
	address := mustURL(server.URL)
	live := host.LauncherActions(host.LauncherActionOptions{URL: address, Credentials: host.Credentials{Cookie: "owner=c"}, Role: "owner"})
	dev := actionC(t, live["devCommand"])
	if consentOf(dev, map[string]any{"action": "reload"}, address) == nil ||
		consentOf(dev, map[string]any{"action": "restart"}, address) != nil ||
		consentOf(dev, map[string]any{"action": "reload", "plugins": []any{"x"}}, address) != nil {
		t.Fatal("devCommand consent")
	}
	value, err := dev.Run(map[string]any{"action": "reload"}, context_(nil).ActionContext)
	if err != nil || !reflect.DeepEqual(value, map[string]any{"message": "Reloaded: chat"}) {
		t.Fatalf("dev: %v %v", value, err)
	}
	signOut := actionC(t, live["signOut"])
	if all := consentOf(signOut, map[string]any{"all": true}, address); all == nil || !strings.Contains(all.Action, "ALL owner devices") {
		t.Fatalf("signOut consent = %+v", all)
	}
	if _, err := signOut.Run(map[string]any{"all": false}, context_(nil).ActionContext); err != nil {
		t.Fatal(err)
	}
	want := []seenRequest{
		{"/__p2_dev", "POST", host.OriginOf(address), "owner=c", `{"action":"reload"}`},
		{"/owner/logout", "POST", host.OriginOf(address), "owner=c", ""},
	}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests = %+v", requests)
	}
	guest := host.LauncherActions(host.LauncherActionOptions{URL: serverURL, Credentials: host.Credentials{Cookie: "g"}, Role: "guest"})
	if consentOf(actionC(t, guest["devCommand"]), map[string]any{"action": "reload"}, serverURL) != nil || consentOf(actionC(t, guest["signOut"]), map[string]any{"all": true}, serverURL) != nil {
		t.Fatal("a guest got an owner action")
	}
	loopback := host.LauncherActions(host.LauncherActionOptions{URL: mustURL("http://127.0.0.1:1"), Role: "owner"})
	if consentOf(actionC(t, loopback["signOut"]), map[string]any{"all": false}, serverURL) != nil {
		t.Fatal("the device owner has no cookie")
	}
	if consentOf(actionC(t, loopback["signOut"]), map[string]any{"all": true}, serverURL) == nil {
		t.Fatal("sign out all")
	}
	_ = context.Background
}
