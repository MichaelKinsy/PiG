package app

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	chordhttp "github.com/MichaelKinsy/PiG/chord/nextnode/http"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Ports packages/chord/test/node/app/app.test.ts (Pi main c23b9ec1): `StartServer`: the layout and the start-data object, start and
// close, a second server on one data directory, the private modes, an owner refused, a plugin of the root failing, and the owner link.
// Workers, export and import are in app_workers_test.go; applying changes and the base marks in app_changes_test.go. Not ported: the
// `@ts-expect-error` type test (a nil `Principal.Worker` with worker kinds is refused at start instead), installs (`installPlugin` is
// not in the Go plugins component yet: T9) and the web and terminal surfaces (T11).

func TestAppLaysOutTheDataDirectoryServesAppInfoAndTheServersPluginsAndClosesEverything(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{"echo": {"server.ts": echoServer}}, nil)
	if got := dirEntries(t, s.dataDir); !slices.Equal(got, []string{"cache", "logs", "owner-token", "plugins", "server", "workers"}) {
		t.Fatalf("data directory = %v", got)
	}
	server := dirEntries(t, filepath.Join(s.dataDir, "server"))
	for _, want := range []string{"cookie-secret", "store.sqlite"} {
		if !slices.Contains(server, want) {
			t.Fatalf("server directory %v lacks %s", server, want)
		}
	}
	info, err := s.app.Host.Get(background, AppInfo, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for member, want := range map[string]delta.JsonValue{"dataDir": s.dataDir, "name": "chord-test", "version": "v1"} {
		if got, _ := info.Member(member).Value(); got != want {
			t.Fatalf("AppInfo.%s = %v, want %v", member, got, want)
		}
	}
	owner, _ := info.Member("owner").Value()
	if object, _ := owner.(*delta.JsonObject); object == nil || object.Value("kind") != "owner" {
		t.Fatalf("AppInfo.owner = %v", owner)
	}
	info.Release()
	served, err := s.app.Node.Get(background, echo, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := served.Member("text").Value(); got != "echo" {
		t.Fatalf("Echo.text = %v", got)
	}
	served.Release()
	if !regexp.MustCompile(`^http://127\.0\.0\.1:` + strconv.Itoa(s.port) + `/_chord/owner#[A-Za-z0-9_-]{43}$`).MatchString(s.app.OwnerLink()) {
		t.Fatalf("owner link = %q", s.app.OwnerLink())
	}
	if len(s.app.Workers) != 0 {
		t.Fatalf("workers = %v", s.app.Workers)
	}
	if err := s.app.Close(background); err != nil {
		t.Fatal(err)
	}
	// HTTP closed first: the port answers nothing any more; a second close is the same close.
	if _, err := socketDial(s.port); err == nil {
		t.Fatal("the port still answers after close")
	}
	if err := s.app.Close(background); err != nil {
		t.Fatal(err)
	}
}

func TestAppRefusesASecondServerOnTheSameDataDirectoryWhileTheFirstRuns(t *testing.T) {
	first := startTestApp(t, nil, nil)
	_, err := StartServer(optionsFor(t, first.dataDir, first.root), background)
	if err == nil || !strings.Contains(err.Error(), "another server runs on "+first.dataDir) {
		t.Fatalf("second start: %v", err)
	}
	if err := first.app.Close(background); err != nil {
		t.Fatal(err)
	}
	third, err := StartServer(optionsFor(t, first.dataDir, first.root), background)
	if err != nil {
		t.Fatal(err)
	}
	if err := third.Close(background); err != nil {
		t.Fatal(err)
	}
}

func TestAppRefusesASecondServerOnTheSameDataDirectoryThroughASymbolicLink(t *testing.T) {
	first := startTestApp(t, nil, nil)
	link := filepath.Join(t.TempDir(), "link")
	testenv.Symlink(t, first.dataDir, link)
	_, err := StartServer(optionsFor(t, link, first.root), background)
	if err == nil || !strings.Contains(err.Error(), "another server runs on") {
		t.Fatalf("second start: %v", err)
	}
}

func TestAppMakesTheDataDirectoryPrivateAtEveryStartAlsoWhenItExistedLooser(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(filepath.Join(dataDir, "server"), 0o775); err != nil {
		t.Fatal(err)
	}
	for path, mode := range map[string]os.FileMode{dataDir: 0o755, filepath.Join(dataDir, "server"): 0o775} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	s := startTestApp(t, nil, func(o *AppOptions) { o.DataDir = dataDir })
	mode := func(path string) os.FileMode {
		t.Helper()
		info, err := os.Stat(filepath.Join(dataDir, path))
		if err != nil {
			t.Fatal(err)
		}
		return info.Mode().Perm()
	}
	paths := []string{".", "server", "workers", "plugins", "logs", "cache", "cache/workers", "server/store.sqlite", "server/cookie-secret", "owner-token"}
	want := []os.FileMode{0o700, 0o700, 0o700, 0o700, 0o700, 0o700, 0o700, 0o600, 0o600, 0o600}
	for i, path := range paths {
		if got := mode(path); got != want[i] {
			t.Fatalf("%s mode = %o, want %o", path, got, want[i])
		}
	}
	if err := s.app.Close(background); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"server/store.sqlite", "owner-token"} {
		if err := os.Chmod(filepath.Join(dataDir, path), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	again := startTestApp(t, nil, func(o *AppOptions) { o.DataDir = dataDir })
	if got := []os.FileMode{mode("server/store.sqlite"), mode("owner-token")}; got[0] != 0o600 || got[1] != 0o600 {
		t.Fatalf("modes after a restart = %o", got)
	}
	_ = again
}

func TestAppRefusesADataDirectoryTheKitMustNotTightenHomeRootAnotherUsersAlsoThroughALink(t *testing.T) {
	// The kit refuses the home directory: a home of this test's own, so that the real one is neither read nor touched.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	start := func(dir string) error {
		options, _ := testOptions(t, func(o *AppOptions) { o.DataDir = dir })
		app, err := StartServer(options, background)
		if err == nil {
			_ = app.Close(background)
		}
		return err
	}
	before, _ := os.Stat(home)
	if err := start(home); err == nil || !strings.Contains(err.Error(), "is the home directory") {
		t.Fatalf("home: %v", err)
	}
	if err := start("/"); err == nil || !strings.Contains(err.Error(), "is a file system's root") {
		t.Fatalf("root: %v", err)
	}
	link := filepath.Join(t.TempDir(), "home-link")
	testenv.Symlink(t, home, link)
	if err := start(link); err == nil || !strings.Contains(err.Error(), "is the home directory") {
		t.Fatalf("link to home: %v", err)
	}
	if after, _ := os.Stat(home); after.Mode().Perm() != before.Mode().Perm() {
		t.Fatalf("home mode changed: %o -> %o", before.Mode().Perm(), after.Mode().Perm())
	}
	if os.Getuid() != 0 {
		// `/tmp`'s owner is root.
		if err := start("/tmp"); err == nil || !strings.Contains(err.Error(), "belongs to another user") {
			t.Fatalf("/tmp: %v", err)
		}
	}
}

func TestAppNamesTheAddressListenedOnInTheOwnerLink(t *testing.T) {
	options, _ := testOptions(t, func(o *AppOptions) { o.Listen = &chordhttp.ListenAddress{Host: "::1", Port: 0} })
	app, err := StartServer(options, background)
	if err != nil {
		t.Skipf("no IPv6 loopback: %v", err)
	}
	defer func() { _ = app.Close(background) }()
	if !regexp.MustCompile(`^http://\[::1\]:\d+/_chord/owner#`).MatchString(app.OwnerLink()) {
		t.Fatalf("owner link = %q", app.OwnerLink())
	}
}

func TestAppFailsWhenAPluginOfTheAppsRootFailsWithEverythingClosed(t *testing.T) {
	// Registered first, so it runs after the starts: nothing of the app, its HTTP component, its workers or its plugins is left.
	t.Cleanup(func() {
		noGoroutinesOf(t, "chord/nextnode/app.", "chord/nextnode/http.", "chord/nextnode/workers.", "chord/nextnode/plugins.")
	})
	options, _ := testOptions(t, nil)
	writePlugin(t, options.Root, "broken", "@test/broken", map[string]string{"server.ts": "export default {"})
	_, err := StartServer(options, background)
	if err == nil || !strings.Contains(err.Error(), `"@test/broken"`) {
		t.Fatalf("start: %v", err)
	}
	// Closed: the data directory's lock is free again (a second start fails only on the same plugin).
	_, err = StartServer(options, background)
	if err == nil || !strings.Contains(err.Error(), `"@test/broken"`) {
		t.Fatalf("second start: %v", err)
	}
}

func TestAppRefusesAnOwnerThatIsNotStrictJSONOrIsTheServersPrincipalBeforeItOpensAnything(t *testing.T) {
	options, _ := testOptions(t, nil)
	options.Principal.Owner = delta.JsonObjectOf("kind", "user", "id", "u1", "name", func() {})
	if _, err := StartServer(options, background); err == nil || !strings.Contains(err.Error(), "principal.owner must be strict JSON") {
		t.Fatalf("not JSON: %v", err)
	}
	options.Principal.Owner = delta.JsonObjectOf("kind", "server")
	if _, err := StartServer(options, background); err == nil || !strings.Contains(err.Error(), "principal.owner must not be the server's principal") {
		t.Fatalf("server: %v", err)
	}
	if _, err := os.Stat(options.DataDir); err == nil {
		t.Fatal("a refused owner opened the data directory")
	}
}

func TestAppStartsWithoutWorkerKindsAndWithoutAWorkerPrincipalWorkerKindsNeedIt(t *testing.T) {
	s := startTestApp(t, nil, func(o *AppOptions) { o.Principal.Worker = nil })
	if len(s.app.Workers) != 0 {
		t.Fatalf("workers = %d, want 0", len(s.app.Workers))
	}
	if err := s.app.Close(background); err != nil {
		t.Fatal(err)
	}
	options, _ := testOptions(t, nil)
	options.Principal.Worker = nil
	options.Workers = []AppWorkerKind{testWorker(t, project, "")}
	if _, err := StartServer(options, background); err == nil || !strings.Contains(err.Error(), "worker kinds need principal.worker") {
		t.Fatalf("start: %v", err)
	}
}
