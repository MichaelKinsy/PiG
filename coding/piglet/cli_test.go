package piglet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/installresolver"
)

func assertNoPigletOutputVersion(t *testing.T, output string) {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal([]byte(output), &document); err != nil {
		t.Fatal(err)
	}
	if _, exists := document["version"]; exists {
		t.Fatalf("Piglet JSON exposed a Pig-owned format version: %s", output)
	}
}

func TestPigletHelpListsCurrentSurface(t *testing.T) {
	var stdout, stderr strings.Builder
	if code := RunCommand([]string{"piglet", "--help"}, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	commands := map[string]struct{}{}
	for line := range strings.SplitSeq(stdout.String(), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "pig piglet ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 3 {
			commands[fields[2]] = struct{}{}
		}
	}
	got := slices.Sorted(maps.Keys(commands))
	want := []string{"add", "build", "keygen", "list", "publish", "pull", "remove", "schema", "show", "trust", "validate", "verify"}
	if !slices.Equal(got, want) {
		t.Fatalf("Piglet help commands = %v, want %v\n%s", got, want, stdout.String())
	}
	for _, want := range []string{"add <path|npm:ref|git:ref>", "publish <name|path> --to github --repo <owner/repo> --sign-key <key>", "Catalog refs require", "image, --locked, and --record are reserved"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("Piglet help missing %q:\n%s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "--image") {
		t.Fatalf("Piglet help advertises the unavailable remove --image flag:\n%s", stdout.String())
	}
}

func TestRunCommandRemoveRejectsUnavailableImageFacet(t *testing.T) {
	var stdout, stderr strings.Builder
	code := RunCommand([]string{"piglet", "remove", "example", "--image"}, &stdout, &stderr)
	if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "[--source|--binary|--all]") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

// TestRunCommandDispatch pins the current piglet verb set.
func TestRunCommandRemoveJSONIsSingleStructuredResult(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	t.Chdir(t.TempDir())
	writeRecordFixture(t, root, "binary")
	var stdout, stderr strings.Builder
	code := RunCommand([]string{"piglet", "remove", "binary", "--binary", "--json", "--no-input"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	assertNoPigletOutputVersion(t, stdout.String())
	var output pigletCommandOutput
	if err := json.Unmarshal([]byte(stdout.String()), &output); err != nil {
		t.Fatal(err)
	}
	if output.Command != "remove" || !output.Success || !strings.Contains(output.Output, "removed Piglet Binary record") {
		t.Fatalf("output = %#v", output)
	}
}

func TestRemovePigletFacetsRollsBackStagedFilesOnFailure(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	if err := os.WriteFile(first, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := removePigletFacets([]pigletFacetRemoval{{path: first, label: "first"}, {path: filepath.Join(root, "missing"), label: "missing"}})
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("error = %v", err)
	}
	if data, err := os.ReadFile(first); err != nil || string(data) != "first" {
		t.Fatalf("first facet was not restored: data=%q err=%v", data, err)
	}
}

func TestRunCommandAddRejectsIncompleteDevContainerClosureCopy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".devcontainer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".devcontainer", "devcontainer.json"), []byte(`{"image":"golang:1.26"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "piglet.yaml")
	if err := os.WriteFile(source, []byte("name: dev\nagentEnv:\n  devContainer: .devcontainer/devcontainer.json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	code := RunCommand([]string{"piglet", "add", source, "--no-input"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "cannot copy an agentEnv.devContainer closure") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, "piglets", "dev.yaml")); !os.IsNotExist(err) {
		t.Fatalf("Piglet was partially added: %v", err)
	}
}

func TestRunCommandAddRejectsRelativeLocalResourceOrigins(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*testing.T, string)
		yaml  func(string) string
		want  []string
	}{
		{
			name: "extension",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(root, "extensions", "tools"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			yaml: func(string) string {
				return "name: blocked\nextensions:\n  - name: tools\n    origins: [local:./extensions/tools]\n"
			},
			want: []string{"FAIL: extension tools (local:./extensions/tools)"},
		},
		{
			name: "skill",
			setup: func(t *testing.T, root string) {
				t.Helper()
				dir := filepath.Join(root, "skills", "review")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: review\n---\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			yaml: func(string) string {
				return "name: blocked\nskills:\n  - name: review\n    origins: [local:./skills/review]\n"
			},
			want: []string{"FAIL: skill review (local:./skills/review)"},
		},
		{
			name: "unused fallback",
			setup: func(t *testing.T, root string) {
				t.Helper()
				packageSkill := filepath.Join(root, "package", "skills", "fallback")
				localSkill := filepath.Join(root, "skills", "fallback")
				for _, dir := range []string{packageSkill, localSkill} {
					if err := os.MkdirAll(dir, 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: fallback\n---\n"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(root, "package", "package.json"), []byte(`{"name":"fallback-package"}`), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			yaml: func(root string) string {
				return "name: blocked\npackages:\n  base: local:" + filepath.Join(root, "package") + "\nskills:\n  - name: fallback\n    origins: [package:base, local:./skills/fallback]\n"
			},
			want: []string{"FAIL: skill fallback (local:./skills/fallback)"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("PIG_HOME", home)
			root := t.TempDir()
			tc.setup(t, root)
			source := filepath.Join(root, "piglet.yaml")
			if err := os.WriteFile(source, []byte(tc.yaml(root)), 0o644); err != nil {
				t.Fatal(err)
			}

			var stdout, stderr strings.Builder
			code := RunCommand([]string{"piglet", "add", source, "--no-input"}, &stdout, &stderr)
			if code != 1 {
				t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			for _, want := range append(tc.want, "Run the Piglet from its source path", "portable origin") {
				if !strings.Contains(stderr.String(), want) {
					t.Fatalf("stderr %q missing %q", stderr.String(), want)
				}
			}
			if count := strings.Count(stderr.String(), "Run the Piglet from its source path"); count != 1 {
				t.Fatalf("guidance count = %d in stderr %q", count, stderr.String())
			}
			if _, err := os.Stat(filepath.Join(home, "piglets", "blocked.yaml")); !os.IsNotExist(err) {
				t.Fatalf("Piglet was partially added: %v", err)
			}
		})
	}
}

func TestValidatePigletAddOriginsPreservesAbsoluteLocalOrigin(t *testing.T) {
	origin := "local:" + t.TempDir()
	p := &Piglet{Skills: []SkillEntry{{Name: "review", Origins: []string{origin}}}}
	if err := validatePigletAddOrigins(p); err != nil {
		t.Fatalf("absolute local origin %q was rejected: %v", origin, err)
	}
}

func TestRunCommandAddJSONIsSingleStructuredResult(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	source := filepath.Join(t.TempDir(), "release.yaml")
	if err := os.WriteFile(source, []byte("name: release\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	code := RunCommand([]string{"piglet", "add", source, "--json", "--no-input"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	assertNoPigletOutputVersion(t, stdout.String())
	var output pigletCommandOutput
	if err := json.Unmarshal([]byte(stdout.String()), &output); err != nil {
		t.Fatal(err)
	}
	if output.Command != "add" || !output.Success || !strings.Contains(output.Output, "Added release") {
		t.Fatalf("output = %#v", output)
	}
}

func TestRunCommandAddRemoteSourcesWritesOriginAndListsIt(t *testing.T) {
	cases := []struct {
		name                string
		source              string
		pigletName          string
		manifestRelative    string
		wantResolvedVersion string
		wantIntegrity       string
		git                 bool
	}{
		{
			name: "npm package manifest path", source: "npm:@acme/review@^1.0.0", pigletName: "npm-review",
			manifestRelative: "config/review.yaml", wantResolvedVersion: "1.2.3", wantIntegrity: "sha512-fixture",
		},
		{
			name: "git fallback path", source: "git:ssh://git@github.example/acme/review.git@v2.0.0", pigletName: "git-review",
			manifestRelative: "piglet.yaml", wantResolvedVersion: "v2.0.0", git: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("PIG_HOME", home)
			t.Setenv("PIG_OFFLINE", "")
			t.Setenv("PI_OFFLINE", "")
			t.Chdir(t.TempDir())
			var installRoot string
			root := t.TempDir()
			if !tc.git {
				installRoot = root
				root = filepath.Join(installRoot, "node_modules", "@acme", "review")
			}
			pigletPath := filepath.Join(root, filepath.FromSlash(tc.manifestRelative))
			if err := os.MkdirAll(filepath.Dir(pigletPath), 0o755); err != nil {
				t.Fatal(err)
			}
			pigletData := []byte("name: " + tc.pigletName + "\nsystemPrompt:\n  file: prompts/system.md\n")
			if err := os.WriteFile(pigletPath, pigletData, 0o644); err != nil {
				t.Fatal(err)
			}
			promptPath := filepath.Join(filepath.Dir(pigletPath), "prompts", "system.md")
			if err := os.MkdirAll(filepath.Dir(promptPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(promptPath, []byte("remote prompt\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if tc.manifestRelative != "piglet.yaml" {
				manifest := fmt.Sprintf(`{"name":"@acme/review","version":"1.2.3","pig":{"piglet":%q}}`, tc.manifestRelative)
				if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(manifest), 0o644); err != nil {
					t.Fatal(err)
				}
				lock := `{"lockfileVersion":3,"packages":{"node_modules/@acme/review":{"version":"1.2.3","integrity":"sha512-fixture"}}}`
				if err := os.WriteFile(filepath.Join(installRoot, "package-lock.json"), []byte(lock), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			wantCommit := ""
			if tc.git {
				runGit := func(args ...string) string {
					t.Helper()
					command := exec.Command("git", args...)
					command.Dir = root
					output, err := command.CombinedOutput()
					if err != nil {
						t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
					}
					return strings.TrimSpace(string(output))
				}
				runGit("init", "-q")
				runGit("add", ".")
				runGit("-c", "user.name=Pig Test", "-c", "user.email=pig@example.test", "commit", "-q", "-m", "fixture")
				wantCommit = runGit("rev-parse", "HEAD")
			}
			installresolver.SetMaterializer(func(_, source, scope string, _, _ io.Writer) (string, error) {
				if source != tc.source || scope != "user" {
					t.Fatalf("materialize = %q/%q", source, scope)
				}
				return root, nil
			})
			t.Cleanup(func() { installresolver.SetMaterializer(nil) })

			var stdout, stderr strings.Builder
			code := RunCommand([]string{"piglet", "add", tc.source, "--no-input"}, &stdout, &stderr)
			if code != 0 || stderr.Len() != 0 {
				t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			installed := filepath.Join(home, "piglets", tc.pigletName+".yaml")
			if data, err := os.ReadFile(installed); err != nil || !bytes.Equal(data, pigletData) {
				t.Fatalf("installed Piglet = %q, %v", data, err)
			}
			if data, err := os.ReadFile(filepath.Join(home, "piglets", "prompts", "system.md")); err != nil || string(data) != "remote prompt\n" {
				t.Fatalf("installed prompt = %q, %v", data, err)
			}
			origin, err := readPigletOrigin(installed)
			if err != nil {
				t.Fatal(err)
			}
			if origin == nil || origin.Source != tc.source || origin.ResolvedVersion != tc.wantResolvedVersion || origin.Integrity != tc.wantIntegrity || origin.Commit != wantCommit {
				t.Fatalf("origin = %#v", origin)
			}

			stdout.Reset()
			stderr.Reset()
			code = RunCommand([]string{"piglet", "list", "--json"}, &stdout, &stderr)
			if code != 0 || stderr.Len() != 0 {
				t.Fatalf("list code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			var listed pigletListOutput
			if err := json.Unmarshal([]byte(stdout.String()), &listed); err != nil {
				t.Fatal(err)
			}
			if len(listed.Piglets) != 1 || listed.Piglets[0].Origin == nil || listed.Piglets[0].Origin.Source != tc.source {
				t.Fatalf("list = %#v", listed)
			}

			stdout.Reset()
			stderr.Reset()
			code = RunCommand([]string{"piglet", "add", tc.source, "--no-input"}, &stdout, &stderr)
			if code != 1 || !strings.Contains(stderr.String(), "already installed") || !strings.Contains(stderr.String(), "pig piglet update "+tc.pigletName) {
				t.Fatalf("duplicate code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunCommandAddRemoteRejectsNonPortablePiglet(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_OFFLINE", "")
	t.Setenv("PI_OFFLINE", "")
	root := t.TempDir()
	packageRoot := filepath.Join(root, "package")
	if err := os.MkdirAll(packageRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageRoot, "package.json"), []byte(`{"name":"local"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	pigletData := "name: blocked\npackages:\n  base: local:./package\n"
	if err := os.WriteFile(filepath.Join(root, "piglet.yaml"), []byte(pigletData), 0o644); err != nil {
		t.Fatal(err)
	}
	installresolver.SetMaterializer(func(_, _, _ string, _, _ io.Writer) (string, error) {
		return root, nil
	})
	t.Cleanup(func() { installresolver.SetMaterializer(nil) })
	var stdout, stderr strings.Builder
	code := RunCommand([]string{"piglet", "add", "npm:blocked@1.0.0", "--no-input"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "catalog Piglet package") || !strings.Contains(stderr.String(), "uses local source") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, "piglets", "blocked.yaml")); !os.IsNotExist(err) {
		t.Fatalf("non-portable Piglet was partially installed: %v", err)
	}
}

func TestRunCommandAddRemoteRejectsCredentialBearingGitURL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_OFFLINE", "")
	t.Setenv("PI_OFFLINE", "")
	const credential = "origin-token-must-not-persist"
	called := false
	installresolver.SetMaterializer(func(_, _, _ string, _, _ io.Writer) (string, error) {
		called = true
		return "", nil
	})
	t.Cleanup(func() { installresolver.SetMaterializer(nil) })

	var stdout, stderr strings.Builder
	code := RunCommand([]string{"piglet", "add", "git:https://publisher:" + credential + "@github.example/acme/review.git@v1", "--no-input"}, &stdout, &stderr)
	if code != 1 || called || !strings.Contains(stderr.String(), "must not include credentials") {
		t.Fatalf("code=%d called=%v stdout=%s stderr=%s", code, called, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), credential) || strings.Contains(stderr.String(), credential) {
		t.Fatalf("credential leaked in command output: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	entries, err := os.ReadDir(filepath.Join(home, "piglets"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("credential-bearing source wrote Piglet files: %v", entries)
	}

	pigletsDir := filepath.Join(home, "piglets")
	if err := os.MkdirAll(pigletsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pigletData := []byte("name: leaked-origin\n")
	pigletPath := filepath.Join(pigletsDir, "leaked-origin.yaml")
	if err := os.WriteFile(pigletPath, pigletData, 0o644); err != nil {
		t.Fatal(err)
	}
	originData, err := marshalPigletOrigin(pigletOrigin{
		Source:          "git:https://publisher:" + credential + "@github.example/acme/review.git@v1",
		ResolvedVersion: "v1",
		Commit:          strings.Repeat("a", 40),
		PigletDigest:    digestPigletData(pigletData),
		AddedAt:         "2026-09-24T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(originPathForPiglet(pigletPath), originData, 0o644); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	code = RunCommand([]string{"piglet", "list", "--json"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "must not include credentials") {
		t.Fatalf("list code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), credential) || strings.Contains(stderr.String(), credential) {
		t.Fatalf("credential from existing origin leaked in list output: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunCommandAddRemoteOfflineFailsBeforeMaterialization(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_OFFLINE", "1")
	t.Setenv("PI_OFFLINE", "")
	called := false
	installresolver.SetMaterializer(func(_, _, _ string, _, _ io.Writer) (string, error) {
		called = true
		return "", nil
	})
	t.Cleanup(func() { installresolver.SetMaterializer(nil) })
	var stdout, stderr strings.Builder
	code := RunCommand([]string{"piglet", "add", "npm:offline@1.0.0", "--no-input"}, &stdout, &stderr)
	if code != 1 || called || !strings.Contains(stderr.String(), "while offline") || !strings.Contains(stderr.String(), "PIG_OFFLINE") {
		t.Fatalf("code=%d called=%v stdout=%s stderr=%s", code, called, stdout.String(), stderr.String())
	}
}

func TestRunCommandShowEffectiveExpandsAgentEnvironmentDefaults(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	source := filepath.Join(t.TempDir(), "image.yaml")
	if err := os.WriteFile(source, []byte("name: image\nagentEnv:\n  image: ghcr.io/acme/dev:1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	code := RunCommand([]string{"piglet", "show", source, "--effective", "--json"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var output pigletShowOutput
	if err := json.Unmarshal([]byte(stdout.String()), &output); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(output.Piglet)
	if !output.Effective || !strings.Contains(string(encoded), `"mode":"inject"`) || !strings.Contains(string(encoded), `"version":"latest"`) || !strings.Contains(string(encoded), `"preset":"standard"`) {
		t.Fatalf("output = %#v", output)
	}
}

// Effective show/validate observe defaults without rewriting the user's source
// (R17) and repeated show is stable (#476 AC-9). A show that clobbered the
// hand-authored YAML with machine-expanded defaults is silent data loss, so the
// source bytes must survive every effective read byte-for-byte.
func TestRunCommandEffectiveShowIsStableAndNeverRewritesSource(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	source := filepath.Join(t.TempDir(), "image.yaml")
	original := []byte("name: image\nagentEnv:\n  image: ghcr.io/acme/dev:1\n")
	if err := os.WriteFile(source, original, 0o644); err != nil {
		t.Fatal(err)
	}

	show := func() string {
		t.Helper()
		var stdout, stderr strings.Builder
		code := RunCommand([]string{"piglet", "show", source, "--effective", "--json"}, &stdout, &stderr)
		if code != 0 || stderr.Len() != 0 {
			t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
		}
		return stdout.String()
	}

	first := show()
	if second := show(); first != second {
		t.Fatalf("effective show not stable across runs:\nfirst=%s\nsecond=%s", first, second)
	}

	// validate must also leave the source untouched even though this image
	// piglet fails validation (host fallback forbidden).
	var vout, verr strings.Builder
	RunCommand([]string{"piglet", "validate", source, "--json", "--no-input"}, &vout, &verr)

	after, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, original) {
		t.Fatalf("source rewritten by effective show/validate:\nwant=%q\ngot =%q", original, after)
	}
}

// Resolved secret values must never be returned by an inspection API
// (secrets.go ResolvedSecrets contract). "piglet show --effective" is exactly
// such an API: it must project the declaration and its binding, never resolve
// the live value, even when that value is present and resolvable in the
// environment. Lower layers guard the struct marshal and the projection; this
// drives the production RunCommand entrypoint with a resolvable secret present
// so a future effective-show change that eagerly resolves and leaks a value is
// caught end-to-end.
func TestRunCommandEffectiveShowNeverLeaksResolvedSecretValue(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	const secretValue = "SUPER-SECRET-VALUE-DO-NOT-LEAK"
	t.Setenv("PROBE_ENV_SECRET", secretValue)
	source := filepath.Join(t.TempDir(), "leak.yaml")
	pigletYAML := "name: leak\nsecrets:\n" +
		"  - name: token\n    from: {env: PROBE_ENV_SECRET}\n" +
		"agentEnv:\n  image: dev:1\n  secrets:\n    - secretRef: token\n      target: {env: TOKEN}\n"
	if err := os.WriteFile(source, []byte(pigletYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	code := RunCommand([]string{"piglet", "show", source, "--effective", "--json"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), secretValue) {
		t.Fatalf("effective show leaked resolved secret value into inspection output:\n%s", stdout.String())
	}
	// The declaration and binding must still be present (the value is what is
	// withheld, not the shape), so the assertion above cannot pass by emitting
	// nothing.
	if !strings.Contains(stdout.String(), "token") || !strings.Contains(stdout.String(), "TOKEN") {
		t.Fatalf("effective show dropped the secret declaration/binding entirely:\n%s", stdout.String())
	}
}

func TestRunCommandValidateJSONReportsFailureWithoutStderr(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	source := filepath.Join(t.TempDir(), "broken.yaml")
	if err := os.WriteFile(source, []byte("name: broken\nskills:\n  - name: missing\n    origins: [local:./missing]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	code := RunCommand([]string{"piglet", "validate", source, "--json", "--no-input"}, &stdout, &stderr)
	if code != 1 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	assertNoPigletOutputVersion(t, stdout.String())
	var output pigletValidationOutput
	if err := json.Unmarshal([]byte(stdout.String()), &output); err != nil {
		t.Fatal(err)
	}
	if output.Valid || len(output.Errors) == 0 || !strings.Contains(output.Errors[0], "missing") {
		t.Fatalf("output = %#v", output)
	}
}

func TestRunCommandValidateJSONReportsEffectiveAgentEnvironment(t *testing.T) {
	source := filepath.Join(t.TempDir(), "image.yaml")
	if err := os.WriteFile(source, []byte("name: image\nagentEnv:\n  image: ghcr.io/acme/dev:1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	code := RunCommand([]string{"piglet", "validate", source, "--json", "--no-input"}, &stdout, &stderr)
	if code != 1 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var output pigletValidationOutput
	if err := json.Unmarshal([]byte(stdout.String()), &output); err != nil {
		t.Fatal(err)
	}
	environment := output.AgentEnvironment
	if environment == nil || environment.Form != "image" || environment.Value != "ghcr.io/acme/dev:1" || environment.RuntimeMode != "inject" || environment.RuntimeVersion != "latest" || environment.PolicyPreset != "standard" || output.Valid || len(output.Errors) != 1 || !strings.Contains(output.Errors[0], "host fallback is forbidden") {
		t.Fatalf("output = %#v", output)
	}
}

func TestRunCommandValidateAcceptsSupportedImageRuntimeSlice(t *testing.T) {
	source := filepath.Join(t.TempDir(), "image.yaml")
	if err := os.WriteFile(source, []byte("name: image\nagentEnv:\n  image: ghcr.io/acme/dev:1\n  pigRuntime:\n    mode: image\n    version: 0.81.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	code := RunCommand([]string{"piglet", "validate", source, "--json", "--no-input"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var output pigletValidationOutput
	if err := json.Unmarshal([]byte(stdout.String()), &output); err != nil {
		t.Fatal(err)
	}
	if !output.Valid || output.AgentEnvironment == nil || output.AgentEnvironment.RuntimeMode != "image" {
		t.Fatalf("output = %#v", output)
	}
}

func TestRunCommandRemoveRequiresFacetWhenSourceAndBinaryExist(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	t.Chdir(t.TempDir())
	pigletsDir := filepath.Join(root, "piglets")
	if err := os.MkdirAll(pigletsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(pigletsDir, "release.yaml")
	if err := os.WriteFile(source, []byte("name: release\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	digest, err := pigletSourceDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	record := writeRecordFixtureWithPigletDigest(t, root, "release", strings.TrimPrefix(digest, "sha256:"))
	var stdout, stderr strings.Builder
	code := RunCommand([]string{"piglet", "remove", "release"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "choose --source, --binary, or --all") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	for _, path := range []string{source, record} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("facet changed after ambiguous remove: %s: %v", path, err)
		}
	}
}

func TestRunCommandRemoveBinaryKeepsSource(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	t.Chdir(t.TempDir())
	pigletsDir := filepath.Join(root, "piglets")
	if err := os.MkdirAll(pigletsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(pigletsDir, "release.yaml")
	if err := os.WriteFile(source, []byte("name: release\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	digest, err := pigletSourceDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	record := writeRecordFixtureWithPigletDigest(t, root, "release", strings.TrimPrefix(digest, "sha256:"))
	piglets, err := List()
	if err != nil || len(piglets) != 1 || len(piglets[0].Records) != 1 {
		t.Fatalf("piglets=%v err=%v", piglets, err)
	}
	artifactPath := piglets[0].Records[0].ArtifactPath
	resolutionRecord := piglets[0].Records[0].ResolutionPath
	var stdout, stderr strings.Builder
	code := RunCommand([]string{"piglet", "remove", "release", "--binary"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source removed: %v", err)
	}
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Fatalf("record remains: %v", err)
	}
	if _, err := os.Stat(artifactPath); !os.IsNotExist(err) {
		t.Fatalf("managed artifact remains: %v", err)
	}
	if _, err := os.Stat(resolutionRecord); !os.IsNotExist(err) {
		t.Fatalf("resolution record remains: %v", err)
	}
}

func TestRunCommandListJSONIncludesFacets(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	t.Chdir(t.TempDir())
	writeRecordFixture(t, root, "binary-only")
	var stdout, stderr strings.Builder
	code := RunCommand([]string{"piglet", "list", "--json", "--no-input"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), `"receipts"`) || !strings.Contains(stdout.String(), `"records"`) {
		t.Fatalf("list JSON retained removed receipt field: %s", stdout.String())
	}
	assertNoPigletOutputVersion(t, stdout.String())
	var output pigletListOutput
	if err := json.Unmarshal([]byte(stdout.String()), &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Piglets) != 1 || output.Piglets[0].Name != "binary-only" || len(output.Piglets[0].Records) != 1 {
		t.Fatalf("output = %#v", output)
	}
}

func TestRunCommandListShowsBinaryFacet(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	t.Chdir(t.TempDir())
	writeRecordFixture(t, root, "binary-only")
	var stdout, stderr strings.Builder
	code := RunCommand([]string{"piglet", "list"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "BINARY") || !strings.Contains(stdout.String(), "linux/amd64") || !strings.Contains(stdout.String(), "binary-only") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestRunCommandShowJSONUsesPigletSchemaFieldNames(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	t.Chdir(t.TempDir())
	pigletsDir := filepath.Join(root, "piglets")
	if err := os.MkdirAll(pigletsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pigletsDir, "release.yaml"), []byte("name: release\nbuild:\n  outputName: pig-release\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	code := RunCommand([]string{"piglet", "show", "release", "--json"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"piglet": {`) || !strings.Contains(stdout.String(), `"build": {`) || strings.Contains(stdout.String(), `"Version"`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &document); err != nil {
		t.Fatal(err)
	}
	if _, exists := document["version"]; exists {
		t.Fatalf("show envelope exposed a Pig-owned format version: %s", stdout.String())
	}
	pigletDocument, _ := document["piglet"].(map[string]any)
	if _, exists := pigletDocument["version"]; exists {
		t.Fatalf("Piglet source exposed a Pig-owned format version: %s", stdout.String())
	}
}

func TestRunCommandShowRecordFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	record := writeRecordFixture(t, root, "release")
	var stdout, stderr strings.Builder
	code := RunCommand([]string{"piglet", "show", "--record", record, "--json"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"piglet": "release"`) || !strings.Contains(stdout.String(), `"passed": true`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestRunCommandShowBinaryOnlyPiglet(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	t.Chdir(t.TempDir())
	writeRecordFixture(t, root, "binary-only")
	var stdout, stderr strings.Builder
	code := RunCommand([]string{"piglet", "show", "binary-only"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "binary-only") || !strings.Contains(stdout.String(), "Source: none") || !strings.Contains(stdout.String(), "linux/amd64") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestRunCommandValidateReportsInlineSkills(t *testing.T) {
	pigletPath := filepath.Join(t.TempDir(), "porter.yaml")
	if err := os.WriteFile(pigletPath, []byte("name: porter\nskills:\n  - name: porter\n    content: instructions\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	code := RunCommand([]string{"piglet", "validate", pigletPath}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{"OK: skill porter (<inline>)", "1 skill(s)"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout %q missing %q", stdout.String(), want)
		}
	}
}

func TestRunCommandDispatch(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir()) // empty home: no piglets discovered
	t.Chdir(t.TempDir())              // empty cwd: no workspace .pig/piglets

	type want struct {
		code      int
		outHas    string
		errHas    string
		errNotHas string
	}
	cases := map[string]struct {
		args []string
		want want
	}{
		"list":               {[]string{"piglet", "list"}, want{code: 0}},
		"validate needs arg": {[]string{"piglet", "validate"}, want{code: 2, errHas: "pig piglet validate"}},
		"add needs arg":      {[]string{"piglet", "add"}, want{code: 2, errHas: "pig piglet add"}},
		"pull needs arg":     {[]string{"piglet", "pull"}, want{code: 2, errHas: "release reference"}},
		"remove needs arg":   {[]string{"piglet", "remove"}, want{code: 2, errHas: "Usage: pig piglet remove"}},
		"unknown verb":       {[]string{"piglet", "frobnicate"}, want{code: 1, errHas: "Unknown piglet command"}},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var out, errb bytes.Buffer
			code := RunCommand(tc.args, &out, &errb)
			if code != tc.want.code {
				t.Fatalf("code = %d, want %d (stderr=%q)", code, tc.want.code, errb.String())
			}
			if tc.want.outHas != "" && !strings.Contains(out.String(), tc.want.outHas) {
				t.Errorf("stdout %q missing %q", out.String(), tc.want.outHas)
			}
			if tc.want.errHas != "" && !strings.Contains(errb.String(), tc.want.errHas) {
				t.Errorf("stderr %q missing %q", errb.String(), tc.want.errHas)
			}
			if tc.want.errNotHas != "" && strings.Contains(errb.String(), tc.want.errNotHas) {
				t.Errorf("stderr %q should not contain %q", errb.String(), tc.want.errNotHas)
			}
		})
	}
}
