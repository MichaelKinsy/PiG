// Package npmtest is a stand-in npm and registry for tests of npm publication.
//
// Install builds a small `npm` program, puts it first on PATH, and gives it a private registry directory. The program implements the part of npm that publication and `pig piglet add npm:` use: `view`, `publish` (with `--dry-run`), and `install --prefix`. Publishing stores the files npm would pack, and installing unpacks them with a package-lock.json, so a package published in one step can be installed in the next without a network.
package npmtest

import (
	_ "embed"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

//go:embed fakenpm.go.txt
var source []byte

const stateEnv = "PIG_FAKE_NPM_STATE"

// Fake is one test's registry and call log.
type Fake struct {
	t     testing.TB
	State string
}

// Call is one recorded npm invocation.
type Call struct {
	Args []string `json:"args"`
	Cwd  string   `json:"cwd"`
}

// Install builds the fake npm and puts it first on PATH for the rest of the test.
func Install(t testing.TB) *Fake {
	t.Helper()
	bin := t.TempDir()
	name := "npm"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	main := filepath.Join(bin, "main.go")
	if err := os.WriteFile(main, source, 0o644); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", filepath.Join(bin, name), main)
	build.Dir = bin
	build.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=", "GO111MODULE=off", "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fake npm: %v\n%s", err, output)
	}
	state := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(stateEnv, state)
	t.Setenv("PIG_OFFLINE", "")
	t.Setenv("PI_OFFLINE", "")
	t.Setenv(TrustedPublishingEnv, "")
	return &Fake{t: t, State: state}
}

// TrustedPublishingEnv is the variable that makes npm publish with provenance. Install clears it.
const TrustedPublishingEnv = "ACTIONS_ID_TOKEN_REQUEST_URL"

func (f *Fake) touch(name string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.State, name), nil, 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// FailPublish makes every real publish fail with a permission error.
func (f *Fake) FailPublish() { f.touch("fail-publish") }

// GoOffline makes every command fail as a network error would.
func (f *Fake) GoOffline() { f.touch("offline") }

// Seed puts a published name@version in the registry without going through publish.
func (f *Fake) Seed(name, version string) {
	f.t.Helper()
	dir := filepath.Join(f.State, "registry", url.PathEscape(name), version, "package")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"`+name+`","version":"`+version+`"}`), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// Calls returns every recorded invocation in order.
func (f *Fake) Calls() []Call {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.State, "calls.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		f.t.Fatal(err)
	}
	var calls []Call
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var call Call
		if err := json.Unmarshal([]byte(line), &call); err != nil {
			f.t.Fatal(err)
		}
		calls = append(calls, call)
	}
	return calls
}

// Publishes returns the arguments of every non-dry-run publish.
func (f *Fake) Publishes() [][]string {
	var out [][]string
	for _, call := range f.Calls() {
		if len(call.Args) > 0 && call.Args[0] == "publish" && !contains(call.Args, "--dry-run") {
			out = append(out, call.Args)
		}
	}
	return out
}

func contains(values []string, want string) bool {
	return slices.Contains(values, want)
}

// PackageDir returns the directory holding the files published as name@version.
func (f *Fake) PackageDir(name, version string) string {
	return filepath.Join(f.State, "registry", url.PathEscape(name), version, "package")
}

// Published reports whether name@version was published.
func (f *Fake) Published(name, version string) bool {
	_, err := os.Stat(f.PackageDir(name, version))
	return err == nil
}

// Manifest returns the published package.json of name@version.
func (f *Fake) Manifest(name, version string) map[string]any {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.PackageDir(name, version), "package.json"))
	if err != nil {
		f.t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		f.t.Fatal(err)
	}
	return manifest
}
