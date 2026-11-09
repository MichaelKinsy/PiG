package app

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
)

// Ports the "base code" tests of packages/chord/test/node/app/app.test.ts that start a server with no `base` given (Pi's `runStart`).
// Pi compares the static import graph of its start script; the server's own code in Go is its executable (D603). Test 1 ("compares
// the server's own code ... with no `base` given", marked and cleared at the revert) and test 3 ("starts when the scan of its entry
// fails ...: reported, not compared") are ported with a helper program the test builds. Test 2 ("starts with an entry whose inactive
// optional import is missing: the import is left out, the rest compared") has the Go form of a path of the server's own code that is
// not there: a `Base` path the app was told of and the build did not leave (an optional API module), beside the executable.

// serverProgram builds a program printing label into bin; Go builds are reproducible, so one label is one set of bytes.
type serverProgram struct {
	t   *testing.T
	src string
	bin string
}

func newServerProgram(t *testing.T) *serverProgram {
	t.Helper()
	program := &serverProgram{t: t, src: t.TempDir(), bin: filepath.Join(t.TempDir(), "server")}
	if err := os.WriteFile(filepath.Join(program.src, "go.mod"), []byte("module server\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return program
}

func (p *serverProgram) build(label string) []byte {
	p.t.Helper()
	main := "package main\n\nimport \"fmt\"\n\nconst label = \"" + label + "\"\n\nfunc main() { fmt.Println(label) }\n"
	if err := os.WriteFile(filepath.Join(p.src, "main.go"), []byte(main), 0o644); err != nil {
		p.t.Fatal(err)
	}
	command := exec.Command("go", "build", "-buildvcs=false", "-o", p.bin, ".")
	command.Dir = p.src
	command.Env = append(os.Environ(), "GOFLAGS=", "GOTOOLCHAIN=local", "GOWORK=off")
	if out, err := command.CombinedOutput(); err != nil {
		p.t.Fatalf("go build: %v\n%s", err, out)
	}
	built, err := os.ReadFile(p.bin)
	if err != nil {
		p.t.Fatal(err)
	}
	return built
}

func TestAppComparesTheServersOwnCodeFromItsExecutableWithNoBaseGivenMarkedAndCleared(t *testing.T) {
	program := newServerProgram(t)
	v1 := program.build("v1")
	// The program was built after this test's process started: its start is later than the build, as the server's is.
	began := time.Now()
	s := startTestApp(t, nil, func(o *AppOptions) {
		o.executable = func() (string, error) { return program.bin, nil }
		o.processStart = func() time.Time { return began }
	})
	<-s.app.BaseWatched()
	if s.app.Plugins.Component.Marks().Base {
		t.Fatal("base is marked at start")
	}
	if v2 := program.build("v2"); bytes.Equal(v1, v2) {
		t.Fatal("v2 has v1's bytes")
	}
	untilMarks(t, s.app, "base marked by an edit of the executable", func(m plugins.PluginsState) bool { return m.Base })
	if !bytes.Equal(program.build("v1"), v1) {
		t.Fatal("a rebuild of v1 differs from v1")
	}
	untilMarks(t, s.app, "base cleared by the revert", func(m plugins.PluginsState) bool { return !m.Base })
	if reported := s.errors.kinds(); len(reported) != 0 {
		t.Fatalf("reported %v", reported)
	}
}

func TestAppStartsWhenItsExecutableCannotBeReadReportedNotCompared(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-server")
	s := startTestApp(t, nil, func(o *AppOptions) { o.executable = func() (string, error) { return missing, nil } })
	s.errors.expect()
	<-s.app.BaseWatched()
	if got := s.errors.kinds(); len(got) != 1 || got[0] != AppErrorServerScan {
		t.Fatalf("reported %v, want [server_scan]", got)
	}
	if s.app.Plugins.Component.Marks().Base {
		t.Fatal("base is marked")
	}
}

// The programs `go run` and `go test` build lie under a go-build work directory: never compared, as Pi never compares an entry under
// `node_modules` (an npm-installed app, a test runner); the tests of this package run as such a program.
func TestAppDoesNotCompareAnExecutableUnderTheGoBuildCache(t *testing.T) {
	for path, want := range map[string]bool{
		"/tmp/go-build123456/b001/exe/server":       true,
		"/var/folders/x/T/go-build99/b001/app.test": true,
		"/tmp/go-build/b001/server":                 true,
		"/usr/local/bin/server":                     false,
		"/opt/go-builds/server":                     false,
		"/opt/go-build-tools/server":                false,
	} {
		if got := inGoBuildCache(path); got != want {
			t.Errorf("inGoBuildCache(%q) = %v, want %v", path, got, want)
		}
	}
	// End to end: an executable under such a directory is not compared. The barrier is a plugin edit: the watcher handles events in
	// order, so once it shows the plugin dirty it has seen the executable's rewrite too.
	built := filepath.Join(t.TempDir(), "go-build123", "b001", "server")
	if err := os.MkdirAll(filepath.Dir(built), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(built, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := startTestApp(t, map[string]map[string]string{"echo": {"server.ts": echoServer}}, func(o *AppOptions) {
		o.executable = func() (string, error) { return built, nil }
	})
	<-s.app.BaseWatched()
	if err := os.WriteFile(built, []byte("v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	editEcho(t, s.root, "barrier")
	untilMarks(t, s.app, "the barrier edit", func(m plugins.PluginsState) bool { return len(m.Dirty) == 1 })
	if s.app.Plugins.Component.Marks().Base {
		t.Fatal("base is marked by an edit of an executable under the go-build cache")
	}
	if got := s.errors.kinds(); len(got) != 0 {
		t.Fatalf("reported %v", got)
	}
}

// Something that is no regular file, and a file that cannot be opened, are the server's code that cannot be read: reported.
func TestAppReportsAnExecutableThatIsNoRegularFileOrCannotBeOpened(t *testing.T) {
	unreadable := filepath.Join(t.TempDir(), "server")
	if err := os.WriteFile(unreadable, []byte("x"), 0o000); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{"a directory": t.TempDir()}
	if file, err := os.Open(unreadable); err != nil {
		cases["a file with no permission"] = unreadable
	} else {
		_ = file.Close() // root, or a system without modes: nothing is unreadable
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			s := startTestApp(t, nil, func(o *AppOptions) { o.executable = func() (string, error) { return path, nil } })
			s.errors.expect()
			<-s.app.BaseWatched()
			if got := s.errors.kinds(); len(got) != 1 || got[0] != AppErrorServerScan {
				t.Fatalf("reported %v, want [server_scan]", got)
			}
		})
	}
}

// startWithExecutable starts an app whose server executable is a file with the given modification time, the process having started
// at began.
func startWithExecutable(t *testing.T, modified, began time.Time) started {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "server")
	if err := os.WriteFile(exe, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(exe, modified, modified); err != nil {
		t.Fatal(err)
	}
	s := startTestApp(t, nil, func(o *AppOptions) {
		o.executable = func() (string, error) { return exe, nil }
		o.processStart = func() time.Time { return began }
	})
	<-s.app.BaseWatched()
	return s
}

// Pi's `marks a file of its own code edited right after the start, before the scan read it (Node loaded the old)`: the executable's
// bytes read now may not be the running process's, as its timestamps are after the process started.
func TestAppMarksAnExecutableEditedAfterTheProcessStartedBeforeItWasRead(t *testing.T) {
	// The mtime is now (the file was just written), the process started an hour ago; the ctime is now too.
	s := startWithExecutable(t, time.Now(), time.Now().Add(-time.Hour))
	if !s.app.Plugins.Component.Marks().Base {
		t.Fatal("base is not marked for an executable edited since the process started")
	}
}

// Pi's `does not mark a file of its own code stamped in the future: compared by its bytes (Astra)`: a timestamp later than now plus
// the slack is no edit since the start (the ctime, which a rewrite sets, is the one of the file's creation here: now, but the
// process started after it).
func TestAppDoesNotMarkAnExecutableStampedInTheFutureComparedByItsBytes(t *testing.T) {
	// mtime tomorrow, process started in an hour (so the ctime, now, is before the start too): neither timestamp is in range.
	s := startWithExecutable(t, time.Now().Add(24*time.Hour), time.Now().Add(time.Hour))
	if s.app.Plugins.Component.Marks().Base {
		t.Fatal("base is marked for an executable stamped in the future")
	}
}

// A program under the go-build cache is skipped before it is read: one the process cannot read is not the server's code that cannot
// be read, so nothing is reported for it (the skip needs no bytes).
func TestAppSkipsAGoBuildProgramBeforeReadingIt(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("the file's mode does not keep this user from reading it")
	}
	built := filepath.Join(t.TempDir(), "go-build123", "b001", "server")
	if err := os.MkdirAll(filepath.Dir(built), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(built, []byte("v1"), 0o000); err != nil {
		t.Fatal(err)
	}
	s := startTestApp(t, nil, func(o *AppOptions) { o.executable = func() (string, error) { return built, nil } })
	<-s.app.BaseWatched()
	if got := s.errors.kinds(); len(got) != 0 {
		t.Fatalf("reported %v, want nothing for a go-build program", got)
	}
}

// Intention of app.test.ts "base code › starts with an entry whose inactive optional import is missing: the import is left out,
// the rest compared" (D603: the Go server's own code is its executable, so it has no import graph to scan). What the case
// protects is that a missing file an import graph names never stops the server from starting. The Node-hosted code of an app is
// its plugins, and the graph of a plugin is what its build reads (BuildFacet's inputs, compared by the plugins' watcher). A
// plugin of the user's root whose facet imports a file that is not there has a failed build (a result naming the file, line and
// import) and the server starts with the app's own plugins running; only the app's root (required) stops a start (see
// TestAppFailsWhenAPluginOfTheAppsRootFailsWithEverythingClosed).
func TestAppStartsWhenAnOptionalPluginsFacetImportsAFileThatIsNotThereItsBuildFailsAndTheRestRuns(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{"notes": notesPlugin}, func(o *AppOptions) {
		writePlugin(t, filepath.Join(o.DataDir, "plugins"), "optional", "@test/optional", map[string]string{
			"server.ts": "import { missing } from \"./missing.ts\";\nexport default { setup: () => missing };",
		})
	})
	if s.app.OwnerLink() == "" {
		t.Fatal("the app has no owner link")
	}
	scripts, ok := s.app.Plugins.Component.Current("server")
	if !ok {
		t.Fatal("the server's plugins were never built")
	}
	for _, script := range scripts {
		if script.Plugin.Name != "@test/optional" {
			continue
		}
		if script.Built.Kind != model.BuiltFacetFailed || !strings.Contains(strings.Join(script.Built.Errors, "\n"), `server.ts:1:`) ||
			!strings.Contains(strings.Join(script.Built.Errors, "\n"), `Could not resolve "./missing.ts"`) {
			t.Fatalf("built = %+v, want a failed build naming server.ts:1 and the import", script.Built)
		}
		return
	}
	t.Fatalf("no built server facet of @test/optional in %d scripts", len(scripts))
}

// Pi's `starts with an entry whose inactive optional import is missing: the import is left out, the rest compared` (it imports
// helper.ts, and two optional files that are not installed behind a condition that is off; edits helper.ts and expects "started v1 0",
// then "marked"): the server's own code is its executable plus the files of its Base option; one of them that is not there is left
// out, nothing is reported (errors 0), and the rest (the executable) is compared. Mutant (portmap-mutants.json, watcher.ts row): recordOwn
// recording an unreadable file as "unreadable" instead of leaving it out marks base once the file appears and a record names it, and this test fails.
func TestAppStartsWithAnOptionalBasePathMissingTheMissingOneLeftOutTheRestCompared(t *testing.T) {
	program := newServerProgram(t)
	v1 := program.build("v1")
	began := time.Now()
	missing := filepath.Join(t.TempDir(), "optional-feature-not-installed.mjs")
	s := startTestApp(t, nil, func(o *AppOptions) {
		o.executable = func() (string, error) { return program.bin, nil }
		o.processStart = func() time.Time { return began }
		o.Base = []string{missing}
	})
	<-s.app.BaseWatched()
	if reported := s.errors.kinds(); len(reported) != 0 {
		t.Fatalf("reported %v at the start, want none (started with 0 errors)", reported)
	}
	if s.app.Plugins.Component.Marks().Base {
		t.Fatal("base is marked at start by a path that is not there")
	}
	// The path was left out of what is compared, not recorded as unreadable: when the file appears and a record names it with its
	// bytes, nothing differs. WatchBaseCompared's channel closes once the comparison of the path ended (after any in flight: a record
	// that held "unreadable" for it has been compared by then), so what Marks shows next is decided.
	if err := os.WriteFile(missing, []byte("export {};\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash, readable := model.HashFile(missing)
	if !readable {
		t.Fatal("the file just written cannot be read")
	}
	<-s.app.Plugins.Component.WatchBaseCompared(model.BaseInputs{missing: hash})
	if s.app.Plugins.Component.Marks().Base {
		t.Fatal("base is marked by the appearance of a path that was left out")
	}
	if v2 := program.build("v2"); bytes.Equal(v1, v2) {
		t.Fatal("v2 has v1's bytes")
	}
	untilMarks(t, s.app, "base marked by an edit of the executable beside the missing path", func(m plugins.PluginsState) bool { return m.Base })
	if reported := s.errors.kinds(); len(reported) != 0 {
		t.Fatalf("reported %v, want none", reported)
	}
}
