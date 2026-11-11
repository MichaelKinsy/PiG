package host_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	filescontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/files/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/host"
	"github.com/MichaelKinsy/PiG/chord/delta"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/artifacts"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/drafts"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/envs"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/files"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/models"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/sharing"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/status"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/terminals"
)

// scriptedUI is the launcher UI of an action under test: it shows what it is asked and answers confirmations as told.
type scriptedUI struct {
	recordingUI
	confirm bool
	shown   []string
}

func (s *scriptedUI) Confirm(request host.ConsentRequest) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shown = append(s.shown, request.Action+"\n"+factsText(request.Facts))
	return s.confirm
}

func factsText(facts []host.ConsentFact) string {
	var lines []string
	for _, fact := range facts {
		lines = append(lines, fact.Label+": "+fact.Value)
	}
	return strings.Join(lines, "\n")
}

func contextFor(grants *host.Grants, session string, confirm bool, mutate func(*host.ActionContext)) (host.ActionContext, *scriptedUI) {
	ui := &scriptedUI{confirm: confirm}
	var grantTarget *host.GrantTarget
	if session != "" {
		grantTarget = &host.GrantTarget{Session: session, Conversation: 1}
	}
	ctx := host.ActionContext{
		Grant: grants.Create("attach", host.GrantC, new(int), grantTarget), UI: ui, Origin: mustURL("http://server.test"),
		Ask:             func(string, any) (any, error) { return nil, nil },
		WriteTerminal:   func(string) {},
		SendInput:       func(string) {},
		ReleaseTerminal: func(run func() error) error { return run() },
	}
	if mutate != nil {
		mutate(&ctx)
	}
	return ctx, ui
}

func TestLocalFilesPathsAsDroppedRegularFilesOnlyNoFinalSymlinks25MiB(t *testing.T) {
	directory := t.TempDir()
	for input, want := range map[string]string{"'/a b/c.png'": "/a b/c.png", `/a\ b/c.png`: "/a b/c.png", "file:///a%20b/c.png": "/a b/c.png"} {
		if got, ok := host.LocalPath(input); !ok || got != want {
			t.Fatalf("localPath(%q) = %q %v", input, got, ok)
		}
	}
	home, _ := os.UserHomeDir()
	if got, _ := host.LocalPath("~/x.txt"); got != filepath.Join(home, "x.txt") {
		t.Fatalf("home = %q", got)
	}
	if _, ok := host.LocalPath(""); ok {
		t.Fatal("empty path")
	}
	file := filepath.Join(directory, "photo.PNG")
	if err := os.WriteFile(file, []byte("png bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	opened, err := host.OpenLocalFile(file)
	if err != nil || opened.Size != 9 || opened.MimeType != "image/png" {
		t.Fatalf("opened = %+v %v", opened, err)
	}
	opened.Close()
	if _, err := host.OpenLocalFile(directory); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("directory: %v", err)
	}
	if err := os.Symlink(file, filepath.Join(directory, "link.png")); err != nil {
		t.Fatal(err)
	}
	if _, err := host.OpenLocalFile(filepath.Join(directory, "link.png")); err == nil {
		t.Fatal("a final symlink was followed")
	}
	if err := exec.Command("mkfifo", filepath.Join(directory, "fifo")).Run(); err == nil {
		if _, err := host.OpenLocalFile(filepath.Join(directory, "fifo")); err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("fifo: %v", err)
		}
	}
	big := filepath.Join(directory, "big.bin")
	if err := os.WriteFile(big, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(big, 25*1024*1024+1); err != nil {
		t.Fatal(err)
	}
	if _, err := host.OpenLocalFile(big); err == nil || !strings.Contains(err.Error(), "25 MiB") {
		t.Fatalf("big: %v", err)
	}
	if got := host.UploadName("bad\x1bname/../x.txt"); got != "x.txt" {
		t.Fatalf("uploadName = %q", got)
	}
}

func TestLauncherFilePickerBrowsesThisComputerChoosesAFileEscapeCancels(t *testing.T) {
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{"sub/inner.txt": "x", "top.txt": "y"} {
		if err := os.WriteFile(filepath.Join(directory, path), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out := &recorder{columns: 80, rows: 20}
	ui := host.NewTrustedUi(out, "http://server.test")
	type picked struct {
		path string
		ok   bool
	}
	pick := func(start string) chan picked {
		result := make(chan picked, 1)
		go func() {
			path, ok := ui.Pick("Choose a file to upload", start)
			result <- picked{path, ok}
		}()
		waitActive(t, ui)
		return result
	}
	chosen := pick(directory)
	if text := out.text(); !strings.Contains(text, "sub/") || !strings.Contains(text, "top.txt") {
		t.Fatalf("listing = %q", text)
	}
	ui.HandleInput("\x1b[B")
	ui.HandleInput("\r")
	if !strings.Contains(out.text(), "inner.txt") {
		t.Fatal("the subdirectory was not opened")
	}
	for _, char := range "inn" {
		ui.HandleInput(string(char))
	}
	ui.HandleInput("\x1b[B")
	ui.HandleInput("\r")
	select {
	case got := <-chosen:
		if !got.ok || got.path != filepath.Join(directory, "sub", "inner.txt") {
			t.Fatalf("picked = %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no pick")
	}
	cancelled := pick(directory)
	ui.HandleInput("\x1b")
	if got := <-cancelled; got.ok {
		t.Fatalf("cancelled pick = %+v", got)
	}
}

func startUploadServer(t *testing.T) (*p2test.Fixture, string) {
	t.Helper()
	fixture := p2test.StartFixture(t, hosts.ServerOptions{})
	sessions, err := p2test.HoldIn(fixture.Browser(), sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	object, _ := created.(*delta.JsonObject)
	return fixture, object.Value("id").(string)
}

func TestUploadsTheBoundSessionFixedRouteAndOriginConfirmationFirstNoRedirectsCancellable(t *testing.T) {
	directory := t.TempDir()
	file := filepath.Join(directory, "notes.txt")
	if err := os.WriteFile(file, []byte("hello upload"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture, session := startUploadServer(t)
	address := mustURL("http://127.0.0.1:" + itoa(fixture.Server.Port))
	attach := actionC(t, host.LauncherActions(host.LauncherActionOptions{URL: address, Credentials: host.Credentials{Cookie: fixture.Owner().Cookie}, Role: "owner"})["attach"])
	if attach.Consent(map[string]any{"path": file}, host.ConsentContext{Origin: address}) != nil {
		t.Fatal("no session bound")
	}
	bound := &host.GrantTarget{Session: session, Conversation: 1}
	if attach.Consent(map[string]any{"path": 5.0}, host.ConsentContext{Origin: address, Target: bound}) != nil {
		t.Fatal("a numeric path was accepted")
	}
	grants := host.NewGrants("x")
	deniedCtx, denied := contextFor(grants, session, false, nil)
	if got, err := attach.Run(map[string]any{"path": file}, deniedCtx); got != nil || err != nil {
		t.Fatalf("declined upload: %v %v", got, err)
	}
	if !containsAll(denied.shown[0], "notes.txt", "12 B", session) {
		t.Fatalf("facts shown = %q", denied.shown)
	}
	// The session's uploads, as the server mirrors them.
	mirror, err := p2test.HoldIn(fixture.Browser(), filescontract.FileSummaries, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	uploads := func() any {
		space, err := mirror.Call("of", delta.JsonObjectOf("session", session), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		return record2(space)["uploads"]
	}
	if uploads() != nil {
		t.Fatal("nothing uploaded without confirmation")
	}
	okCtx, _ := contextFor(grants, session, true, nil)
	uploaded, err := attach.Run(map[string]any{"path": file}, okCtx)
	if err != nil {
		t.Fatal(err)
	}
	file1, _ := uploaded.(host.UploadedFile)
	if file1.Name != "notes.txt" {
		t.Fatalf("uploaded = %+v", uploaded)
	}
	newest, _ := record2(uploads())["newest"].([]any)
	if len(newest) != 1 || record2(newest[0])["path"] != file1.ID+"-notes.txt" || record2(newest[0])["key"] != file1.ID {
		t.Fatalf("the mirror: %v", newest)
	}
	dirCtx, _ := contextFor(grants, session, true, nil)
	if _, err := attach.Run(map[string]any{"path": directory}, dirCtx); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("directory upload: %v", err)
	}

	// A redirecting or slow server: refused, and a cancelled grant aborts the upload.
	var mu sync.Mutex
	mode := "redirect"
	release := make(chan struct{})
	var origins []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		origins = append(origins, r.Header.Get("Origin"))
		current := mode
		mu.Unlock()
		if current == "redirect" {
			http.Redirect(w, r, "https://evil.test/upload", http.StatusTemporaryRedirect)
			return
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	other := mustURL(server.URL)
	remote := actionC(t, host.LauncherActions(host.LauncherActionOptions{URL: other, Role: "owner"})["attach"])
	redirectCtx, _ := contextFor(grants, session, true, nil)
	if _, err := remote.Run(map[string]any{"path": file}, redirectCtx); err == nil {
		t.Fatal("a redirect was followed")
	}
	if !slices.Equal(origins, []string{host.OriginOf(other)}) {
		t.Fatalf("origins = %v (exact Origin)", origins)
	}
	mu.Lock()
	mode = "slow"
	mu.Unlock()
	cancelGrants := host.NewGrants("x")
	slowCtx, _ := contextFor(cancelGrants, session, true, nil)
	done := make(chan error, 1)
	go func() {
		_, err := remote.Run(map[string]any{"path": file}, slowCtx)
		done <- err
	}()
	poll(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(origins) == 2 })
	cancelGrants.Cancel(slowCtx.Grant.ID)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the cancelled upload succeeded")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a cancelled grant did not abort the upload")
	}
}

func TestCtrlVAnImageUploadsAfterConfirmationTextIsPastedAsTypedFilesGoThroughTheFacts(t *testing.T) {
	fixture, session := startUploadServer(t)
	address := mustURL("http://127.0.0.1:" + itoa(fixture.Server.Port))
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 1, 2, 3}
	var mu sync.Mutex
	clipboard := struct {
		image []byte
		text  string
	}{image: png}
	paste := actionK(t, host.LauncherActions(host.LauncherActionOptions{
		URL: address, Credentials: host.Credentials{Cookie: fixture.Owner().Cookie}, Role: "owner",
		ReadClipboard: &host.ClipboardReaders{
			Files: func() []string { return nil },
			Image: func() *host.ClipboardImage {
				mu.Lock()
				defer mu.Unlock()
				if clipboard.image == nil {
					return nil
				}
				return &host.ClipboardImage{Bytes: clipboard.image, MimeType: "image/png"}
			},
			Text: func() string { mu.Lock(); defer mu.Unlock(); return clipboard.text },
		},
	})["paste.key"])
	if paste.Key != "app.clipboard.pasteImage" {
		t.Fatalf("key = %q", paste.Key)
	}
	type asked struct {
		kind    string
		payload any
	}
	var calls []asked
	ask := func(kind string, payload any) (any, error) {
		calls = append(calls, asked{kind, payload})
		return map[string]any{"added": true}, nil
	}
	grants := host.NewGrants("x")
	declinedCtx, _ := contextFor(grants, session, false, func(c *host.ActionContext) { c.Ask = ask })
	if err := paste.RunKey(declinedCtx); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Fatalf("declined: nothing uploaded or attached: %v", calls)
	}
	confirmedCtx, confirmed := contextFor(grants, session, true, func(c *host.ActionContext) { c.Ask = ask })
	if err := paste.RunKey(confirmedCtx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(confirmed.shown[0], "image/png, 11 B") || len(calls) == 0 || calls[0].kind != "attached" {
		t.Fatalf("shown %q calls %v", confirmed.shown, calls)
	}
	if file, _ := calls[0].payload.(*host.UploadedFile); file == nil || !regexp.MustCompile(`^clipboard-.*\.png$`).MatchString(file.Name) {
		t.Fatalf("attached = %+v", calls[0].payload)
	}
	mu.Lock()
	clipboard.image, clipboard.text = nil, "pasted\x1b]52;c;x\x07 text"
	mu.Unlock()
	var inputs []string
	textCtx, _ := contextFor(grants, session, true, func(c *host.ActionContext) { c.SendInput = func(data string) { inputs = append(inputs, data) } })
	if err := paste.RunKey(textCtx); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(inputs, []string{"\x1b[200~pasted]52;c;x text\x1b[201~"}) {
		t.Fatalf("inputs = %q", inputs)
	}
}

func record2(value any) map[string]any {
	switch v := value.(type) {
	case map[string]any:
		return v
	case *delta.JsonObject:
		out := map[string]any{}
		if v == nil {
			return out
		}
		for _, key := range v.Keys() {
			out[key] = v.Value(key)
		}
		return out
	}
	return map[string]any{}
}

func itoa(n int) string { return strconvItoa(n) }
