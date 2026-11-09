// Ports test/p2/workspace.test.ts (Pi main c23b9ec1): the workspace plugin over a temporary git repository under the fake
// provider: the queries, unknown and private conversations, the place query, the published changes (kept across a reload),
// the HTTP file stream with media ranges, bounded uploads, owner and guest access, review drafts as a prompt, and the
// worker's watch lease.

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	sharingcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sharing/contract"
	workspacecontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/workspace/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/workspace"
)

func TestWorkspaceFakeProvider(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{AllowedHosts: []string{"share.test"}})
	origin := "http://127.0.0.1:" + itoa(fixture.Server.Port)
	tunnel := map[string]string{"Host": "share.test", "Origin": "https://share.test"}
	if err := os.Mkdir(filepath.Join(fixture.Workspace, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(fixture.Workspace, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("src/hello.ts", "const hello = 'before';\n")
	write(".gitignore", "ignored.txt\n")
	git := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = fixture.Workspace
		command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		output, err := command.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return string(output)
	}
	git("init", "-q")
	git("add", "src/hello.ts", ".gitignore")
	git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null", "commit", "-qm", "Initial")
	hash := strings.TrimSpace(git("rev-parse", "HEAD"))
	write("src/hello.ts", "const hello = 'after';\n")
	write("new.md", "# Untracked hello\n")
	write("ignored.txt", "hello ignored\n")
	write("clip.mp3", "0123456789")
	if err := os.Symlink(fixture.DataDir, filepath.Join(fixture.Workspace, "private")); err != nil {
		t.Fatal(err)
	}
	browser := fixture.Browser()
	sessions, err := p2test.HoldIn(browser, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
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
	session, other := create("Workspace"), create("Other")
	workspace, err := p2test.HoldIn(browser, workspacecontract.Workspace, delta.JsonObjectOf("session", session, "conversation", float64(1)), "reader")
	if err != nil {
		t.Fatal(err)
	}
	query := func(request *delta.JsonObject) *delta.JsonObject {
		t.Helper()
		answer, err := workspace.Call("query", request, p2test.Background)
		if err != nil {
			t.Fatalf("query %v: %v", request, err)
		}
		return object(answer)
	}
	rejects := func(request *delta.JsonObject, pattern string) {
		t.Helper()
		_, err := workspace.Call("query", request, p2test.Background)
		if err == nil || (pattern != "" && !strings.Contains(err.Error(), pattern)) {
			t.Fatalf("query %v: %v, want an error matching %q", request, err, pattern)
		}
	}
	url := func(path string, id ...string) string {
		identity := session
		if len(id) > 0 {
			identity = id[0]
		}
		return "/workspace-file?" + urlValues("session", identity, "conversation", "1", "path", path)
	}
	get := func(path string, headers map[string]string, method ...string) p2test.HTTPResult {
		t.Helper()
		options := p2test.HTTPOptions{Headers: headers}
		if len(method) > 0 {
			options.Method = method[0]
		}
		result, err := fixture.HTTP(path, options)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	upload := func(name string, body []byte, path string, headers map[string]string, id ...string) p2test.HTTPResult {
		t.Helper()
		identity := session
		if len(id) > 0 {
			identity = id[0]
		}
		result, err := fixture.HTTP("/workspace-upload?"+urlValues("session", identity, "conversation", "1", "path", path, "name", name),
			p2test.HTTPOptions{Method: http.MethodPost, Headers: headers, Body: bytes.NewReader(body)})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	sameOrigin := map[string]string{"Origin": origin}
	joinGuest := func(mode string) string {
		t.Helper()
		sharing, err := p2test.HoldIn(fixture.Browser(), sharingcontract.Sharing, delta.JsonObjectOf("session", session), "owner")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sharing.Call("link", delta.JsonObjectOf("mode", mode), p2test.Background); err != nil {
			t.Fatal(err)
		}
		links, err := p2test.Until(sharing.Member("links"), func(value delta.JsonValue) bool {
			for _, each := range list(value) {
				if object(each).Value("mode") == mode {
					return true
				}
			}
			return false
		}, 0)
		if err != nil {
			t.Fatal(err)
		}
		var token any
		for _, each := range list(links) {
			if object(each).Value("mode") == mode {
				token = object(each).Value("token")
			}
		}
		body, _ := chordjson.Marshal(delta.JsonObjectOf("token", token, "name", "Workspace "+mode))
		result, err := p2test.HTTP(fixture.Server.Port, "/join", p2test.HTTPOptions{Method: http.MethodPost,
			Headers: map[string]string{"Host": "share.test", "Origin": "https://share.test", "Content-Type": "application/json"}, Body: strings.NewReader(string(body))})
		if err != nil || result.Status != 200 {
			t.Fatalf("join: %v %+v", err, result)
		}
		cookie, _, _ := strings.Cut(result.Headers.Get("Set-Cookie"), ";")
		return cookie
	}
	withCookie := func(cookie string) map[string]string {
		return map[string]string{"Host": "share.test", "Origin": "https://share.test", "Cookie": cookie}
	}
	_ = tunnel

	t.Run("lists folders, ignored annotations, all filenames, text, changes, search and history", func(t *testing.T) {
		list := query(delta.JsonObjectOf("kind", "list", "path", ""))
		if list.Value("kind") != "list" {
			t.Fatalf("list = %v", list)
		}
		var ignored any
		for _, each := range listOf(list.Value("entries")) {
			entry := object(each)
			if entry.Value("name") == ".git" {
				t.Fatal("the listing shows .git")
			}
			if entry.Value("name") == "ignored.txt" {
				ignored = entry.Value("ignored")
			}
		}
		if ignored != true {
			t.Fatalf("ignored.txt not annotated: %v", list)
		}
		files := query(delta.JsonObjectOf("kind", "files"))
		paths := stringsOf(files.Value("paths"))
		if files.Value("kind") != "files" || !contains(paths, "src/hello.ts") || contains(paths, "ignored.txt") {
			t.Fatalf("files = %v", files)
		}
		file := query(delta.JsonObjectOf("kind", "file", "path", "src/hello.ts"))
		if text, _ := file.Value("text").(string); file.Value("kind") != "file" || !strings.Contains(text, "after") {
			t.Fatalf("file = %v", file)
		}
		status := query(delta.JsonObjectOf("kind", "status"))
		changed := false
		for _, each := range listOf(status.Value("changes")) {
			changed = changed || object(each).Value("path") == "src/hello.ts"
		}
		if status.Value("kind") != "status" || status.Value("repo") == nil || status.Value("repo") == false || !changed {
			t.Fatalf("status = %v", status)
		}
		diff := query(delta.JsonObjectOf("kind", "diff", "path", "src/hello.ts", "full", true))
		if patch, _ := diff.Value("patch").(string); diff.Value("kind") != "diff" || !strings.Contains(patch, "+const hello = 'after'") {
			t.Fatalf("diff = %v", diff)
		}
		added := query(delta.JsonObjectOf("kind", "diff", "path", "new.md"))
		if patch, _ := added.Value("patch").(string); added.Value("kind") != "diff" || !strings.Contains(patch, "+# Untracked hello") {
			t.Fatalf("added = %v", added)
		}
		search := query(delta.JsonObjectOf("kind", "search", "path", "", "text", "hello", "include", "*.{ts,md}", "exclude", "new.md"))
		matches := listOf(search.Value("matches"))
		if search.Value("kind") != "search" || len(matches) != 1 || object(matches[0]).Value("path") != "src/hello.ts" {
			t.Fatalf("search = %v", search)
		}
		log := query(delta.JsonObjectOf("kind", "log", "skip", float64(0), "limit", float64(30)))
		commits := listOf(log.Value("commits"))
		if log.Value("kind") != "log" || len(commits) == 0 || object(commits[0]).Value("hash") != hash {
			t.Fatalf("log = %v", log)
		}
		commit := query(delta.JsonObjectOf("kind", "commit", "commit", hash))
		touched := false
		for _, each := range listOf(commit.Value("changes")) {
			touched = touched || object(each).Value("path") == "src/hello.ts"
		}
		if commit.Value("kind") != "commit" || commit.Value("message") != "Initial" || !touched {
			t.Fatalf("commit = %v", commit)
		}
		old := query(delta.JsonObjectOf("kind", "file", "path", "src/hello.ts", "commit", hash))
		if text, _ := old.Value("text").(string); old.Value("kind") != "file" || !strings.Contains(text, "before") {
			t.Fatalf("old = %v", old)
		}
		rejects(delta.JsonObjectOf("kind", "file", "path", "../data/settings.json"), "Invalid")
		rejects(delta.JsonObjectOf("kind", "file", "path", ".git/config"), "Invalid")
		rejects(delta.JsonObjectOf("kind", "list", "path", "private"), "")
		if _, err := workspace.Call("query", delta.JsonObjectOf("kind", "list", "path", "private"), p2test.Background); err == nil ||
			(!strings.Contains(err.Error(), "private") && !strings.Contains(err.Error(), "leaves")) {
			t.Fatalf("private list: %v", err)
		}
		rejects(delta.JsonObjectOf("kind", "log", "skip", float64(-1), "limit", float64(1000)), "")
	})

	t.Run("rejects unknown conversations and sessions rooted in private application data", func(t *testing.T) {
		unknown, err := fixture.Browser().Hold(p2test.Background, workspacecontract.Workspace, delta.JsonObjectOf("session", session, "conversation", float64(99999)), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		// A failed open leaves its holders waiting (it would heal): the failure names the cause.
		failure, err := p2test.FailureOf(unknown)
		if err != nil || !strings.Contains(failure.Error(), "Unknown conversation") {
			t.Fatalf("unknown conversation: %v, %v", failure, err)
		}
		unknown.Release()
		created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.DataDir, "title", "Private"), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		private, err := fixture.Browser().Hold(p2test.Background, workspacecontract.Workspace, delta.JsonObjectOf("session", object(created).Value("id"), "conversation", float64(1)), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		failure, err = p2test.FailureOf(private)
		if err != nil || !strings.Contains(failure.Error(), "private") {
			t.Fatalf("private session: %v, %v", failure, err)
		}
		private.Release()
	})

	t.Run("answers where it is open, without a waiting or failed move", func(t *testing.T) {
		place := query(delta.JsonObjectOf("kind", "place"))
		encoded, _ := chordjson.Marshal(place)
		want, _ := chordjson.Marshal(delta.JsonObjectOf("kind", "place", "location", delta.JsonObjectOf("env", nil, "cwd", fixture.Workspace)))
		if string(encoded) != string(want) {
			t.Fatalf("place = %s, want %s", encoded, want)
		}
	})

	t.Run("publishes changes while held, and keeps the same watch across reload", func(t *testing.T) {
		after := func(than delta.JsonValue) func(delta.JsonValue) bool {
			return func(value delta.JsonValue) bool {
				number, ok := value.(float64)
				return ok && (than == nil || number > than.(float64))
			}
		}
		initial, err := p2test.Until(workspace.Member("changes"), after(nil), 0)
		if err != nil {
			t.Fatal(err)
		}
		write("watched.txt", "one")
		if _, err := p2test.Until(workspace.Member("changes"), after(initial), 0); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.Server.Reload([]string{"workspace"}, p2test.Background); err != nil {
			t.Fatal(err)
		}
		revision, err := p2test.Until(workspace.Member("changes"), after(nil), 0)
		if err != nil {
			t.Fatal(err)
		}
		write("watched.txt", "two")
		if _, err := p2test.Until(workspace.Member("changes"), after(revision), 0); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("streams bytes, HEAD and media ranges; denies private paths and cross-site reads", func(t *testing.T) {
		bytesResult := get(url("src/hello.ts"), nil)
		if bytesResult.Status != 200 || bytesResult.Body != "const hello = 'after';\n" {
			t.Fatalf("bytes = %+v", bytesResult)
		}
		// As Pi Mobile: only media keeps its type; anything else is an opaque download.
		if got := bytesResult.Headers.Get("Content-Type"); got != "application/octet-stream" {
			t.Fatalf("content-type = %q", got)
		}
		head := get(url("clip.mp3"), nil, http.MethodHead)
		if head.Status != 200 || head.Headers.Get("Content-Type") != "audio/mpeg" || head.Headers.Get("Content-Length") != "10" || head.Body != "" {
			t.Fatalf("head = %+v", head)
		}
		for _, each := range [][2]string{{"bytes=2-4", "234"}, {"bytes=-3", "789"}, {"bytes=7-", "789"}} {
			response := get(url("clip.mp3"), map[string]string{"Range": each[0]})
			if response.Status != 206 || response.Body != each[1] {
				t.Fatalf("range %s = %+v", each[0], response)
			}
		}
		if got := get(url("clip.mp3"), map[string]string{"Range": "bytes=20-"}).Status; got != 416 {
			t.Fatalf("range past the end: %d", got)
		}
		if got := get(url("src/hello.ts"), map[string]string{"Sec-Fetch-Site": "cross-site"}).Status; got != 403 {
			t.Fatalf("cross-site: %d", got)
		}
		// A whole file over 25 MiB is refused (Pi Mobile's limit); a sparse file keeps the fixture small on disk.
		huge := filepath.Join(fixture.Workspace, "huge.bin")
		write("huge.bin", "")
		if err := os.Truncate(huge, 25*1024*1024+1); err != nil {
			t.Fatal(err)
		}
		refused := get(url("huge.bin"), nil)
		if refused.Status != 413 || refused.Body != "File too large" {
			t.Fatalf("huge = %d %q", refused.Status, refused.Body)
		}
		_ = os.Remove(huge)
		if got := get(url(".git/config"), nil).Status; got != 404 {
			t.Fatalf(".git/config: %d", got)
		}
		if got := get(url("private/settings.json"), nil).Status; got != 404 {
			t.Fatalf("private/settings.json: %d", got)
		}
		transfer, err := fixture.Browser().Hold(p2test.Background, workspacecontract.WorkspaceTransfer, delta.JsonObjectOf("session", session, "conversation", float64(1)), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		p2test.ExpectChord(t, p2runtime.WhenLive(p2test.Background, transfer), data.CodeForbidden)
	})

	t.Run("streams bounded uploads into a folder, including empty files, without overwriting", func(t *testing.T) {
		payload := bytes.Repeat([]byte("x"), 2*1024*1024+11)
		response := upload("large.txt", payload, "src", sameOrigin)
		if response.Status != 200 {
			t.Fatalf("upload = %d %s", response.Status, response.Body)
		}
		if written, err := os.ReadFile(filepath.Join(fixture.Workspace, "src/large.txt")); err != nil || !bytes.Equal(written, payload) {
			t.Fatalf("written: %d bytes, %v", len(written), err)
		}
		streamed := get(url("src/large.txt"), nil)
		if streamed.Status != 200 || streamed.Body != string(payload) {
			t.Fatalf("streamed: %d, %d bytes", streamed.Status, len(streamed.Body))
		}
		capped := query(delta.JsonObjectOf("kind", "file", "path", "src/large.txt"))
		if text, _ := capped.Value("text").(string); capped.Value("truncated") != true || len(text) != 1024*1024 {
			t.Fatalf("capped = truncated %v, %d", capped.Value("truncated"), len(text))
		}
		if got := upload("large.txt", []byte("overwrite"), "src", sameOrigin).Status; got != 400 {
			t.Fatalf("overwrite: %d", got)
		}
		if written, _ := os.ReadFile(filepath.Join(fixture.Workspace, "src/large.txt")); !bytes.Equal(written, payload) {
			t.Fatal("the overwrite changed the file")
		}
		var racing [2]p2test.HTTPResult
		var group sync.WaitGroup
		for index, body := range []string{"first", "second"} {
			group.Add(1)
			go func() {
				defer group.Done()
				racing[index] = upload("racing.txt", []byte(body), "src", sameOrigin)
			}()
		}
		group.Wait()
		statuses := []int{racing[0].Status, racing[1].Status}
		if !(statuses[0] == 200 && statuses[1] == 400 || statuses[0] == 400 && statuses[1] == 200) {
			t.Fatalf("racing statuses = %v", statuses)
		}
		winner := "first"
		if racing[1].Status == 200 {
			winner = "second"
		}
		if written, _ := os.ReadFile(filepath.Join(fixture.Workspace, "src/racing.txt")); string(written) != winner {
			t.Fatalf("racing.txt = %q, want %q", written, winner)
		}
		if got := upload("empty.txt", nil, "src", sameOrigin).Status; got != 200 {
			t.Fatalf("empty: %d", got)
		}
		if written, err := os.ReadFile(filepath.Join(fixture.Workspace, "src/empty.txt")); err != nil || len(written) != 0 {
			t.Fatalf("empty.txt: %d bytes, %v", len(written), err)
		}
		if got := upload("bad.txt", []byte("x"), "private", sameOrigin).Status; got != 400 {
			t.Fatalf("into private: %d", got)
		}
		if got := upload("../bad.txt", []byte("x"), "", sameOrigin).Status; got != 400 {
			t.Fatalf("../bad.txt: %d", got)
		}
		if got := upload("bad.txt", []byte("x"), "", nil).Status; got != 403 {
			t.Fatalf("no Origin: %d", got)
		}
		if got := upload("oversized.txt", make([]byte, 25*1024*1024+1), "", sameOrigin).Status; got != 400 {
			t.Fatalf("oversized: %d", got)
		}
		entries, _ := os.ReadDir(fixture.Workspace)
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".pi-upload-") {
				t.Fatalf("an upload left %s behind", entry.Name())
			}
		}
	})

	t.Run("only owner and read/write guests access their workspace; HTTP reauthorizes after removal", func(t *testing.T) {
		viewer := joinGuest("view")
		reader, _ := fixture.Client(withCookie(viewer))
		viewing, err := reader.Hold(p2test.Background, workspacecontract.Workspace, delta.JsonObjectOf("session", session, "conversation", float64(1)), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		p2test.ExpectChord(t, p2runtime.WhenLive(p2test.Background, viewing), data.CodeForbidden)
		if got := get(url("new.md"), withCookie(viewer)).Status; got != 403 {
			t.Fatalf("viewer download: %d", got)
		}
		writer := joinGuest("full")
		client, _ := fixture.Client(withCookie(writer))
		full, err := p2test.HoldIn(client, workspacecontract.Workspace, delta.JsonObjectOf("session", session, "conversation", float64(1)), "reader")
		if err != nil {
			t.Fatal(err)
		}
		answered, err := full.Call("query", delta.JsonObjectOf("kind", "file", "path", "new.md"), p2test.Background)
		if text, _ := object(answered).Value("text").(string); err != nil || object(answered).Value("kind") != "file" || !strings.Contains(text, "hello") {
			t.Fatalf("guest file = %v, %v", answered, err)
		}
		if got := get(url("new.md"), withCookie(writer)).Status; got != 200 {
			t.Fatalf("writer download: %d", got)
		}
		if got := upload("guest.txt", []byte("guest"), "", withCookie(writer)).Status; got != 200 {
			t.Fatalf("writer upload: %d", got)
		}
		if got := get(url("new.md", other), withCookie(writer)).Status; got != 403 {
			t.Fatalf("another session's download: %d", got)
		}
		if got := upload("guest.txt", []byte("x"), "", withCookie(writer), other).Status; got != 403 {
			t.Fatalf("another session's upload: %d", got)
		}
		sharing, err := p2test.HoldIn(fixture.Browser(), sharingcontract.Sharing, delta.JsonObjectOf("session", session), "owner")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sharing.Call("link", delta.JsonObjectOf("mode", "full", "reset", true), p2test.Background); err != nil {
			t.Fatal(err)
		}
		if got := get(url("new.md"), withCookie(writer)).Status; got != 403 {
			t.Fatalf("after the link was reset: %d", got)
		}
	})

	t.Run("sends quoted browser review drafts as an ordinary attributed prompt", func(t *testing.T) {
		chat, err := p2test.HoldIn(fixture.Browser(), chatcontract.Chat, delta.JsonObjectOf("session", session, "conversation", float64(1)), "participant")
		if err != nil {
			t.Fatal(err)
		}
		defer chat.Release()
		text := "Review comments:\n\nsrc/hello.ts:1 @" + hash[:8] + "\n> const hello = 'after';\nPlease use a clearer name"
		if _, err := chat.Call("send", delta.JsonObjectOf("text", text, "requestId", "review"), p2test.Background); err != nil {
			t.Fatal(err)
		}
		view, err := p2test.Until(chat.Member("view"), func(value delta.JsonValue) bool {
			for _, entry := range listOf(object(value).Value("entries")) {
				encoded, _ := json.Marshal(entryText(entry))
				if object(entry).Value("kind") == "pi.assistant" && strings.Contains(string(encoded), "clearer name") {
					return true
				}
			}
			return false
		}, 0)
		if err != nil {
			t.Fatal(err)
		}
		if encoded, _ := chordjson.Marshal(view); !strings.Contains(string(encoded), "Review comments:") {
			t.Fatal("the view does not hold the review comments")
		}
	})
}

func TestWorkspaceHoldsKeepAnIdleWorkerAliveAndReleasingTheLastReleasesTheWatch(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{Idle: 100 * time.Millisecond})
	browser := fixture.Browser()
	sessions, err := p2test.HoldIn(browser, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace, "title", "Watch lease"), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	session := object(created).Value("id").(string)
	workspace, err := p2test.HoldIn(browser, workspacecontract.Workspace, delta.JsonObjectOf("session", session, "conversation", float64(1)), "reader")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Call("query", delta.JsonObjectOf("kind", "list", "path", ""), p2test.Background); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if state, err := p2test.UntilNative(workspace.State(), func(*host.RefState) bool { return true }, time.Second); err != nil || state.Kind != host.RefStateLive {
		t.Fatalf("the hold is %v, %v after the idle time", state, err)
	}
	workers, err := fixture.Server.Host.Get(context.Background(), p2runtime.Hosts, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer workers.Release()
	workspace.Release()
	if _, err := p2test.Until(workers.Member("workers"), func(value delta.JsonValue) bool {
		state := object(object(value).Value(session))
		return state != nil && state.Value("running") == false
	}, 0); err != nil {
		t.Fatal(err)
	}
}

func urlValues(pairs ...string) string {
	values := url.Values{}
	for at := 0; at+1 < len(pairs); at += 2 {
		values.Set(pairs[at], pairs[at+1])
	}
	return values.Encode()
}

func listOf(value any) []any {
	items, _ := value.([]any)
	return items
}

func stringsOf(value any) []string {
	var out []string
	for _, each := range listOf(value) {
		if text, ok := each.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func contains(items []string, wanted string) bool {
	for _, item := range items {
		if item == wanted {
			return true
		}
	}
	return false
}

// entryText is an entry as JSON-encodable values.
func entryText(entry any) any {
	encoded, _ := chordjson.Marshal(entry)
	return string(encoded)
}
