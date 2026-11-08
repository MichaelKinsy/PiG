package app

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// Ports the "base code" tests of packages/chord/test/node/app/app.test.ts that start a server with no `base` given (Pi's `runStart`).
// Pi compares the static import graph of its start script; the server's own code in Go is its executable (D603). Test 1 ("compares
// the server's own code ... with no `base` given", marked and cleared at the revert) and test 3 ("starts when the scan of its entry
// fails ...: reported, not compared") are ported with a helper program the test builds; test 2 (a lazy optional import that is
// missing) has no Go form.

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
	s := startTestApp(t, nil, func(o *AppOptions) { o.executable = func() (string, error) { return program.bin, nil } })
	if s.app.Plugins.Marks().Base {
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
	if got := s.errors.kinds(); len(got) != 1 || got[0] != AppErrorServerScan {
		t.Fatalf("reported %v, want [server_scan]", got)
	}
	if s.app.Plugins.Marks().Base {
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
	if err := os.WriteFile(built, []byte("v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	editEcho(t, s.root, "barrier")
	untilMarks(t, s.app, "the barrier edit", func(m plugins.PluginsState) bool { return len(m.Dirty) == 1 })
	if s.app.Plugins.Marks().Base {
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
			if got := s.errors.kinds(); len(got) != 1 || got[0] != AppErrorServerScan {
				t.Fatalf("reported %v, want [server_scan]", got)
			}
		})
	}
}
