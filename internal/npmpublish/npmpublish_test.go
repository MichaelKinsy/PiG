package npmpublish_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/npmpublish"
	"github.com/MichaelKinsy/PiG/internal/npmpublish/npmtest"
)

func writePackage(t *testing.T, version string) string {
	t.Helper()
	return writePackageAt(t, t.TempDir(), version)
}

func writePackageAt(t *testing.T, dir, version string) string {
	t.Helper()
	for name, body := range map[string]string{
		"package.json": `{"name":"@acme/tool","version":"` + version + `","files":["lib"]}`,
		"lib/index.js": "module.exports = {}\n",
		"secret.txt":   "not in files\n",
	} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func request(dir string, flags npmpublish.Flags, out *bytes.Buffer) npmpublish.Request {
	return npmpublish.Request{
		Package: npmpublish.Package{Name: "@acme/tool", Version: "1.0.0", Dir: dir},
		Flags:   flags, Label: "Package",
		Stdout: out, Stderr: out,
	}
}

func TestPublishIsADryRunUnlessYes(t *testing.T) {
	fake := npmtest.Install(t)
	dir := writePackage(t, "1.0.0")
	var out bytes.Buffer

	if err := request(dir, npmpublish.Flags{}, &out).Run(context.Background()); err != nil {
		t.Fatalf("dry run: %v\n%s", err, out.String())
	}

	if len(fake.Publishes()) != 0 || fake.Published("@acme/tool", "1.0.0") {
		t.Fatalf("a dry run published: %v", fake.Publishes())
	}
	calls := fake.Calls()
	want := [][]string{{"view", "@acme/tool@1.0.0", "version"}, {"publish", dir, "--dry-run"}}
	if len(calls) != 2 {
		t.Fatalf("npm calls = %v, want %v", calls, want)
	}
	if got := [][]string{calls[0].Args, calls[1].Args}; !reflect.DeepEqual(got, want) {
		t.Fatalf("npm calls = %v, want %v", calls, want)
	}
	for _, expect := range []string{"Package npm publish dry run", "@acme/tool@1.0.0", "npm notice lib/index.js", "Would run: npm publish " + dir, "rerun with --yes"} {
		if !strings.Contains(out.String(), expect) {
			t.Fatalf("output lacks %q:\n%s", expect, out.String())
		}
	}
	if strings.Contains(out.String(), "secret.txt") {
		t.Fatalf("the tarball listing includes a file outside `files`:\n%s", out.String())
	}
}

func TestPublishReportsAMissingNPM(t *testing.T) {
	npmtest.Install(t)
	var out bytes.Buffer
	r := request(writePackage(t, "1.0.0"), npmpublish.Flags{}, &out)
	r.Command = []string{"pig-test-no-such-npm"}
	if err := r.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "npm is not on PATH") {
		t.Fatalf("publish with a missing npm = %v, want the npm-not-found error\n%s", err, out.String())
	}
}

func TestTheExistenceCheckAsksTheRegistryPublishSendsTo(t *testing.T) {
	for _, registry := range []string{"https://npm.pkg.github.com/", "https://registry.example.org/team/"} {
		fake := npmtest.Install(t)
		dir := writePackage(t, "1.0.0")
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"@acme/tool","version":"1.0.0","publishConfig":{"registry":"`+registry+`"}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := request(dir, npmpublish.Flags{}, &out).Run(context.Background()); err != nil {
			t.Fatalf("dry run: %v\n%s", err, out.String())
		}
		// npm view ignores publishConfig, so without --registry the check would ask npm's default registry.
		if calls := fake.Calls(); len(calls) == 0 || !reflect.DeepEqual(calls[0].Args, []string{"view", "@acme/tool@1.0.0", "version", "--registry", registry}) {
			t.Fatalf("npm calls = %v, want the view to name %s", calls, registry)
		}
	}
}

func TestYesPublishesWithTheRequestedTagAccessAndCode(t *testing.T) {
	fake := npmtest.Install(t)
	dir := writePackage(t, "1.0.0")
	var out bytes.Buffer
	flags := npmpublish.Flags{Yes: true, Tag: "next", Access: "public", OTP: "123456"}
	r := request(dir, flags, &out)
	r.Next = []string{"Add it with: pig package install npm:@acme/tool"}

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("publish: %v\n%s", err, out.String())
	}

	want := [][]string{{"publish", dir, "--tag", "next", "--access", "public", "--otp", "123456"}}
	if got := fake.Publishes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("publish calls = %v, want %v", got, want)
	}
	if !fake.Published("@acme/tool", "1.0.0") {
		t.Fatal("nothing was published")
	}
	if strings.Contains(out.String(), "123456") {
		t.Fatalf("the one-time code was printed:\n%s", out.String())
	}
	for _, expect := range []string{"Running: npm publish " + dir + " --tag next --access public --otp <code>", "Published @acme/tool@1.0.0 to npm.", "Add it with: pig package install npm:@acme/tool"} {
		if !strings.Contains(out.String(), expect) {
			t.Fatalf("output lacks %q:\n%s", expect, out.String())
		}
	}
	if fake.Manifest("@acme/tool", "1.0.0")["version"] != "1.0.0" {
		t.Fatal("published manifest lost its version")
	}
}

func TestAnExistingVersionIsRefusedBeforeAnyPublish(t *testing.T) {
	fake := npmtest.Install(t)
	fake.Seed("@acme/tool", "1.0.0")
	dir := writePackage(t, "1.0.0")
	var out bytes.Buffer

	err := request(dir, npmpublish.Flags{Yes: true}, &out).Run(context.Background())

	if err == nil || !strings.Contains(err.Error(), "@acme/tool@1.0.0 is already on npm") || !strings.Contains(err.Error(), "raise the version") {
		t.Fatalf("error = %v", err)
	}
	if len(fake.Publishes()) != 0 {
		t.Fatalf("published over an existing version: %v", fake.Publishes())
	}
}

func TestADifferentVersionOfAnExistingNameIsPublishable(t *testing.T) {
	fake := npmtest.Install(t)
	fake.Seed("@acme/tool", "0.9.0")
	var out bytes.Buffer
	if err := request(writePackage(t, "1.0.0"), npmpublish.Flags{Yes: true}, &out).Run(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !fake.Published("@acme/tool", "1.0.0") {
		t.Fatal("not published")
	}
}

func TestAnUnreachableRegistryIsNeverReadAsNotPublished(t *testing.T) {
	fake := npmtest.Install(t)
	fake.GoOffline()
	var out bytes.Buffer
	err := request(writePackage(t, "1.0.0"), npmpublish.Flags{Yes: true}, &out).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "npm view @acme/tool@1.0.0 failed") {
		t.Fatalf("error = %v", err)
	}
	if len(fake.Publishes()) != 0 {
		t.Fatalf("published without knowing the registry state: %v", fake.Publishes())
	}
}

func TestAFailedPublishReportsNPMAndPublishesNothing(t *testing.T) {
	fake := npmtest.Install(t)
	fake.FailPublish()
	var out bytes.Buffer
	err := request(writePackage(t, "1.0.0"), npmpublish.Flags{Yes: true}, &out).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "npm publish of @acme/tool@1.0.0 failed") {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(out.String(), "E403") || strings.Contains(out.String(), "Published @acme/tool") {
		t.Fatalf("output:\n%s", out.String())
	}
}

func TestAPrereleaseNeedsADistTag(t *testing.T) {
	fake := npmtest.Install(t)
	var out bytes.Buffer
	r := request(writePackage(t, "2.0.0-rc.1"), npmpublish.Flags{Yes: true}, &out)
	r.Version = "2.0.0-rc.1"
	err := r.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "--tag next") {
		t.Fatalf("error = %v", err)
	}
	if len(fake.Calls()) != 0 {
		t.Fatalf("npm ran before the prerelease was refused: %v", fake.Calls())
	}
	r.Tag = "next"
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
}

func TestTrustedPublishingAddsProvenance(t *testing.T) {
	fake := npmtest.Install(t)
	t.Setenv(npmpublish.TrustedPublishingEnv, "https://token.actions.example/")
	dir := writePackage(t, "1.0.0")
	var out bytes.Buffer
	if err := request(dir, npmpublish.Flags{Yes: true}, &out).Run(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	want := [][]string{{"publish", dir, "--provenance"}}
	if got := fake.Publishes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("publish calls = %v, want %v", got, want)
	}
	if !strings.Contains(out.String(), "Provenance: on") {
		t.Fatalf("output:\n%s", out.String())
	}
}

func TestInPlacePublishRunsNPMInTheAuthorsDirectory(t *testing.T) {
	fake := npmtest.Install(t)
	dir := writePackage(t, "1.0.0")
	var out bytes.Buffer
	r := request(dir, npmpublish.Flags{Yes: true}, &out)
	r.InPlace = true
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	for _, call := range fake.Calls() {
		resolved, _ := filepath.EvalSymlinks(dir)
		cwd, _ := filepath.EvalSymlinks(call.Cwd)
		if cwd != resolved {
			t.Fatalf("npm %v ran in %s, want the package directory %s", call.Args, call.Cwd, dir)
		}
	}
	if got := fake.Publishes(); !reflect.DeepEqual(got, [][]string{{"publish"}}) {
		t.Fatalf("publish calls = %v", got)
	}
}

func TestNPMMissingFromPathIsExplained(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var out bytes.Buffer
	err := request(writePackage(t, "1.0.0"), npmpublish.Flags{}, &out).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "npm is not on PATH") {
		t.Fatalf("error = %v", err)
	}
}

func TestFlagsParseAndValidate(t *testing.T) {
	var f npmpublish.Flags
	args := []string{"--tag", "next", "--access=restricted", "--otp", "654321", "--yes", "--other"}
	var handled []string
	for i := 0; i < len(args); i++ {
		ok, err := f.Parse(args, &i)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			handled = append(handled, args[i])
		}
	}
	want := npmpublish.Flags{Yes: true, Tag: "next", Access: "restricted", OTP: "654321"}
	if f != want || slices.Contains(handled, "--other") {
		t.Fatalf("flags = %+v handled = %v", f, handled)
	}
	if err := f.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]npmpublish.Flags{
		"both modes":     {Yes: true, DryRun: true},
		"access":         {Access: "internal"},
		"tag is version": {Tag: "1.2.3"},
		"tag spaces":     {Tag: "my tag"},
		"token as otp":   {OTP: "npm_0123456789abcdefghijklmnopqrstuvwxyz"}, // gitleaks:allow -- synthetic token-shaped fixture, not a credential
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%s: accepted %+v", name, bad)
		}
	}
	for _, args := range [][]string{{"--tag"}, {"--tag="}, {"--access", "--yes"}} {
		var g npmpublish.Flags
		i := 0
		if _, err := g.Parse(args, &i); err == nil {
			t.Errorf("%v: missing value accepted", args)
		}
	}
}

func TestCommandRefusesAPackageManagerThatIsNotNPM(t *testing.T) {
	agent := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", agent)
	if err := os.WriteFile(filepath.Join(agent, "settings.json"), []byte(`{"npmCommand":["pnpm"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := npmpublish.Command(); err == nil || !strings.Contains(err.Error(), "selects pnpm") {
		t.Fatalf("error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(agent, "settings.json"), []byte(`{"npmCommand":["mise","exec","--","npm"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	command, err := npmpublish.Command()
	if err != nil || !slices.Equal(command, []string{"mise", "exec", "--", "npm"}) {
		t.Fatalf("command = %v, %v", command, err)
	}
}

func TestSatisfiableAnswersForRangesAndNeverGuessesOnErrors(t *testing.T) {
	fake := npmtest.Install(t)
	fake.Seed("@acme/tool", "1.4.0")
	ctx := context.Background()
	command := []string{"npm"}
	for spec, want := range map[string]bool{"^1.2.0": true, "1.4.0": true, "^2.0.0": false, "1.5.0": false} {
		got, err := npmpublish.Satisfiable(ctx, command, "@acme/tool", spec, "")
		if err != nil || got != want {
			t.Errorf("@acme/tool@%s = %v, %v; want %v", spec, got, err, want)
		}
	}
	if got, err := npmpublish.Satisfiable(ctx, command, "@acme/missing", "^1.0.0", ""); err != nil || got {
		t.Errorf("a package that is not on npm = %v, %v", got, err)
	}
	fake.GoOffline()
	if _, err := npmpublish.Satisfiable(ctx, command, "@acme/tool", "^1.0.0", ""); err == nil || !strings.Contains(err.Error(), "npm view @acme/tool@^1.0.0 failed") {
		t.Errorf("an unreachable registry: %v", err)
	}
}

// Authors often publish from the Package's own directory, and a Package may ship its own npm there. Go's LookPath on Windows searches the working directory before PATH and then fails with ErrDot, and on every platform a relative PATH entry such as "." names the working directory. npm must come from the absolute PATH entries, so publishing neither fails nor runs the Package's npm.
func TestPublishFindsNPMOnPathWhenPiGRunsInThePackageDirectory(t *testing.T) {
	fake := npmtest.Install(t)
	t.Setenv("PATH", "."+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("NoDefaultCurrentDirectoryInExePath", "")
	if err := os.Unsetenv("NoDefaultCurrentDirectoryInExePath"); err != nil {
		t.Fatal(err)
	}
	dir := writePackage(t, "1.0.0")
	marker := filepath.Join(t.TempDir(), "planted-npm-ran")
	planted, program := "npm", "#!/bin/sh\n: > '"+marker+"'\n"
	if runtime.GOOS == "windows" {
		planted, program = "npm.cmd", "@echo planted> \""+marker+"\"\r\n"
	}
	if err := os.WriteFile(filepath.Join(dir, planted), []byte(program), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	var out bytes.Buffer
	r := request(dir, npmpublish.Flags{}, &out)
	r.InPlace = true
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("dry run in the package directory: %v\n%s", err, out.String())
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("publishing ran the npm in the package directory:\n%s", out.String())
	}
	want := [][]string{{"view", "@acme/tool@1.0.0", "version"}, {"publish", "--dry-run"}}
	var got [][]string
	for _, call := range fake.Calls() {
		got = append(got, call.Args)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("npm calls = %q, want %q\n%s", got, want, out.String())
	}
}
