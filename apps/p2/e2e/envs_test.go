// Ports test/p2/envs.test.ts (Pi main c23b9ec1) through the full server: "envs are owner-only, observed, retain valid config,
// browse local folders and refuse missing remote workers", and the parts of the SSH case that the compiled-in plugins reach
// (a session created on the remote environment, its worker's connection, relocation there and back, the users the Envs sheet
// lists, forgetting the host key, a forced delete). What Pi's case also drives (bash/write/send_file tools, the workspace,
// uploads, prompt resources, sharing confirmation, terminals, subagents) belongs to the plugins not ported yet; each of those
// ports adds its step to its own test.

package e2e_test

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	envscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/envs/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	statuscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/env"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

func envObject(t *testing.T, text string) *delta.JsonObject {
	t.Helper()
	value, err := chordjson.Decode([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return value.(*delta.JsonObject)
}

func rowsOf(value delta.JsonValue) []any { rows, _ := value.([]any); return rows }

func TestEnvsAreOwnerOnlyObservedRetainValidConfigBrowseLocalFoldersAndRefuseMissingRemoteWorkers(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{})
	browser := fixture.Browser()
	envs, err := p2test.HoldIn(browser, envscontract.Envs, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	gpu := `{"type":"ssh","host":"box"}`
	if _, err := envs.Call("save", delta.JsonObjectOf("name", "gpu", "config", envObject(t, gpu)), p2test.Background); err != nil {
		t.Fatal(err)
	}
	// The answer follows its state: the saved environment is listed when the call resolves.
	until := func(source p2test.JSONSource, predicate func(delta.JsonValue) bool) {
		t.Helper()
		if _, err := p2test.Until(source, predicate, 0); err != nil {
			t.Fatal(err)
		}
	}
	hostIs := func(want string) func(delta.JsonValue) bool {
		return func(value delta.JsonValue) bool {
			rows := rowsOf(value)
			return len(rows) > 0 && object(object(rows[0]).Value("config")).Value("host") == want
		}
	}
	if rows := rowsOf(func() delta.JsonValue { v, _ := envs.Member("envs").Value(); return v }()); len(rows) != 1 || object(rows[0]).Value("name") != "gpu" {
		t.Fatalf("the saved environment is not listed when the call resolves: %v", rows)
	}
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(fixture.DataDir, "envs.json"), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"gpu":{"type":"ssh","host":"changed"}}`)
	until(envs.Member("envs"), hostIs("changed"))
	write("{")
	until(envs.Member("failure"), func(value delta.JsonValue) bool { return value != nil })
	listing, err := envs.Call("listDirectory", delta.JsonObjectOf("path", fixture.Workspace), p2test.Background)
	if err != nil || object(listing).Value("path") != fixture.Workspace {
		t.Fatalf("listDirectory = %v, %v", listing, err)
	}
	until(envs.Member("envs"), hostIs("changed"))
	write("{}")
	until(envs.Member("failure"), func(value delta.JsonValue) bool { return value == nil })

	guest := p2runtime.GuestPrincipal{ID: "guest", Name: "Unverified", Home: "unknown", Sessions: map[string]p2runtime.GuestMode{"unknown": p2runtime.GuestFull}}
	guestRef, err := fixture.As(guest).Hold(p2test.Background, envscontract.Envs, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	p2test.ExpectChord(t, p2runtime.WhenLive(p2test.Background, guestRef), data.CodeForbidden)
	guestRef.Release()

	// Remote owners manage keys too (Pi Mobile's rule): generate, then read the public key back.
	if _, err := exec.LookPath("ssh-keygen"); err == nil {
		tunnel, err := p2test.HoldIn(fixture.As(p2runtime.OwnerPrincipal{Access: p2runtime.AccessRelay}), envscontract.Envs, delta.NewJsonObject(0), "owner")
		if err != nil {
			t.Fatal(err)
		}
		generated, err := tunnel.Call("generateKey", delta.JsonObjectOf("name", "remote-key"), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		publicKey, _ := object(generated).Value("publicKey").(string)
		if !strings.HasPrefix(publicKey, "ssh-ed25519 ") {
			t.Fatalf("public key = %q", publicKey)
		}
		if read, err := tunnel.Call("publicKey", delta.JsonObjectOf("name", "remote-key"), p2test.Background); err != nil || read != publicKey {
			t.Errorf("publicKey = %v, %v", read, err)
		}
		tunnel.Release()
		if _, err := os.Stat(filepath.Join(fixture.DataDir, "ssh", "keys", "remote-key")); err != nil {
			t.Error(err)
		}
	}

	// A session on an environment that is not configured: its worker refuses to start, and nothing runs locally instead.
	created, err := fixture.Server.Host.Root(func(ctx host.Ctx) (any, error) {
		catalog, err := ctx.Get(sessionscontract.Catalog, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			return nil, err
		}
		return catalog.Call("create", delta.JsonObjectOf("env", "missing", "cwd", fixture.Workspace, "title", "Missing remote",
			"model", delta.JsonObjectOf("provider", "faux", "modelId", "faux-1"), "thinkingLevel", "high"), ctx)
	}, p2test.Background, host.RootOptions{})
	if err != nil {
		t.Fatal(err)
	}
	chat, err := browser.Hold(p2test.Background, chatcontract.Chat, delta.JsonObjectOf("session", object(created).Value("id"), "conversation", float64(1)), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// A failed start leaves its holders waiting (failed until Retry).
	failure, err := p2test.FailureOf(chat)
	if err != nil {
		t.Fatal(err)
	}
	if text := strings.ToLower(failure.Error()); !slices.ContainsFunc([]string{"not configured", "refusing local", "unavailable", "exited", "failed to start"}, func(part string) bool { return strings.Contains(text, part) }) {
		t.Errorf("the missing environment's chat failed with %q", failure)
	}
	chat.Release()
	if _, err := os.Stat(filepath.Join(fixture.Workspace, "created.txt")); err == nil {
		t.Error("something ran locally")
	}
}

// startSshd starts an sshd on a free port with a user's key list at authorized, home as the session's HOME.
func startSshd(t *testing.T, root, home string) (port int, authorized string) {
	t.Helper()
	sshd, err := exec.LookPath("sshd")
	if err != nil {
		if _, statErr := os.Stat("/usr/sbin/sshd"); statErr != nil {
			t.Skip("no local sshd")
		}
		sshd = "/usr/sbin/sshd"
	}
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("no ssh-keygen")
	}
	if _, err := env.LocalDaemon(); err != nil {
		t.Skipf("no packaged pi-env daemon: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port = listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	hostKey := filepath.Join(root, "host_key")
	if output, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", hostKey).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v: %s", err, output)
	}
	authorized = filepath.Join(root, "authorized_keys")
	if err := os.WriteFile(authorized, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	config := strings.Join([]string{
		fmt.Sprintf("Port %d", port), "ListenAddress 127.0.0.1", "HostKey " + hostKey, "AuthorizedKeysFile " + authorized,
		"PidFile " + filepath.Join(root, "sshd.pid"), "PasswordAuthentication no", "KbdInteractiveAuthentication no",
		"StrictModes no", "SetEnv HOME=" + home, "",
	}, "\n")
	configPath := filepath.Join(root, "sshd_config")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	server := exec.Command(sshd, "-D", "-e", "-f", configPath)
	if err := server.Start(); err != nil {
		t.Skipf("sshd does not start here: %v", err)
	}
	t.Cleanup(func() {
		_ = server.Process.Kill()
		_ = server.Wait()
	})
	if err := p2test.Poll(func() (bool, error) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		if err != nil {
			return false, nil
		}
		_ = conn.Close()
		return true, nil
	}, 10*time.Second, "local sshd"); err != nil {
		t.Skipf("sshd does not listen here: %v", err)
	}
	return port, authorized
}

func TestASessionOnAnSSHEnvironmentRunsItsWorkerThereMovesAndLeavesItsUsersListed(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	port, authorized := startSshd(t, root, home)
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	fixture := p2test.StartFixture(t, hosts.ServerOptions{Idle: 1500 * time.Millisecond})
	browser := fixture.Browser()
	envs, err := p2test.HoldIn(browser, envscontract.Envs, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	key, err := envs.Call("generateKey", delta.JsonObjectOf("name", "box"), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authorized, []byte(object(key).Value("publicKey").(string)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := delta.JsonObjectOf("type", "ssh", "host", "127.0.0.1", "port", float64(port), "user", current.Username, "identityFile", object(key).Value("identityFile"))
	if _, err := envs.Call("save", delta.JsonObjectOf("name", "box", "config", config), p2test.Background); err != nil {
		t.Fatal(err)
	}
	untrusted, err := envs.Call("test", delta.JsonObjectOf("name", "box"), p2test.Background)
	if err != nil || object(object(untrusted).Value("hostKey")).Value("state") != "unknown" {
		t.Fatalf("the host key is trusted before the owner accepted it: %v, %v", untrusted, err)
	}
	if _, err := envs.Call("acceptHostKey", delta.JsonObjectOf("name", "box", "lines", object(object(untrusted).Value("hostKey")).Value("lines")), p2test.Background); err != nil {
		t.Fatal(err)
	}
	trusted, err := envs.Call("test", delta.JsonObjectOf("name", "box"), p2test.Background)
	if err != nil || object(trusted).Value("ok") != true || object(trusted).Value("home") != home {
		t.Fatalf("trusted test = %v, %v", trusted, err)
	}

	sessions, err := p2test.HoldIn(browser, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("env", "box", "cwd", "~/project", "createDirectory", true), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	session := object(created)
	id := session.Value("id").(string)
	project := filepath.Join(home, "project")
	if session.Value("env") != "box" || session.Value("cwd") != project {
		t.Fatalf("the session = %v, want env box at %s", session, project)
	}

	// The worker connects over SSH and answers: the turn runs on the environment.
	chat, err := p2test.HoldIn(browser, chatcontract.Chat, delta.JsonObjectOf("session", id, "conversation", float64(1)), "participant")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Call("send", delta.JsonObjectOf("text", "hello box", "requestId", "ssh-1"), p2test.Background); err != nil {
		t.Fatal(err)
	}
	answered := func(text string) func(delta.JsonValue) bool {
		return func(view delta.JsonValue) bool {
			return view != nil && strings.Contains(answerText(lastAssistant(view)), text)
		}
	}
	if _, err := p2test.Until(chat.Member("view"), answered("Echo: hello box"), 60*time.Second); err != nil {
		t.Fatal(err)
	}

	// Moving to this machine and back happens in place: a viewer's Chat stays live.
	statuses, err := p2test.HoldIn(browser, statuscontract.SessionStatus, delta.NewJsonObject(0), "reader")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p2test.Until(statuses.Member("statuses"), func(value delta.JsonValue) bool {
		entry := status(value, id)
		return entry != nil && entry.Value("tasks") == float64(0)
	}, 60*time.Second); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(root, "local")
	if err := os.Mkdir(local, 0o755); err != nil {
		t.Fatal(err)
	}
	cwdIs := func(want string) func(delta.JsonValue) bool {
		return func(view delta.JsonValue) bool {
			agent := object(object(object(view).Value("docs")).Value("pi.agent"))
			return agent != nil && agent.Value("cwd") == want
		}
	}
	if _, err := sessions.Call("relocate", delta.JsonObjectOf("id", id, "cwd", local, "env", nil), p2test.Background); err != nil {
		t.Fatal(err)
	}
	if _, err := p2test.Until(chat.Member("view"), cwdIs(local), 60*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Call("relocate", delta.JsonObjectOf("id", id, "cwd", "~/project", "env", "box"), p2test.Background); err != nil {
		t.Fatal(err)
	}
	if _, err := p2test.Until(chat.Member("view"), cwdIs(project), 60*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Call("send", delta.JsonObjectOf("text", "back on box", "requestId", "ssh-2"), p2test.Background); err != nil {
		t.Fatal(err)
	}
	if _, err := p2test.Until(chat.Member("view"), answered("Echo: back on box"), 60*time.Second); err != nil {
		t.Fatal(err)
	}
	chat.Release()

	// The environment cannot be deleted while a session, archived or not, uses it; the sheet lists what blocks it.
	if _, err := sessions.Call("archive", delta.JsonObjectOf("id", id), p2test.Background); err != nil {
		t.Fatal(err)
	}
	_, err = envs.Call("delete", delta.JsonObjectOf("name", "box"), p2test.Background)
	if err == nil || !strings.Contains(err.Error(), "still use") {
		t.Fatalf("delete while a session uses it = %v", err)
	}
	if _, err := envs.Call("forgetHostKey", delta.JsonObjectOf("name", "box"), p2test.Background); err != nil {
		t.Fatal(err)
	}
	forgotten, err := envs.Call("test", delta.JsonObjectOf("name", "box"), p2test.Background)
	if err != nil || object(object(forgotten).Value("hostKey")).Value("state") != "unknown" {
		t.Fatalf("after forgetting the host key: %v, %v", forgotten, err)
	}
	rows, err := p2test.Until(envs.Member("envs"), func(value delta.JsonValue) bool {
		for _, row := range rowsOf(value) {
			if object(row).Value("name") == "box" {
				return len(rowsOf(object(row).Value("users"))) > 0
			}
		}
		return false
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rowsOf(rows) {
		if object(row).Value("name") != "box" {
			continue
		}
		users := rowsOf(object(row).Value("users"))
		if len(users) != 1 || object(users[0]).Value("id") != id || object(users[0]).Value("archived") != true {
			t.Errorf("users = %v, want [%s archived]", users, id)
		}
	}
	if _, err := envs.Call("delete", delta.JsonObjectOf("name", "box", "force", true), p2test.Background); err != nil {
		t.Fatal(err)
	}
	if _, err := p2test.Until(envs.Member("envs"), func(value delta.JsonValue) bool {
		for _, row := range rowsOf(value) {
			if object(row).Value("name") == "box" {
				return false
			}
		}
		return true
	}, 0); err != nil {
		t.Fatal(err)
	}
}
