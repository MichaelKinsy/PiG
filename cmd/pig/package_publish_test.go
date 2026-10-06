package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/npmpublish/npmtest"
)

const goodPackageJSON = `{
  "name": "@acme/review",
  "version": "1.2.0",
  "description": "Review helpers for PiG",
  "keywords": ["pig-package", "review"],
  "repository": {"type": "git", "url": "git+https://github.com/acme/review.git"},
  "pi": {"extensions": ["extensions/*"]}
}`

func writePublishablePackage(t *testing.T, packageJSON string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"package.json":               packageJSON,
		"extensions/runner/index.js": "export default function extension(pi) {}\n",
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

func runPackagePublishForTest(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runPackagePublish(context.Background(), args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestPackagePublishDryRunValidatesAndPublishesNothing(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	fake := npmtest.Install(t)
	dir := writePublishablePackage(t, goodPackageJSON)

	code, stdout, stderr := runPackagePublishForTest(t, dir, "--to", "npm")

	if code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{"Package npm publish dry run\nPackage: @acme/review@1.2.0 (not on npm yet)\n", "extensions: 1", "Would run: npm publish\n", "rerun with --yes"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if len(fake.Publishes()) != 0 {
		t.Fatalf("dry run published: %v", fake.Publishes())
	}
	for _, call := range fake.Calls() {
		if cwd, _ := filepath.EvalSymlinks(call.Cwd); cwd != mustEval(t, dir) {
			t.Fatalf("npm %v ran in %s, want the Package directory", call.Args, call.Cwd)
		}
	}
}

func mustEval(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestPackagePublishYesRunsPlainNPMPublishInThePackage(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	fake := npmtest.Install(t)
	dir := writePublishablePackage(t, goodPackageJSON)

	code, stdout, stderr := runPackagePublishForTest(t, dir, "--to=npm", "--yes", "--access", "public")

	if code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if got, want := fake.Publishes(), [][]string{{"publish", "--access", "public"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("publish calls = %v, want %v", got, want)
	}
	if !fake.Published("@acme/review", "1.2.0") {
		t.Fatal("not published")
	}
	for _, want := range []string{"Published @acme/review@1.2.0 to npm.", "Install it with: pig install npm:@acme/review"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	// The author's package.json is not edited by publication.
	if data, _ := os.ReadFile(filepath.Join(dir, "package.json")); string(data) != goodPackageJSON {
		t.Fatalf("package.json was rewritten:\n%s", data)
	}
}

func TestPackagePublishDefaultsToTheCurrentDirectory(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	fake := npmtest.Install(t)
	dir := writePublishablePackage(t, goodPackageJSON)
	t.Chdir(dir)

	if code, stdout, stderr := runPackagePublishForTest(t, "--to", "npm", "--yes"); code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !fake.Published("@acme/review", "1.2.0") {
		t.Fatal("not published")
	}
}

func TestPackagePublishRefusesWhatTheCatalogCannotList(t *testing.T) {
	for name, tc := range map[string]struct {
		packageJSON string
		want        string
	}{
		"no keyword":      {`{"name":"@acme/review","version":"1.2.0","description":"d","keywords":["review"]}`, `keywords lack "pig-package"`},
		"no keywords":     {`{"name":"@acme/review","version":"1.2.0","description":"d"}`, `keywords lack "pig-package"`},
		"no description":  {`{"name":"@acme/review","version":"1.2.0","keywords":["pig-package"]}`, "needs a description"},
		"no name":         {`{"version":"1.2.0","description":"d","keywords":["pig-package"]}`, "requires a non-empty name"},
		"bad name":        {`{"name":"Review","version":"1.2.0","description":"d","keywords":["pig-package"]}`, "is not an npm package name"},
		"no version":      {`{"name":"@acme/review","description":"d","keywords":["pig-package"]}`, "needs a version"},
		"bad version":     {`{"name":"@acme/review","version":"v1","description":"d","keywords":["pig-package"]}`, "not a version npm publishes"},
		"short version":   {`{"name":"@acme/review","version":"1.2","description":"d","keywords":["pig-package"]}`, "not a version npm publishes"},
		"private":         {`{"name":"@acme/review","version":"1.2.0","private":true,"description":"d","keywords":["pig-package"]}`, "is private"},
		"invalid package": {`{"name":"@acme/review","version":"1.2.0","description":"d","keywords":["pig-package"],"pi":{"extensions":["missing/*.js","../escape"]}}`, "Package invalid"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("PIG_HOME", t.TempDir())
			fake := npmtest.Install(t)
			dir := writePublishablePackage(t, tc.packageJSON)
			code, stdout, stderr := runPackagePublishForTest(t, dir, "--to", "npm", "--yes")
			if code != 1 || stdout != "" || !strings.Contains(stderr, tc.want) {
				t.Fatalf("code=%d stdout=%s stderr=%s\nwant %q", code, stdout, stderr, tc.want)
			}
			if len(fake.Calls()) != 0 {
				t.Fatalf("npm ran for an unpublishable Package: %v", fake.Calls())
			}
		})
	}
}

func TestPackagePublishRefusesAnExistingVersion(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	fake := npmtest.Install(t)
	fake.Seed("@acme/review", "1.2.0")
	dir := writePublishablePackage(t, goodPackageJSON)
	code, _, stderr := runPackagePublishForTest(t, dir, "--to", "npm", "--yes")
	if code != 1 || !strings.Contains(stderr, "@acme/review@1.2.0 is already on npm") || len(fake.Publishes()) != 0 {
		t.Fatalf("code=%d stderr=%s publishes=%v", code, stderr, fake.Publishes())
	}
}

func TestPackagePublishArgumentErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"no destination": {[]string{"."}, "--to is required"},
		"git":            {[]string{".", "--to", "git"}, "use --to npm"},
		"two dirs":       {[]string{"a", "b", "--to", "npm"}, `unexpected argument "b"`},
		"unknown":        {[]string{".", "--to", "npm", "--bogus"}, `unknown option "--bogus"`},
		"modes":          {[]string{".", "--to", "npm", "--yes", "--dry-run"}, "cannot be combined"},
		"access":         {[]string{".", "--to", "npm", "--access", "x"}, "--access must be"},
	} {
		t.Run(name, func(t *testing.T) {
			code, _, stderr := runPackagePublishForTest(t, tc.args...)
			if code != 2 || !strings.Contains(stderr, tc.want) || !strings.Contains(stderr, "pig package publish [<dir>] --to npm") {
				t.Fatalf("code=%d stderr=%s\nwant %q", code, stderr, tc.want)
			}
		})
	}
	if code, stdout, _ := runPackagePublishForTest(t, "--help"); code != 0 || !strings.Contains(stdout, "plain `npm publish`") {
		t.Fatalf("help: code=%d stdout=%s", code, stdout)
	}
}

func TestPackageCommandRoutesPublish(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	fake := npmtest.Install(t)
	dir := writePublishablePackage(t, goodPackageJSON)
	out, errOut, code := captureStdoutStderr(t, func() int {
		return runPackageManagementCommand([]string{"publish", dir, "--to", "npm"})
	})
	if code != 0 || !strings.Contains(out, "Package npm publish dry run") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, out, errOut)
	}
	if len(fake.Publishes()) != 0 {
		t.Fatal("dry run published")
	}
}
