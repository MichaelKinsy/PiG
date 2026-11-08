// Ports test/p2/files.test.ts (Pi main c23b9ec1): uploads (POST /upload into the session's files, attached to messages by ID
// and materialized) and download snapshots (GET /download), owner and guests; the server's mirror of the session's file
// summaries.
//
// Not ported: the artifact assertions of "answers at once for an archived or unknown session" (the `artifacts` plugin and its
// `/artifact` route are not part of this module yet; the archived/unknown download, upload and snapshot assertions are).

package e2e_test

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	filescontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/files/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	sharingcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sharing/contract"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/files"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/sharing"
)

const pngBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="

// userMessageContent is the content of the first user message of a conversation view (the `p2.input` entries' models).
func userMessageContent(view delta.JsonValue) []any {
	entries, _ := object(view).Value("entries").([]any)
	for _, entry := range entries {
		if object(entry).Value("kind") != "p2.input" {
			continue
		}
		model, _ := object(entry).Value("model").([]any)
		if len(model) > 0 {
			content, _ := object(model[0]).Value("content").([]any)
			return content
		}
	}
	return nil
}

func TestFilesFakeProvider(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{AllowedHosts: []string{"share.test"}})
	local := "http://127.0.0.1:" + itoa(fixture.Server.Port)
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
	session, other := create("Files"), create("Other")
	chat, err := p2test.HoldIn(owner, chatcontract.Chat, delta.JsonObjectOf("session", session, "conversation", float64(1)), "participant")
	if err != nil {
		t.Fatal(err)
	}
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
	upload := func(id, name string, body string, headers map[string]string) p2test.HTTPResult {
		t.Helper()
		result, err := p2test.HTTP(fixture.Server.Port, "/upload?"+url.Values{"session": {id}, "name": {name}}.Encode(),
			p2test.HTTPOptions{Method: http.MethodPost, Headers: headers, Body: strings.NewReader(body)})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	sameOrigin := map[string]string{"Origin": local}
	snapshot := func(id, path, name string) (*delta.JsonObject, error) {
		saved, err := fixture.Server.Host.Root(func(ctx host.Ctx) (any, error) {
			store, err := ctx.Get(filescontract.DownloadStore, delta.NewJsonObject(0), host.GetOptions{})
			if err != nil {
				return nil, err
			}
			args := delta.JsonObjectOf("session", id, "path", path)
			if name != "" {
				args.Set("name", name)
			}
			return store.Call("save", args, ctx)
		}, p2test.Background, host.RootOptions{})
		if err != nil {
			return nil, err
		}
		return object(saved), nil
	}
	mustSnapshot := func(id, path, name string) *delta.JsonObject {
		t.Helper()
		saved, err := snapshot(id, path, name)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	guestCookie := func(mode string) string {
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
		body, _ := chordjson.Marshal(delta.JsonObjectOf("token", token, "name", mode))
		result, err := p2test.HTTP(fixture.Server.Port, "/join", p2test.HTTPOptions{Method: http.MethodPost,
			Headers: map[string]string{"Host": "share.test", "Origin": "https://share.test", "Content-Type": "application/json"}, Body: strings.NewReader(string(body))})
		if err != nil || result.Status != 200 {
			t.Fatalf("join: %v %+v", err, result)
		}
		cookie, _, _ := strings.Cut(result.Headers.Get("Set-Cookie"), ";")
		return cookie
	}
	tunnel := func(cookie string) map[string]string {
		return map[string]string{"Host": "share.test", "Origin": "https://share.test", "Cookie": cookie}
	}

	t.Run("stores uploads in the session's files, same-origin only; the server mirrors their summary", func(t *testing.T) {
		if got := upload(session, "a.txt", "x", nil).Status; got != 403 {
			t.Fatalf("no Origin: %d", got)
		}
		if got := upload(session, "a.txt", "x", map[string]string{"Origin": "https://evil.test"}).Status; got != 403 {
			t.Fatalf("evil Origin: %d", got)
		}
		if got := upload("missing", "a.txt", "x", sameOrigin).Status; got != 400 {
			t.Fatalf("missing session: %d", got)
		}
		if got := upload(session, "", "x", sameOrigin).Status; got != 400 {
			t.Fatalf("empty name: %d", got)
		}
		response := upload(session, "../../evil notes.txt", "uploaded-sentinel\n", sameOrigin)
		if response.Status != 200 {
			t.Fatalf("upload: %d %s", response.Status, response.Body)
		}
		parsed, err := chordjson.Decode([]byte(response.Body))
		if err != nil {
			t.Fatal(err)
		}
		uploaded := object(parsed)
		if uploaded.Value("name") != "evil_notes.txt" || uploaded.Value("size") != float64(18) || uploaded.Value("mimeType") != nil || len(uploaded.Keys()) != 3 {
			t.Fatalf("uploaded = %s", response.Body)
		}
		if entries, _ := os.ReadDir(fixture.Workspace); len(entries) != 0 {
			t.Fatalf("something was written into the project: %v", entries)
		}
		// The worker pushed the space's summary to the server before the upload answered.
		mirror, err := p2test.HoldIn(fixture.Browser(), filescontract.FileSummaries, delta.NewJsonObject(0), "owner")
		if err != nil {
			t.Fatal(err)
		}
		spaces, err := mirror.Call("of", delta.JsonObjectOf("session", session), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		uploads := object(object(spaces).Value("uploads"))
		newest := list(uploads.Value("newest"))
		if uploads.Value("files") != float64(1) || len(newest) != 1 || object(newest[0]).Value("source") != "owner" || object(newest[0]).Value("key") != uploaded.Value("id") {
			t.Fatalf("the mirror: %v", spaces)
		}
	})

	t.Run("attaches uploads to messages by ID, materialized on this machine: paths listed, images inline", func(t *testing.T) {
		text := parseObject(t, upload(session, "notes.txt", "hello", sameOrigin).Body)
		png, _ := base64.StdEncoding.DecodeString(pngBase64)
		image := parseObject(t, upload(session, "dot.png", string(png), sameOrigin).Body)
		if image.Value("mimeType") != "image/png" {
			t.Fatalf("image = %v", image)
		}
		foreign := parseObject(t, upload(other, "private.txt", "private", sameOrigin).Body)
		for _, attachments := range [][]any{{"../../etc/passwd"}, {foreign.Value("id")}} {
			_, err := chat.Call("send", delta.JsonObjectOf("text", "x", "requestId", "bad", "attachments", attachments), p2test.Background)
			if err == nil || !strings.Contains(err.Error(), "Unknown attachment") {
				t.Fatalf("send with %v: %v", attachments, err)
			}
		}
		if _, err := chat.Call("send", delta.JsonObjectOf("text", "", "requestId", "files", "attachments", []any{text.Value("id"), image.Value("id")}), p2test.Background); err != nil {
			t.Fatal(err)
		}
		view, err := p2test.Until(chat.Member("view"), func(value delta.JsonValue) bool { return value != nil && userMessageContent(value) != nil }, 0)
		if err != nil {
			t.Fatal(err)
		}
		content := userMessageContent(view)
		// This machine's copies: under the data directory's `uploads/<session>/`, private, outside the project.
		textPath := filepath.Join(fixture.DataDir, "uploads", session, text.Value("id").(string)+"-notes.txt")
		imagePath := filepath.Join(fixture.DataDir, "uploads", session, image.Value("id").(string)+"-dot.png")
		listed := false
		for _, part := range content {
			if object(part).Value("type") == "text" {
				body := object(part).Value("text").(string)
				listed = listed || (strings.Contains(body, "<attachments>") && strings.Contains(body, textPath) && strings.Contains(body, imagePath))
			}
		}
		if !listed {
			t.Fatalf("no attachment note with %s and %s in %v", textPath, imagePath, content)
		}
		if written, err := os.ReadFile(textPath); err != nil || string(written) != "hello" {
			t.Fatalf("copy = %q, %v", written, err)
		}
		if stat, err := os.Stat(textPath); err != nil || stat.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v, %v", stat, err)
		}
		// The fake model accepts images, so the PNG is also inlined.
		inline := false
		for _, part := range content {
			if object(part).Value("type") == "image" {
				inline = object(part).Value("mimeType") == "image/png" && object(part).Value("data") == pngBase64
			}
		}
		if !inline {
			t.Fatalf("the PNG is not inlined: %v", content)
		}
	})

	t.Run("serves download snapshots to the owner", func(t *testing.T) {
		source := filepath.Join(fixture.DataDir, "report.txt")
		if err := os.WriteFile(source, []byte("snapshot"), 0o600); err != nil {
			t.Fatal(err)
		}
		info := mustSnapshot(session, source, "résumé\"\r\n.txt")
		if info.Value("name") != "résumé_.txt" {
			t.Fatalf("name = %v", info.Value("name"))
		}
		if err := os.WriteFile(source, []byte("changed later"), 0o600); err != nil {
			t.Fatal(err)
		}
		path := "/download?session=" + session + "&id=" + info.Value("id").(string)
		downloaded := get(path, nil)
		if downloaded.Status != 200 || downloaded.Body != "snapshot" {
			t.Fatalf("download = %d %q", downloaded.Status, downloaded.Body)
		}
		if disposition := downloaded.Headers.Get("Content-Disposition"); !strings.HasPrefix(disposition, `attachment; filename="r_sum__.txt"`) {
			t.Fatalf("Content-Disposition = %q", disposition)
		}
		if kind := downloaded.Headers.Get("Content-Type"); kind != "text/plain; charset=utf-8" {
			t.Fatalf("previewable types keep their type: %q", kind)
		}
		if head := get(path, nil, http.MethodHead); head.Status != 200 || head.Body != "" {
			t.Fatalf("HEAD = %d %q", head.Status, head.Body)
		}
		if got := get(path, map[string]string{"Sec-Fetch-Site": "cross-site"}).Status; got != 403 {
			t.Fatalf("a cross-site page downloads with the owner's access: %d", got)
		}
		if got := get("/download?session="+other+"&id="+info.Value("id").(string), nil).Status; got != 404 {
			t.Fatalf("another session's snapshot: %d", got)
		}
		if got := get("/download?session="+session+"&id=../x", nil).Status; got != 404 {
			t.Fatalf("a path as an id: %d", got)
		}
		// Audio and video are seekable.
		audio := filepath.Join(fixture.DataDir, "clip.mp3")
		if err := os.WriteFile(audio, []byte("0123456789"), 0o600); err != nil {
			t.Fatal(err)
		}
		clip := mustSnapshot(session, audio, "")
		clipPath := "/download?session=" + session + "&id=" + clip.Value("id").(string)
		partial := get(clipPath, map[string]string{"Range": "bytes=2-4"})
		if partial.Status != 206 || partial.Body != "234" || partial.Headers.Get("Content-Range") != "bytes 2-4/10" {
			t.Fatalf("range = %d %q %q", partial.Status, partial.Body, partial.Headers.Get("Content-Range"))
		}
		if got := get(clipPath, map[string]string{"Range": "bytes=20-"}).Status; got != 416 {
			t.Fatalf("an unsatisfiable range: %d", got)
		}
	})

	t.Run("lets guests download from their session, and only read/write guests upload", func(t *testing.T) {
		source := filepath.Join(fixture.DataDir, "shared.txt")
		if err := os.WriteFile(source, []byte("shared"), 0o600); err != nil {
			t.Fatal(err)
		}
		shared := mustSnapshot(session, source, "")
		hidden := mustSnapshot(other, source, "")
		viewer := guestCookie("view")
		downloaded := get("/download?session="+session+"&id="+shared.Value("id").(string), tunnel(viewer))
		if downloaded.Status != 200 || downloaded.Body != "shared" {
			t.Fatalf("a guest's download = %d %q", downloaded.Status, downloaded.Body)
		}
		if got := get("/download?session="+other+"&id="+hidden.Value("id").(string), tunnel(viewer)).Status; got != 403 {
			t.Fatalf("another session: %d", got)
		}
		if got := get("/download?session="+session+"&id="+shared.Value("id").(string), tunnel("")).Status; got != 403 {
			t.Fatalf("not signed in: %d", got)
		}
		if got := upload(session, "a.txt", "x", tunnel(viewer)).Status; got != 403 {
			t.Fatalf("read-only upload: %d", got)
		}
		writer := guestCookie("full")
		if uploaded := upload(session, "guest.txt", "from guest", tunnel(writer)); uploaded.Status != 200 {
			t.Fatalf("guest upload: %d %s", uploaded.Status, uploaded.Body)
		}
		if got := upload(other, "a.txt", "x", tunnel(writer)).Status; got != 403 {
			t.Fatalf("a guest uploads to another session: %d", got)
		}
	})

	t.Run("answers at once for an archived or unknown session, never waiting for its worker", func(t *testing.T) {
		archived := create("Archived")
		source := filepath.Join(fixture.DataDir, "archived.txt")
		if err := os.WriteFile(source, []byte("archived"), 0o600); err != nil {
			t.Fatal(err)
		}
		sent := mustSnapshot(archived, source, "")
		download := "/download?session=" + archived + "&id=" + sent.Value("id").(string)
		if got := get(download, nil).Status; got != 200 {
			t.Fatalf("download before archiving: %d", got)
		}
		if _, err := sessions.Call("archive", delta.JsonObjectOf("id", archived), p2test.Background); err != nil {
			t.Fatal(err)
		}
		if _, err := p2test.Until(sessions.Member("sessions"), func(value delta.JsonValue) bool {
			for _, each := range list(value) {
				if object(each).Value("id") == archived {
					return object(each).Value("archivedAt") != nil
				}
			}
			return false
		}, 0); err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		if got := get(download, nil).Status; got != 404 {
			t.Fatalf("archived download: %d", got)
		}
		if got := get(download, nil, http.MethodHead).Status; got != 404 {
			t.Fatalf("archived HEAD: %d", got)
		}
		if got := upload(archived, "late.txt", "x", sameOrigin).Status; got != 409 {
			t.Fatalf("archived upload: %d", got)
		}
		if got := get("/download?session=00000000-0000-4000-8000-000000000000&id="+sent.Value("id").(string), nil).Status; got != 404 {
			t.Fatalf("unknown session: %d", got)
		}
		if _, err := snapshot(archived, source, ""); err == nil || !strings.Contains(err.Error(), "archived") {
			t.Fatalf("snapshot of an archived session: %v", err)
		}
		// A worker start that is refused would take a minute per request.
		if took := time.Since(started); took >= 5*time.Second {
			t.Fatalf("answered in %v", took)
		}
		if _, err := sessions.Call("restore", delta.JsonObjectOf("id", archived), p2test.Background); err != nil {
			t.Fatal(err)
		}
		if err := p2test.Poll(func() (bool, error) { return get(download, nil).Status == 200, nil }, 30*time.Second, "served again once restored"); err != nil {
			t.Fatal(err)
		}
	})
}

func parseObject(t *testing.T, body string) *delta.JsonObject {
	t.Helper()
	parsed, err := chordjson.Decode([]byte(body))
	if err != nil || object(parsed) == nil {
		t.Fatalf("body %q: %v", body, err)
	}
	return object(parsed)
}

func itoa(value int) string { return strconv.Itoa(value) }
