package envs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/envs/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	statuscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/contract"
	subagentscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

func TestMain(m *testing.M) { p2test.Main(m) }

var (
	owner      = p2runtime.OwnerPrincipal{Access: p2runtime.AccessLocal}
	relayOwner = p2runtime.OwnerPrincipal{Access: p2runtime.AccessRelay}
)

// stoppedWorkers records the `Hosts.stop` calls of the restart contribution.
type stoppedWorkers struct {
	mu      sync.Mutex
	stopped []string
}

func (s *stoppedWorkers) add(session string) {
	s.mu.Lock()
	s.stopped = append(s.stopped, session)
	s.mu.Unlock()
}

func (s *stoppedWorkers) list() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.stopped...)
}

// envsFixture is the real envs server facet beside stubs of the catalog, the child store, the workers and the statuses.
type envsFixture struct {
	*p2test.TestHost
	dataDir  string
	sessions *p2test.Cell
	children *p2test.Cell
	workers  *p2test.Cell
	statuses *p2test.Cell
	stopped  *stoppedWorkers
}

func newEnvsFixture(t *testing.T) *envsFixture {
	t.Helper()
	p2test.CheckLeaks(t, 0)
	f := &envsFixture{dataDir: t.TempDir(), stopped: &stoppedWorkers{}}
	info := p2test.Stub{Contract: p2runtime.ServerLocal, Factory: func(*delta.JsonObject, p2runtime.Principal, p2test.StubSelf) p2test.StubMembers {
		return p2test.StubMembers{State: map[string]any{"info": delta.JsonObjectOf("app", "/app", "dataDir", f.dataDir, "fake", true, "allowedHosts", []any{}, "port", float64(1))}, Methods: inert("revokeOwners", "revokeGuests", "revokeFrames")}
	}}
	catalog := p2test.Stub{Contract: sessionscontract.Catalog, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
		f.sessions = self.Cell([]any{})
		return p2test.StubMembers{State: map[string]any{"sessions": f.sessions}, Methods: inert("get", "create", "activity", "update")}
	}}
	children := p2test.Stub{Contract: subagentscontract.ChildStore, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
		f.children = self.Cell(delta.NewJsonObject(0))
		return p2test.StubMembers{State: map[string]any{"children": f.children}, Methods: inert("report")}
	}}
	hosts := p2test.Stub{Contract: p2runtime.Hosts, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
		f.workers = self.Cell(delta.NewJsonObject(0))
		return p2test.StubMembers{State: map[string]any{"workers": f.workers}, Methods: map[string]p2test.StubMethod{
			"stop": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
				f.stopped.add(stringOf(objectOf(args).Value("session")))
				return nil, nil
			},
		}}
	}}
	status := p2test.Stub{Contract: statuscontract.StatusStore, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
		f.statuses = self.Cell(delta.NewJsonObject(0))
		return p2test.StubMembers{State: map[string]any{"statuses": f.statuses}, Methods: inert("report")}
	}}
	f.TestHost = p2test.NewTestHost(t, p2test.TestHostOptions{
		Facets: []host.Facet{serverFacet},
		Stubs:  []p2test.Stub{info, catalog, children, hosts, status},
	})
	return f
}

func inert(names ...string) map[string]p2test.StubMethod {
	methods := map[string]p2test.StubMethod{}
	for _, name := range names {
		methods[name] = func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }
	}
	return methods
}

func (f *envsFixture) owner(t *testing.T, principal p2runtime.Principal) *host.Reference {
	t.Helper()
	ref, err := p2test.HoldIn(f.Client(principal), contract.Envs, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ref.Release)
	return ref
}

func (f *envsFixture) write(t *testing.T, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dataDir, "envs.json"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func sessionRow(id string, env any, archived bool) *delta.JsonObject {
	row := delta.JsonObjectOf("id", id, "cwd", "/work", "title", "Session "+id, "createdAt", float64(1), "updatedAt", float64(1),
		"archivedAt", nil, "pinned", false, "model", delta.JsonObjectOf("provider", "faux", "modelId", "faux-1"), "thinkingLevel", "high",
		"main", float64(1), "branches", []any{})
	if env != nil {
		row.Set("env", env)
	}
	if archived {
		row.Set("archivedAt", float64(2))
	}
	return row
}

const sshHost = `{"type":"ssh","host":"box"}`

func TestEnvsAreOwnerOnlyObservedAndRetainTheLastValidConfiguration(t *testing.T) {
	f := newEnvsFixture(t)
	envs := f.owner(t, owner)
	save := func(name, config string) {
		t.Helper()
		if _, err := envs.Call("save", delta.JsonObjectOf("name", name, "config", object(t, config)), p2test.Background); err != nil {
			t.Fatal(err)
		}
	}
	save("gpu", sshHost)
	// The answer follows its state: the saved environment is listed when the call resolves.
	rows, err := p2test.Until(envs.Member("envs"), func(value delta.JsonValue) bool { r, _ := value.([]any); return len(r) == 1 }, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := objectOf(rows.([]any)[0]).Value("name"); got != "gpu" {
		t.Errorf("listed %v", got)
	}

	hostOf := func(value delta.JsonValue) any {
		rows, _ := value.([]any)
		if len(rows) == 0 {
			return nil
		}
		return objectOf(objectOf(rows[0]).Value("config")).Value("host")
	}
	f.write(t, `{"gpu":{"type":"ssh","host":"changed"}}`)
	if _, err := p2test.Until(envs.Member("envs"), func(value delta.JsonValue) bool { return hostOf(value) == "changed" }, 0); err != nil {
		t.Fatal(err)
	}
	f.write(t, "{")
	if _, err := p2test.Until(envs.Member("failure"), func(value delta.JsonValue) bool { return value != nil }, 0); err != nil {
		t.Fatal(err)
	}
	// Local folders are listed while the file is invalid, and the last valid configuration stays.
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	listing, err := envs.Call("listDirectory", delta.JsonObjectOf("path", workspace), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	if got := objectOf(listing).Value("path"); got != workspace {
		t.Errorf("listing path = %v, want %s", got, workspace)
	}
	if dirs, _ := objectOf(listing).Value("directories").([]any); len(dirs) != 1 || dirs[0] != filepath.Join(workspace, "sub") {
		t.Errorf("directories = %v", dirs)
	}
	if _, err := p2test.Until(envs.Member("envs"), func(value delta.JsonValue) bool { return hostOf(value) == "changed" }, 0); err != nil {
		t.Fatal(err)
	}
	f.write(t, "{}")
	if _, err := p2test.Until(envs.Member("failure"), func(value delta.JsonValue) bool { return value == nil }, 0); err != nil {
		t.Fatal(err)
	}

	// A guest sees nothing of it.
	guest := p2runtime.GuestPrincipal{ID: "guest", Name: "Unverified", Home: "unknown", Sessions: map[string]p2runtime.GuestMode{"unknown": p2runtime.GuestFull}}
	ref, err := f.Client(guest).Hold(p2test.Background, contract.Envs, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer ref.Release()
	p2test.ExpectChord(t, p2runtime.WhenLive(p2test.Background, ref), data.CodeForbidden)
	secrets, err := f.Client(guest).Hold(p2test.Background, contract.SandboxSecrets, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer secrets.Release()
	p2test.ExpectChord(t, p2runtime.WhenLive(p2test.Background, secrets), data.CodeForbidden)
}

func TestEveryOwnerManagesKeysAndTheKeyIsNeverPublished(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("no ssh-keygen")
	}
	f := newEnvsFixture(t)
	envs := f.owner(t, relayOwner)
	// Remote owners manage keys too (Pi Mobile's rule): generate, then read the public key back.
	generated, err := envs.Call("generateKey", delta.JsonObjectOf("name", "remote-key"), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	publicKey := stringOf(objectOf(generated).Value("publicKey"))
	if !strings.HasPrefix(publicKey, "ssh-ed25519 ") {
		t.Fatalf("public key = %q", publicKey)
	}
	read, err := envs.Call("publicKey", delta.JsonObjectOf("name", "remote-key"), p2test.Background)
	if err != nil || read != publicKey {
		t.Errorf("publicKey = %v, %v", read, err)
	}
	if none, err := envs.Call("publicKey", delta.JsonObjectOf("name", "nobody"), p2test.Background); err != nil || none != nil {
		t.Errorf("publicKey of no key = %v, %v", none, err)
	}
	keyPath := filepath.Join(f.dataDir, "ssh", "keys", "remote-key")
	if got := objectOf(generated).Value("identityFile"); got != keyPath {
		t.Errorf("identityFile = %v, want %s", got, keyPath)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Error(err)
	}
	// Generating twice never locks you out.
	again, err := envs.Call("generateKey", delta.JsonObjectOf("name", "remote-key"), p2test.Background)
	if err != nil || objectOf(again).Value("publicKey") != publicKey {
		t.Errorf("generating twice = %v, %v", again, err)
	}
}

// Ports remote-env-keys.test.ts (the owner's half: upload, refusals, 0600, adopted identity file, no key material in state).
func TestAnOwnerMayUploadKeysAndOnlyValidOnes(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("no ssh-keygen")
	}
	f := newEnvsFixture(t)
	envs := f.owner(t, relayOwner)
	source := filepath.Join(f.dataDir, "fixture-key")
	if err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", source).Run(); err != nil {
		t.Fatal(err)
	}
	keyBytes, _ := os.ReadFile(source)
	key := string(keyBytes)
	if _, err := envs.Call("save", delta.JsonObjectOf("name", "box", "config", object(t, `{"type":"ssh","host":"fixture.test"}`)), p2test.Background); err != nil {
		t.Fatal(err)
	}
	stored, err := envs.Call("uploadKey", delta.JsonObjectOf("name", "box", "key", key), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	identity := stringOf(objectOf(stored).Value("identityFile"))
	if !strings.HasPrefix(stringOf(objectOf(stored).Value("publicKey")), "ssh-ed25519 ") || identity != filepath.Join(f.dataDir, "ssh", "keys", "box") {
		t.Fatalf("stored = %v", stored)
	}
	if got, _ := os.ReadFile(identity); string(got) != key {
		t.Error("the stored key differs from the uploaded one")
	}
	if info, _ := os.Stat(identity); info.Mode().Perm() != 0o600 {
		t.Errorf("key mode = %v", info.Mode())
	}
	rows, err := p2test.Until(envs.Member("envs"), func(value delta.JsonValue) bool {
		r, _ := value.([]any)
		return len(r) == 1 && objectOf(objectOf(r[0]).Value("config")).Value("identityFile") == identity
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if text := mustMarshal(t, rows); strings.Contains(text, "PRIVATE KEY") {
		t.Error("key material is in the published state")
	}
	upload := func(name, text string) error {
		_, err := envs.Call("uploadKey", delta.JsonObjectOf("name", name, "key", text), p2test.Background)
		return err
	}
	wantError(t, upload("box", "invalid"), "private key")
	if err := upload("box", strings.Repeat("PRIVATE KEY", 2000)); err == nil {
		t.Error("an oversize key was accepted")
	}
	wantError(t, upload("../escape", key), "name")
	protected := filepath.Join(f.dataDir, "protected-key")
	if err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "fixture-passphrase", "-f", protected).Run(); err != nil {
		t.Fatal(err)
	}
	protectedBytes, _ := os.ReadFile(protected)
	err = upload("box", string(protectedBytes))
	if err == nil || (!strings.Contains(err.Error(), "passphrase") && !strings.Contains(strings.ToLower(err.Error()), "valid")) {
		t.Errorf("a protected key = %v", err)
	}
	if got, _ := os.ReadFile(identity); string(got) != key {
		t.Error("a refused upload replaced the stored key")
	}
	entries, _ := os.ReadDir(filepath.Join(f.dataDir, "ssh", "keys"))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".upload-") {
			t.Errorf("left %s", entry.Name())
		}
	}
}

func TestDeletingAnEnvironmentRefusesWhileSessionsUseItUnlessForced(t *testing.T) {
	f := newEnvsFixture(t)
	envs := f.owner(t, owner)
	if _, err := envs.Call("save", delta.JsonObjectOf("name", "box", "config", object(t, sshHost)), p2test.Background); err != nil {
		t.Fatal(err)
	}
	if err := f.sessions.Replace([]any{sessionRow("s1", "box", true), sessionRow("s2", nil, false)}); err != nil {
		t.Fatal(err)
	}
	// A subagent whose own location is the environment counts its session.
	if err := f.children.Replace(delta.JsonObjectOf("s2", []any{delta.JsonObjectOf("location", delta.JsonObjectOf("cwd", "/x", "env", "box"))})); err != nil {
		t.Fatal(err)
	}
	rows, err := p2test.Until(envs.Member("envs"), func(value delta.JsonValue) bool {
		r, _ := value.([]any)
		return len(r) == 1 && objectOf(r[0]).Value("sessions") == float64(2)
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	users, _ := objectOf(rows.([]any)[0]).Value("users").([]any)
	got := map[string]bool{}
	for _, user := range users {
		got[stringOf(objectOf(user).Value("id"))] = objectOf(user).Value("archived") == true
	}
	if len(got) != 2 || !got["s1"] || got["s2"] {
		t.Errorf("users = %v (the archived session counts, flagged)", got)
	}
	_, err = envs.Call("delete", delta.JsonObjectOf("name", "box"), p2test.Background)
	wantError(t, err, `2 sessions still use "box"`)
	if _, err := envs.Call("delete", delta.JsonObjectOf("name", "box", "force", true), p2test.Background); err != nil {
		t.Fatal(err)
	}
	if _, err := p2test.Until(envs.Member("envs"), func(value delta.JsonValue) bool { r, _ := value.([]any); return len(r) == 0 }, 0); err != nil {
		t.Fatal(err)
	}
}

func TestAnEditedEnvironmentStopsItsSessionsWorkersOnceIdle(t *testing.T) {
	f := newEnvsFixture(t)
	envs := f.owner(t, owner)
	for _, each := range [][2]string{{"box", sshHost}, {"other", `{"type":"ssh","host":"other"}`}} {
		name, config := each[0], each[1]
		if _, err := envs.Call("save", delta.JsonObjectOf("name", name, "config", object(t, config)), p2test.Background); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := envs.Call("save", delta.JsonObjectOf("name", "sbx", "config", object(t, `{"type":"gondolin","host":"box"}`)), p2test.Background); err != nil {
		t.Fatal(err)
	}
	f.sessions.Replace([]any{sessionRow("on-box", "box", false), sessionRow("on-sbx", "sbx", false), sessionRow("on-other", "other", false), sessionRow("stopped", "box", false)})
	f.workers.Replace(delta.JsonObjectOf(
		"on-box", delta.JsonObjectOf("running", true, "generation", float64(1)),
		"on-sbx", delta.JsonObjectOf("running", true, "generation", float64(1)),
		"on-other", delta.JsonObjectOf("running", true, "generation", float64(1)),
		"stopped", delta.JsonObjectOf("running", false, "generation", float64(1)),
	))
	// A task is running in on-box; the others are idle.
	f.statuses.Replace(delta.JsonObjectOf("on-box", statusOf(1), "on-sbx", statusOf(0), "on-other", statusOf(0), "stopped", statusOf(0)))
	time.Sleep(200 * time.Millisecond) // the contribution has recorded the revisions
	if got := f.stopped.list(); len(got) != 0 {
		t.Fatalf("stopped %v before any edit", got)
	}
	// Editing the host reaches its sandbox's sessions too.
	if _, err := envs.Call("save", delta.JsonObjectOf("name", "box", "config", object(t, `{"type":"ssh","host":"box","port":2222}`)), p2test.Background); err != nil {
		t.Fatal(err)
	}
	if err := p2test.Poll(func() (bool, error) { return len(f.stopped.list()) == 1, nil }, 20*time.Second, "the idle worker of a session using the sandbox"); err != nil {
		t.Fatal(err)
	}
	if got := f.stopped.list(); got[0] != "on-sbx" {
		t.Errorf("stopped %v, want on-sbx first (idle); on-box has a task running", got)
	}
	// The busy one stops when its tasks end.
	f.statuses.Replace(delta.JsonObjectOf("on-box", statusOf(0), "on-sbx", statusOf(0), "on-other", statusOf(0), "stopped", statusOf(0)))
	if err := p2test.Poll(func() (bool, error) { return len(f.stopped.list()) == 2, nil }, 20*time.Second, "the busy worker once idle"); err != nil {
		t.Fatal(err)
	}
	if got := f.stopped.list(); got[1] != "on-box" {
		t.Errorf("stopped %v", got)
	}
	time.Sleep(300 * time.Millisecond)
	if got := f.stopped.list(); len(got) != 2 {
		t.Errorf("stopped %v: a session of another environment or a stopped worker was stopped", got)
	}
}

func statusOf(tasks float64) *delta.JsonObject { return delta.JsonObjectOf("tasks", tasks) }

func TestSandboxSecretsAreOwnerManagedAndNeverReturnValues(t *testing.T) {
	f := newEnvsFixture(t)
	envs := f.owner(t, owner)
	ref, err := p2test.HoldIn(f.Client(owner), contract.SandboxSecrets, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	defer ref.Release()
	call := func(method string, args *delta.JsonObject) (delta.JsonValue, error) {
		return ref.Call(method, args, p2test.Background)
	}
	// The host first: a sandbox runs on a configured SSH environment.
	for _, each := range [][2]string{{"host", `{"type":"ssh","host":"h.example"}`}, {"box", `{"type":"gondolin","host":"host","allowHosts":["api.github.com"]}`}} {
		name, config := each[0], each[1]
		if _, err := envs.Call("save", delta.JsonObjectOf("name", name, "config", object(t, config)), p2test.Background); err != nil {
			t.Fatal(err)
		}
	}
	_, err = call("list", delta.JsonObjectOf("name", "host"))
	wantError(t, err, `"host" is not a sandbox`)
	_, err = call("set", delta.JsonObjectOf("sandbox", "box", "name", "GITHUB_TOKEN", "value", "ghp_x", "hosts", []any{"github.com"}))
	wantError(t, err, "not an allowed host")
	// addHosts allows the hosts the sandbox does not allow yet.
	if _, err := call("set", delta.JsonObjectOf("sandbox", "box", "name", "GITHUB_TOKEN", "value", "ghp_x", "hosts", []any{"github.com"}, "preset", "github", "addHosts", true)); err != nil {
		t.Fatal(err)
	}
	rows, err := p2test.Until(envs.Member("envs"), func(value delta.JsonValue) bool {
		for _, each := range value.([]any) {
			if row := objectOf(each); row.Value("name") == "box" {
				allowed, _ := objectOf(row.Value("config")).Value("allowHosts").([]any)
				return len(allowed) == 2
			}
		}
		return false
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if text := mustMarshal(t, rows); strings.Contains(text, "ghp_x") {
		t.Error("a secret value is in the published state")
	}
	listed, err := call("list", delta.JsonObjectOf("name", "box"))
	if err != nil {
		t.Fatal(err)
	}
	text := mustMarshal(t, listed)
	if strings.Contains(text, "ghp_x") || !strings.Contains(text, `"name":"GITHUB_TOKEN"`) || !strings.Contains(text, `"preset":"github"`) {
		t.Errorf("list = %s", text)
	}
	if _, err := call("delete", delta.JsonObjectOf("sandbox", "box", "name", "GITHUB_TOKEN")); err != nil {
		t.Fatal(err)
	}
	if listed, _ := call("list", delta.JsonObjectOf("name", "box")); len(listed.([]any)) != 0 {
		t.Errorf("after delete: %v", listed)
	}
}

// Pages built with older versions of Envs still bind: version 2 reads rows without sandbox disk sizes, version 1 also without
// users and its delete takes no force.
func TestOlderEnvsVersionsStillBind(t *testing.T) {
	f := newEnvsFixture(t)
	current := f.owner(t, owner)
	for _, each := range [][2]string{{"host", `{"type":"ssh","host":"h.example"}`}, {"box", `{"type":"gondolin","host":"host","diskGb":64,"cpus":2}`}} {
		name, config := each[0], each[1]
		if _, err := current.Call("save", delta.JsonObjectOf("name", name, "config", object(t, config)), p2test.Background); err != nil {
			t.Fatal(err)
		}
	}
	for version, old := range map[int]*contractVersion{1: {contract.EnvsV1, false}, 2: {contract.EnvsV2, true}} {
		ref, err := p2test.BoundIn(f.Client(owner), old.contract, delta.NewJsonObject(0), "owner")
		if err != nil {
			t.Fatalf("version %d does not bind: %v", version, err)
		}
		rows, err := p2test.Until(ref.Member("envs"), func(value delta.JsonValue) bool { r, _ := value.([]any); return len(r) == 2 }, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, each := range rows.([]any) {
			row := objectOf(each)
			if row.Has("users") != old.users {
				t.Errorf("version %d row %v: users present = %v", version, row.Value("name"), row.Has("users"))
			}
			if config := objectOf(row.Value("config")); config.Has("diskGb") {
				t.Errorf("version %d row %v carries diskGb", version, row.Value("name"))
			}
		}
		ref.Release()
	}
}

type contractVersion struct {
	contract *contracts.Contract
	users    bool
}
