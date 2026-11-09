// SPDX-License-Identifier: MIT

//go:build linux

package ciimages

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// These tests run the real run: blocks of release-candidate.yml under bash,
// the way a runner does (shell: bash is `bash --noprofile --norc -eo pipefail`),
// with fixtures for what the step would find on a runner.

type workflowStep struct {
	Name  string
	Run   string
	If    string
	Shell string
	Env   map[string]string
}

func releaseWorkflowSteps(t *testing.T, job string) []workflowStep {
	t.Helper()
	var workflow struct {
		Jobs map[string]struct {
			Steps []workflowStep
		}
	}
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "release-candidate.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	steps := workflow.Jobs[job].Steps
	if len(steps) == 0 {
		t.Fatalf("release-candidate.yml has no job %q", job)
	}
	return steps
}

func releaseWorkflowStep(t *testing.T, job, name string) workflowStep {
	t.Helper()
	for _, step := range releaseWorkflowSteps(t, job) {
		if step.Name == name {
			if step.Run == "" {
				t.Fatalf("%s step %q has no run block", job, name)
			}
			return step
		}
	}
	t.Fatalf("release-candidate.yml job %q has no step %q", job, name)
	return workflowStep{}
}

// runWorkflowStep runs a step's run block in dir with only the given variables
// (plus PATH and HOME) and returns the combined output and the exit error.
func runWorkflowStep(t *testing.T, step workflowStep, dir string, env map[string]string, pathPrefix string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", "--noprofile", "--norc", "-eo", "pipefail", "-c", step.Run)
	cmd.Dir = dir
	path := os.Getenv("PATH")
	if pathPrefix != "" {
		path = pathPrefix + string(os.PathListSeparator) + path
	}
	cmd.Env = []string{"PATH=" + path, "HOME=" + dir, "RUNNER_TEMP=" + filepath.Join(dir, "runner-temp")}
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func writeExecutable(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o755); err != nil {
		t.Fatal(err)
	}
}

// R3: attestation runs only in a public repository, but assemble-evidence
// requires a Sigstore bundle for every archive, so a run from a private
// repository would build everything and stop at publish. The run must refuse
// before the first build step.
func TestReleaseCandidateRefusesAPrivateRepositoryBeforeBuilding(t *testing.T) {
	steps := releaseWorkflowSteps(t, "source")
	guard := steps[0]
	if guard.Run == "" || guard.Env["REPOSITORY_PRIVATE"] != "${{ github.event.repository.private }}" {
		t.Fatalf("the first step of the source job (every other job needs it) is %q; want a run step that reads REPOSITORY_PRIVATE from github.event.repository.private, got env %v", guard.Name, guard.Env)
	}
	dir := t.TempDir()
	out, err := runWorkflowStep(t, guard, dir, map[string]string{"REPOSITORY_PRIVATE": "true"}, "")
	if err == nil {
		t.Fatalf("guard passed for a private repository:\n%s", out)
	}
	for _, want := range []string{"private", "sigstore bundle", "assemble-evidence", "attestation"} {
		if !strings.Contains(strings.ToLower(out), want) {
			t.Errorf("guard message lacks %q:\n%s", want, out)
		}
	}
	// A repository object without a private value renders an empty string (or no variable at all): that is not "private".
	for name, env := range map[string]map[string]string{
		"false":   {"REPOSITORY_PRIVATE": "false"},
		"empty":   {"REPOSITORY_PRIVATE": ""},
		"missing": {},
	} {
		if out, err := runWorkflowStep(t, guard, dir, env, ""); err != nil {
			t.Fatalf("guard refused a repository whose private value is %s: %v\n%s", name, err, out)
		}
	}
	// binary needs source, acp and the smoke jobs need binary, publish needs all of them.
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "release-candidate.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "    needs: source\n") {
		t.Fatal("the binary job no longer waits for the source job that carries the guard")
	}
}

// R9: the draft Release body is the reviewed notes draft when the repository
// holds one for the version, GitHub's generated notes otherwise.
func TestReleaseCandidateDraftBodyIsTheNotesDraftWhenPresent(t *testing.T) {
	step := releaseWorkflowStep(t, "publish", "Create the draft GitHub Release")
	for _, tc := range []struct {
		name      string
		draft     bool
		wantNotes string
		wantGen   bool
	}{
		{"notes draft present", true, "docs/release/0.8.0-release-notes-draft.md", false},
		{"no notes draft", false, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			argsFile := filepath.Join(dir, "gh-args")
			writeExecutable(t, filepath.Join(bin, "gh"), "#!/bin/sh\nprintf '%s\\n' \"$@\" > \""+argsFile+"\"\n")
			if err := os.MkdirAll(filepath.Join(dir, "release"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "release", "pig-0.8.0-linux-amd64.tar.gz"), []byte("archive"), 0o644); err != nil {
				t.Fatal(err)
			}
			// A draft for another version never stands in for this one.
			if err := os.MkdirAll(filepath.Join(dir, "docs", "release"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "docs", "release", "0.7.0-release-notes-draft.md"), []byte("old"), 0o644); err != nil {
				t.Fatal(err)
			}
			if tc.draft {
				if err := os.WriteFile(filepath.Join(dir, "docs", "release", "0.8.0-release-notes-draft.md"), []byte("# notes"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			env := map[string]string{"GH_TOKEN": "x", "VERSION": "0.8.0", "GITHUB_REPOSITORY": "o/r", "GITHUB_SHA": strings.Repeat("a", 40)}
			if out, err := runWorkflowStep(t, step, dir, env, bin); err != nil {
				t.Fatalf("step failed: %v\n%s", err, out)
			}
			raw, err := os.ReadFile(argsFile)
			if err != nil {
				t.Fatal(err)
			}
			args := strings.Split(strings.TrimSpace(string(raw)), "\n")
			has := func(arg string) bool {
				for _, a := range args {
					if a == arg {
						return true
					}
				}
				return false
			}
			if !has("--draft") || !has("v0.8.0") || !has("release/pig-0.8.0-linux-amd64.tar.gz") {
				t.Fatalf("gh args = %q, want a draft release v0.8.0 with the archive", args)
			}
			if got := has("--generate-notes"); got != tc.wantGen {
				t.Errorf("--generate-notes present = %v, want %v (args %q)", got, tc.wantGen, args)
			}
			notes := ""
			for i, a := range args {
				if a == "--notes-file" && i+1 < len(args) {
					notes = args[i+1]
				}
			}
			if notes != tc.wantNotes {
				t.Errorf("--notes-file = %q, want %q (args %q)", notes, tc.wantNotes, args)
			}
		})
	}
}

// The generator writes a banner near the top of the notes draft; published as the Release body it would say
// "Draft. Not published." on a published release. The publish step refuses a draft that still has it instead of
// stripping it: the owner's edit is the review.
func TestReleaseCandidateRefusesANotesDraftThatStillHasTheBanner(t *testing.T) {
	step := releaseWorkflowStep(t, "publish", "Create the draft GitHub Release")
	source, err := os.ReadFile(filepath.Join(repoRoot(t), "automation", "ci", "bugledger", "notes.go"))
	if err != nil {
		t.Fatal(err)
	}
	const banner = "> **Draft. Not published.**"
	if !strings.Contains(string(source), banner) {
		t.Fatalf("automation/ci/bugledger/notes.go no longer writes %q; update the publish step's banner check and this test together", banner)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	argsFile := filepath.Join(dir, "gh-args")
	writeExecutable(t, filepath.Join(bin, "gh"), "#!/bin/sh\nprintf '%s\\n' \"$@\" > \""+argsFile+"\"\n")
	if err := os.MkdirAll(filepath.Join(dir, "release"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "release", "pig-0.8.0-linux-amd64.tar.gz"), []byte("archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "docs", "release"), 0o755); err != nil {
		t.Fatal(err)
	}
	draft := filepath.Join(dir, "docs", "release", "0.8.0-release-notes-draft.md")
	if err := os.WriteFile(draft, []byte("# PiG 0.8.0 release notes (draft)\n\n"+banner+" `make release-notes-draft` generated this file.\n\n## Summary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"GH_TOKEN": "x", "VERSION": "0.8.0", "GITHUB_REPOSITORY": "o/r", "GITHUB_SHA": strings.Repeat("a", 40)}
	out, err := runWorkflowStep(t, step, dir, env, bin)
	if err == nil {
		t.Fatalf("publish step accepted a notes draft that still has the banner:\n%s", out)
	}
	for _, want := range []string{"docs/release/0.8.0-release-notes-draft.md", "Draft. Not published.", "Edit the draft"} {
		if !strings.Contains(out, want) {
			t.Errorf("refusal lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(argsFile); err == nil {
		t.Error("gh release create ran although the draft still has the banner")
	}
}

// fakePig is a pig whose three invoked commands print what the real ones
// print: --version is Pi's version, `version` names PiG's.
func fakePig(pigVersionLine string) string {
	return "#!/bin/sh\ncase \"$1\" in\n--help) echo usage;;\n--version) echo 1.5.0;;\nversion) printf '%s\\nupstream pi: 1.5.0+c23b9ec1\\ngo: go1.27.3\\nplatform: linux/amd64\\nbuild: abc\\n' '" + pigVersionLine + "';;\ndocs) echo doc;;\nesac\n"
}

// R11: the packaged smoke steps run `pig --version`, which prints Pi's
// version by design, so only `pig version` can show that the archive holds the
// release's binary. Each smoke step that runs a built pig must compare its
// `pig:` line with the release VERSION.
func TestReleaseCandidateSmokeAssertsTheReleaseVersion(t *testing.T) {
	const version = "0.8.0"
	nativeStep := releaseWorkflowStep(t, "native-smoke", "Verify and run packaged candidate")
	for _, tc := range []struct {
		name      string
		pigLine   string
		wantError bool
	}{
		{"release version", "pig: " + version, false},
		{"another release", "pig: 0.7.0", true},
		{"development suffix", "pig: " + version + "-dev", true},
		{"longer number", "pig: " + version + "1", true},
		{"no pig line", "version " + version, true},
	} {
		t.Run("native "+tc.name, func(t *testing.T) {
			dir := t.TempDir()
			candidate := filepath.Join(dir, "candidate")
			stage := filepath.Join(dir, "stage", "pig-"+version+"-linux-amd64")
			writeExecutable(t, filepath.Join(stage, "pig"), fakePig(tc.pigLine))
			archive := filepath.Join(candidate, "pig-"+version+"-linux-amd64.tar.gz")
			if err := os.MkdirAll(candidate, 0o755); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command("tar", "-C", filepath.Dir(stage), "-czf", archive, filepath.Base(stage)).CombinedOutput(); err != nil {
				t.Fatalf("tar: %v\n%s", err, out)
			}
			data, err := os.ReadFile(archive)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(data)
			if err := os.WriteFile(filepath.Join(candidate, "SHA256SUMS"), []byte(hex.EncodeToString(sum[:])+"  "+filepath.Base(archive)+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			env := map[string]string{"VERSION": version, "GOOS": "linux", "GOARCH": "amd64", "FORMAT": "tar"}
			out, err := runWorkflowStep(t, nativeStep, dir, env, "")
			assertSmokeResult(t, out, err, tc.wantError, version)
		})
		t.Run("acp "+tc.name, func(t *testing.T) {
			acpStep := releaseWorkflowStep(t, "acp-native-smoke", "Assert the bundled pig reports the release version")
			dir := t.TempDir()
			writeExecutable(t, filepath.Join(dir, "extracted", "pig-acp-"+version+"-linux-amd64", "pig"), fakePig(tc.pigLine))
			env := map[string]string{"VERSION": version, "GOOS": "linux", "GOARCH": "amd64", "FORMAT": "tar"}
			out, err := runWorkflowStep(t, acpStep, dir, env, "")
			assertSmokeResult(t, out, err, tc.wantError, version)
		})
	}
}

func assertSmokeResult(t *testing.T, out string, err error, wantError bool, version string) {
	t.Helper()
	switch {
	case wantError && err == nil:
		t.Fatalf("smoke step accepted a pig that does not report %s:\n%s", version, out)
	case wantError && !strings.Contains(out, version):
		t.Fatalf("smoke failure does not name the wanted version %s:\n%s", version, out)
	case !wantError && err != nil:
		t.Fatalf("smoke step failed for a pig that reports %s: %v\n%s", version, err, out)
	}
}

// fakeStandard is a PiG Standard Binary whose invoked commands print what the
// real ones print: `version` names PiG's version, `verify --json` carries the
// baked Piglet release, and `status` names the active Piglet.
func fakeStandard(pigLine, pigletRelease, activePiglet string) string {
	verify := "{\\n  \"verified\": true,\\n  \"identity\": {\\n    \"pigVersion\": \"x\",\\n    \"platform\": \"linux/amd64\",\\n    \"build\": \"dev\""
	if pigletRelease != "" {
		verify += ",\\n    \"pigletRelease\": \"" + pigletRelease + "\""
	}
	verify += "\\n  }\\n}"
	return "#!/bin/sh\n" +
		"[ \"$PIG_OFFLINE\" = 1 ] || { echo 'not offline' >&2; exit 9; }\n" +
		"case \"$1\" in\n--help) echo usage;;\n--version) echo 1.5.0;;\n" +
		"version) printf '%s\\nupstream pi: 1.5.0+c23b9ec1\\nbuild: dev\\n' '" + pigLine + "';;\n" +
		"verify) printf '" + verify + "\\n';;\n" +
		"status) printf 'Pig status: healthy\\n  active Piglet: " + activePiglet + "\\n';;\nesac\n"
}

// The PiG Standard packaged smoke starts the archive's Binary offline and
// requires it to identify as PiG Standard at exactly the release version, in
// `pig version`, in its baked Piglet release and in its active Piglet.
func TestReleaseCandidateStandardSmokeAssertsStandardIdentity(t *testing.T) {
	const version = "0.8.0"
	step := releaseWorkflowStep(t, "standard-native-smoke", "Verify and run packaged candidate")
	for _, tc := range []struct {
		name      string
		pigLine   string
		release   string
		active    string
		wantError bool
	}{
		{"release version", "pig: " + version, version, "pig-standard (pig-standard)", false},
		{"stock pig version", "pig: 0.7.0", version, "pig-standard (pig-standard)", true},
		{"development version", "pig: " + version + "-dev", version, "pig-standard (pig-standard)", true},
		{"no baked Piglet release (Stock)", "pig: " + version, "", "none", true},
		{"another Piglet release", "pig: " + version, "0.7.0-dev", "pig-standard (pig-standard)", true},
		{"longer Piglet release", "pig: " + version, version + "1", "pig-standard (pig-standard)", true},
		{"another active Piglet", "pig: " + version, version, "pig-bolt (pig-bolt)", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			candidate := filepath.Join(dir, "candidate")
			name := "pig-standard-" + version + "-linux-amd64"
			stage := filepath.Join(dir, "stage", name)
			writeExecutable(t, filepath.Join(stage, "pig-standard"), fakeStandard(tc.pigLine, tc.release, tc.active))
			archive := filepath.Join(candidate, name+".tar.gz")
			if err := os.MkdirAll(candidate, 0o755); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command("tar", "-C", filepath.Dir(stage), "-czf", archive, filepath.Base(stage)).CombinedOutput(); err != nil {
				t.Fatalf("tar: %v\n%s", err, out)
			}
			data, err := os.ReadFile(archive)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(data)
			if err := os.WriteFile(filepath.Join(candidate, "SHA256SUMS"), []byte(hex.EncodeToString(sum[:])+"  "+filepath.Base(archive)+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			env := map[string]string{"VERSION": version, "GOOS": "linux", "GOARCH": "amd64", "FORMAT": "tar"}
			out, err := runWorkflowStep(t, step, dir, env, "")
			assertSmokeResult(t, out, err, tc.wantError, version)
		})
	}
}
