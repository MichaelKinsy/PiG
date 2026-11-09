// Ports test/p2/send-file.test.ts (Pi main c23b9ec1): the `send_file` tool snapshots a cwd-relative file into the session's
// files, `/download` serves it, and `SentFiles` previews it for viewers without a cookie. The `previewText` case is a unit
// test of the plugin (`plugins/send-file/worker_test.go`).

package e2e_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	sendfilecontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/send-file/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	"github.com/MichaelKinsy/PiG/chord/delta"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/files"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/send-file"
)

func TestSendFileFakeProvider(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	var rebuilds p2test.Rebuilds
	fixture := p2test.StartFixture(t, hosts.ServerOptions{BuildRevision: rebuilds.Revision})
	owner := fixture.Browser()
	sessions, err := p2test.HoldIn(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	session := object(created).Value("id").(string)
	chat, err := p2test.HoldIn(owner, chatcontract.Chat, delta.JsonObjectOf("session", session, "conversation", float64(1)), "participant")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(fixture.Workspace, "created.txt")
	count := 0
	// send asks the fake provider to call `send_file` on created.txt: the tool's result message.
	send := func(t *testing.T) *delta.JsonObject {
		t.Helper()
		count++
		if _, err := chat.Call("send", delta.JsonObjectOf("text", "send-file", "requestId", "file-"+strconv.Itoa(count)), p2test.Background); err != nil {
			t.Fatal(err)
		}
		view, err := p2test.Until(chat.Member("view"), func(value delta.JsonValue) bool {
			return value != nil && len(toolResults(value, "send_file")) == count && !running(value)
		}, 0)
		if err != nil {
			t.Fatal(err)
		}
		return toolResults(view, "send_file")[count-1]
	}
	write := func(content []byte) {
		t.Helper()
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("snapshots a cwd-relative file and serves the original after deletion/reload", func(t *testing.T) {
		write([]byte("immutable sentinel"))
		result := send(t)
		if result.Value("isError") != false {
			t.Fatalf("result: %v", result)
		}
		info := object(result.Value("details"))
		if info.Value("name") != "created.txt" || info.Value("size") != float64(18) {
			t.Fatalf("details = %v", info)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.Server.Reload([]string{"send-file"}, context.Background()); err != nil {
			t.Fatal(err)
		}
		response, err := fixture.HTTP("/download?session="+session+"&id="+info.Value("id").(string), p2test.HTTPOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if response.Status != 200 || response.Body != "immutable sentinel" {
			t.Fatalf("download: %d %q", response.Status, response.Body)
		}
		if !regexp.MustCompile(`attachment`).MatchString(response.Headers.Get("Content-Disposition")) {
			t.Fatalf("Content-Disposition = %q", response.Headers.Get("Content-Disposition"))
		}
	})

	t.Run("previews sent text for viewers that have no cookie (the TUI), and refuses binary text", func(t *testing.T) {
		files, err := p2test.HoldIn(fixture.Browser(), sendfilecontract.SentFiles, delta.JsonObjectOf("session", session), "reader")
		if err != nil {
			t.Fatal(err)
		}
		write([]byte("line one\nline two"))
		text := send(t)
		shown, err := files.Call("preview", delta.JsonObjectOf("id", object(text.Value("details")).Value("id")), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		if want := delta.JsonObjectOf("name", "created.txt", "size", float64(17), "text", "line one\nline two", "truncated", false); !jsonEqual(shown, want) {
			t.Fatalf("preview = %v", shown)
		}
		write([]byte{0x50, 0x4b, 0x00, 0x01})
		binary := send(t)
		refused, err := files.Call("preview", delta.JsonObjectOf("id", object(binary.Value("details")).Value("id")), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		if object(refused).Value("text") != nil || object(refused).Value("size") != float64(4) {
			t.Fatalf("binary preview = %v", refused)
		}
		if _, err := files.Call("preview", delta.JsonObjectOf("id", "00000000-0000-0000-0000-000000000000"), p2test.Background); err == nil || !strings.Contains(strings.ToLower(err.Error()), "not found") {
			t.Fatalf("unknown id: %v", err)
		}
		files.Release()
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("rejects directories and oversized files", func(t *testing.T) {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if got := send(t).Value("isError"); got != true {
			t.Fatalf("a directory: isError = %v", got)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Truncate(25*1024*1024 + 1); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		result := send(t)
		encoded, _ := json.Marshal(result.Value("content"))
		if result.Value("isError") != true || !strings.Contains(string(encoded), "25 MiB") {
			t.Fatalf("oversized: %v", result)
		}
	})
}
