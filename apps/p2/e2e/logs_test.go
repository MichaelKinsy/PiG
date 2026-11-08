// Ports the server halves of test/p2/logs.test.ts (the owner-only Logs case and the detached host's startup failure) and
// test/p2/ui-logs.test.ts (the second case's server half: a failed worker start is cached and Retry recovers). The browser
// halves of ui-logs.test.ts are the web lane's (`browserTests`).
//
// The detached preload case of logs.test.ts starts Node with `--import log-preload.ts` before a main module that may not
// parse; a Go program has no such stage. Its contract is tested here: a detached host that fails at startup (an error or a
// panic) leaves `terminals.logs.json` with the redacted error, and the owner's Logs imports it. The host is this test binary
// re-run as a child.

package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	logscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/logs/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	statuscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/logs"
)

const detachedChildEnv = "P2_TEST_DETACHED_LOG_CHILD"

// detachedChild runs this test binary as the detached host of the startup-failure case (TestMain calls it first).
func detachedChild() {
	if directory := os.Getenv(detachedChildEnv); directory != "" {
		runDetachedChild(directory, os.Getenv(detachedChildEnv+"_MODE"))
		os.Exit(0)
	}
}

// runDetachedChild is a detached host that fails at startup: it publishes its logs, then fails as the mode says.
func runDetachedChild(directory, mode string) {
	p2runtime.ProcessLogs.Redactor.AddSecret("fixture-startup-secret")
	published := p2runtime.CapturePublishedLogs("terminals", directory, true)
	defer func() {
		if recovered := recover(); recovered != nil {
			published.Fatal(recovered, debug.Stack())
			os.Exit(1)
		}
	}()
	if mode == "panic" {
		panic("fixture-startup-marker fixture-startup-secret")
	}
	published.Fatal(os.ErrInvalid, nil)
	os.Exit(1)
}

func TestDetachedHostStoresFatalStartupErrorsRedacted(t *testing.T) {
	for _, mode := range []string{"panic", "error"} {
		directory := t.TempDir()
		command := exec.Command(os.Args[0])
		command.Env = append(os.Environ(), detachedChildEnv+"="+directory, detachedChildEnv+"_MODE="+mode)
		err := command.Run()
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
			t.Fatalf("%s: the host exited with %v, want 1", mode, err)
		}
		text, err := os.ReadFile(filepath.Join(directory, "terminals.logs.json"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(text), "fixture-startup-secret") {
			t.Fatalf("%s: the snapshot keeps the secret: %s", mode, text)
		}
		want := "fixture-startup-marker"
		if mode == "error" {
			want = "invalid argument"
		}
		if !strings.Contains(string(text), want) {
			t.Fatalf("%s: the snapshot lacks %q: %s", mode, want, text)
		}
	}
}

func TestOwnerOnlyLogsExposeLivePerSourceStateAndImportBoundedDetachedSnapshots(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{})
	owner := fixture.Browser()
	source := "session:fixture-" + time.Now().Format("150405.000000")
	logs, err := p2test.HoldIn(owner, logscontract.Logs, delta.JsonObjectOf("source", source), "owner")
	if err != nil {
		t.Fatal(err)
	}
	server, err := p2test.HoldIn(owner, logscontract.Logs, delta.JsonObjectOf("source", "server"), "owner")
	if err != nil {
		t.Fatal(err)
	}
	hasText := func(text string) func(delta.JsonValue) bool {
		return func(value delta.JsonValue) bool {
			for _, each := range list(value) {
				if strings.Contains(object(each).Value("text").(string), text) {
					return true
				}
			}
			return false
		}
	}
	p2runtime.ProcessLogs.Append(source, p2runtime.LogError, "Unknown p2 plugin: settings")
	if _, err := p2test.Until(logs.Member("lines"), hasText("Unknown p2 plugin"), 0); err != nil {
		t.Fatal(err)
	}
	if lines, _ := server.Member("lines").Value(); hasText("Unknown p2 plugin")(lines) {
		t.Fatal("the server's log shows another source's line")
	}
	if _, err := p2test.Until(logs.Member("sources"), func(value delta.JsonValue) bool {
		for _, each := range list(value) {
			if object(each).Value("id") == source {
				return true
			}
		}
		return false
	}, 0); err != nil {
		t.Fatal(err)
	}
	notices, err := p2test.HoldIn(owner, p2runtime.LogNotices, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	p2runtime.ProcessLogs.Redactor.AddSecret("fixture-notice-secret")
	p2runtime.ProcessLogs.Notice(source, "Failed facet fixture-notice-secret")
	seen, err := p2test.Until(notices.Member("notices"), func(value delta.JsonValue) bool {
		for _, each := range list(value) {
			if object(each).Value("source") == source {
				return true
			}
		}
		return false
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var notice *delta.JsonObject
	for _, each := range list(seen) {
		if object(each).Value("source") == source {
			notice = object(each)
		}
	}
	if notice.Value("message") != "Failed facet [redacted]" {
		t.Fatalf("notice = %v", notice)
	}
	if _, err := notices.Call("dismiss", delta.JsonObjectOf("id", notice.Value("id")), p2test.Background); err != nil {
		t.Fatal(err)
	}
	// The answer follows its state: the notice is gone on the page when the call resolves.
	after, _ := notices.Member("notices").Value()
	for _, each := range list(after) {
		if object(each).Value("id") == notice.Value("id") {
			t.Fatal("the dismissed notice is still listed when dismiss answered")
		}
	}
	terminals, err := p2test.HoldIn(owner, logscontract.Logs, delta.JsonObjectOf("source", "terminals"), "owner")
	if err != nil {
		t.Fatal(err)
	}
	store, err := p2runtime.NewLogStore(p2runtime.LogLimits{})
	if err != nil {
		t.Fatal(err)
	}
	store.Redactor.AddSecret("fixture-detached-secret")
	path := filepath.Join(fixture.DataDir, "terminals.logs.json")
	stop := p2runtime.PersistLogSource("terminals", path, store)
	store.Append("terminals", p2runtime.LogEvent, "detached-host fixture-detached-secret")
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if written, _ := os.ReadFile(path); strings.Contains(string(written), "fixture-detached-secret") {
		t.Fatal("the published snapshot keeps the secret")
	}
	if _, err := p2test.Until(terminals.Member("lines"), hasText("detached-host [redacted]"), 0); err != nil {
		t.Fatal(err)
	}
	// Refused: a guest, and a worker, never see logs, notices or the worker control.
	for name, principal := range map[string]p2runtime.Principal{
		"guest":  p2runtime.GuestPrincipal{ID: "g", Name: "Guest", Home: "s", Sessions: map[string]p2runtime.GuestMode{"s": "full"}, Revision: 1},
		"worker": p2runtime.WorkerPrincipal{Session: "s", Generation: 1},
	} {
		client := fixture.As(principal)
		denied, err := p2test.HoldIn(client, logscontract.Logs, delta.JsonObjectOf("source", source), "owner")
		if err == nil {
			_, err = denied.Member("sources").Value()
		}
		if err == nil || !strings.Contains(err.Error(), "forbidden") {
			t.Fatalf("%s: Logs = %v, want forbidden", name, err)
		}
		deniedNotices, err := p2test.HoldIn(client, p2runtime.LogNotices, delta.NewJsonObject(0), "owner")
		if err == nil {
			_, err = deniedNotices.Call("dismiss", delta.JsonObjectOf("id", notice.Value("id")), p2test.Background)
		}
		if err == nil || !strings.Contains(err.Error(), "forbidden") {
			t.Fatalf("%s: LogNotices = %v, want forbidden", name, err)
		}
	}
}

func TestAFailedWorkerStartIsCachedAndRetryRecovers(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{})
	client := fixture.Browser()
	sessions, err := p2test.HoldIn(client, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace, "title", "Broken worker fixture"), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	session := object(created).Value("id").(string)
	status, err := p2test.HoldIn(client, statuscontract.SessionStatus, delta.NewJsonObject(0), "reader")
	if err != nil {
		t.Fatal(err)
	}
	// A start that is refused: the session's environment is not configured (as a stale catalog would leave it).
	setEnv := func(env any) {
		t.Helper()
		if _, err := fixture.Server.Host.Root(func(ctx host.Ctx) (any, error) {
			catalog, err := ctx.Get(sessionscontract.Catalog, delta.NewJsonObject(0), host.GetOptions{})
			if err != nil {
				return nil, err
			}
			return catalog.Call("update", delta.JsonObjectOf("id", session, "env", env), ctx)
		}, p2test.Background, host.RootOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	setEnv("missing")
	p2runtime.ProcessLogs.Append("session:another-worker", p2runtime.LogEvent, "other-worker-only-marker")
	// Holding the session's chat asks for its worker, whose start fails.
	if _, err := fixture.Server.Workers().Retry(p2test.Background, session); err == nil {
		t.Log("the first retry did not fail")
	}
	failed, err := p2test.Until(status.Member("statuses"), func(value delta.JsonValue) bool {
		return object(object(value).Value(session)) != nil && object(object(value).Value(session)).Value("error") != nil
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	entry := object(object(failed).Value(session))
	if entry.Value("running") != false || !strings.Contains(entry.Value("error").(string), "not configured") {
		t.Fatalf("status = %v", entry)
	}
	generation := fixture.Server.Workers().Generation(session)
	time.Sleep(time.Second)
	if got := fixture.Server.Workers().Generation(session); got != generation {
		t.Fatalf("failed starts are cached, not repeatedly spawned: generation %d -> %d", generation, got)
	}
	lines := p2runtime.ProcessLogs.Lines("session:" + session)
	joined := ""
	for _, line := range lines {
		joined += line.Text + "\n"
	}
	if !strings.Contains(joined, "not configured") || strings.Contains(joined, "other-worker-only-marker") {
		t.Fatalf("the worker's log: %q", joined)
	}
	notified := false
	for _, notice := range p2runtime.ProcessLogs.Notices() {
		notified = notified || strings.Contains(notice.Message, "not configured")
	}
	if !notified {
		t.Fatal("no owner notice names the failed start")
	}
	setEnv(nil)
	if _, err := fixture.Server.Workers().Retry(p2test.Background, session); err != nil {
		t.Fatal(err)
	}
	if _, err := p2test.Until(status.Member("statuses"), func(value delta.JsonValue) bool {
		entry := object(object(value).Value(session))
		return entry != nil && entry.Value("running") == true && entry.Value("error") == nil
	}, 0); err != nil {
		t.Fatal(err)
	}
	if fixture.Server.Workers().Generation(session) <= generation {
		t.Fatal("the generation did not advance after Retry")
	}
}
