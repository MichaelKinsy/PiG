// SPDX-License-Identifier: MIT

package ciimages

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

var digestPinned = regexp.MustCompile(`@sha256:[0-9a-f]{64}$`)

// Every external image a CI image build, scan or SBOM starts from is pinned by digest, so a moving tag cannot change what a pull
// request verifies. The apk locks pin the packages above them, and test/ci-images proves the workflow reads no index (below).
func TestCIImageBasesArePinnedByDigest(t *testing.T) {
	root := repoRoot(t)
	argument := regexp.MustCompile(`(?m)^ARG ([A-Z_]*(?:IMAGE|BASE|BUILDER))=(\S+)$`)
	from := regexp.MustCompile(`(?m)^FROM (\S+)`)
	for _, image := range []string{"ci-go", "ci-parity"} {
		data, err := os.ReadFile(filepath.Join(root, "automation/images", image, "Dockerfile"))
		if err != nil {
			t.Fatal(err)
		}
		dockerfile := string(data)
		for _, match := range argument.FindAllStringSubmatch(dockerfile, -1) {
			if !digestPinned.MatchString(match[2]) {
				t.Errorf("%s: ARG %s = %s is not pinned by digest", image, match[1], match[2])
			}
		}
		stages := map[string]bool{"scratch": true}
		for _, match := range regexp.MustCompile(`(?m)^FROM \S+ AS (\S+)`).FindAllStringSubmatch(dockerfile, -1) {
			stages[match[1]] = true
		}
		for _, match := range from.FindAllStringSubmatch(dockerfile, -1) {
			ref := match[1]
			// A variable names a digest-pinned ARG above; a stage name is an earlier stage of this file.
			if strings.HasPrefix(ref, "${") || stages[ref] {
				continue
			}
			t.Errorf("%s: FROM %s is neither a digest-pinned ARG nor an earlier stage", image, ref)
		}
	}
	seed, err := os.ReadFile(filepath.Join(root, "automation/ci/seed-ci-image-bases.sh"))
	if err != nil {
		t.Fatal(err)
	}
	loads := regexp.MustCompile(`(?m)^\s+'([^']+)' \\\n`).FindAllStringSubmatch(string(seed), -1)
	if len(loads) < 3 {
		t.Fatalf("found %d images loaded by seed-ci-image-bases.sh, want buildkit, syft and grype", len(loads))
	}
	for _, match := range loads {
		if strings.Contains(match[1], "@sha256:") && digestPinned.MatchString(match[1]) {
			continue
		}
		if strings.HasPrefix(match[1], "pig-ci-base-") {
			continue // the local tag a pinned pull is loaded as
		}
		t.Errorf("seed-ci-image-bases.sh loads %s, which is not pinned by digest", match[1])
	}
}

type imageVerifyWorkflow struct {
	On   map[string]any
	Jobs map[string]struct {
		If    string
		Needs any
		Steps []struct {
			Name string
			If   string
			Run  string
			Env  map[string]string
			Uses string
		}
	}
}

func loadImageVerifyWorkflow(t *testing.T) imageVerifyWorkflow {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github/workflows/ci-images.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow imageVerifyWorkflow
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	return workflow
}

// A pull request or a push to main may read neither a package index nor a vulnerability database: the verify job checks the
// published image its tree pins by digest, and only the refresh (schedule and dispatch) resolves locks, builds and scans.
func TestImagePullRequestChecksNeverReadAMovingIndex(t *testing.T) {
	workflow := loadImageVerifyWorkflow(t)
	for _, event := range []string{"pull_request", "push"} {
		if _, ok := workflow.On[event]; !ok {
			t.Fatalf("ci-images.yml no longer runs on %s", event)
		}
	}
	verify, ok := workflow.Jobs["verify"]
	if !ok {
		t.Fatal("ci-images.yml has no verify job")
	}
	if verify.If != "github.event_name == 'pull_request' || github.event_name == 'push'" {
		t.Errorf("verify runs on %q, want exactly the pull request and push events", verify.If)
	}
	for _, step := range verify.Steps {
		for _, forbidden := range []string{"apk ", "RESOLVE_APK_LOCK", "grype", "GRYPE_IMAGE", "seed-ci-image-bases.sh \"$IMAGE\"", "docker build ", "docker buildx build \\"} {
			if strings.Contains(step.Run, forbidden) {
				t.Errorf("verify step %q contains %q: pull request CI must not read a package index or vulnerability database", step.Name, forbidden)
			}
		}
		if strings.Contains(step.Run, "build-ci-image.sh") && step.Env["VERIFY_IMAGE_REF"] == "" {
			t.Errorf("verify step %q builds an image; it may only check the published one (VERIFY_IMAGE_REF)", step.Name)
		}
	}
	build := workflow.Jobs["build"]
	if build.If != "github.event_name == 'schedule' || github.event_name == 'workflow_dispatch'" {
		t.Errorf("build (the refresh, which reads the index and the vulnerability database) runs on %q, want schedule and dispatch only", build.If)
	}
	if _, ok := workflow.On["schedule"]; !ok {
		t.Error("ci-images.yml has no weekly schedule, so nothing refreshes the locks")
	}
	resolves := false
	for _, step := range build.Steps {
		if step.Env["RESOLVE_APK_LOCK"] == "1" {
			resolves = true
		}
	}
	if !resolves {
		t.Error("the refresh does not resolve the apk locks from the current index")
	}
	for _, job := range []string{"propose-lock", "propose-pin"} {
		if _, ok := workflow.Jobs[job]; !ok {
			t.Errorf("ci-images.yml has no %s job", job)
		}
	}
}

func TestCIImageInputsFollowEveryInput(t *testing.T) {
	root := t.TempDir()
	copyCIFixture(t, root, "automation/ci/ci-image-inputs.sh")
	files := map[string][]string{
		"go": {".go-version", "automation/images/ci-go/Dockerfile", "automation/images/ci-go/metadata.env", "automation/images/ci-go/packages.lock"},
		"parity": {
			".go-version", ".node-version", ".upstream/pin", "automation/images/ci-parity/Dockerfile", "automation/images/ci-parity/metadata.env",
			"automation/images/ci-parity/packages.lock", "automation/images/ci-parity/package.json", "automation/images/ci-parity/package-lock.json",
			"automation/images/npm-runtime/package.json", "automation/images/npm-runtime/package-lock.json",
		},
	}
	seen := map[string]bool{}
	for _, list := range files {
		for _, file := range list {
			if !seen[file] {
				writeCIFixture(t, root, file, "original "+file+"\n")
				seen[file] = true
			}
		}
	}
	writeCIFixture(t, root, ".github/workflows/ci.yml", "env:\n  RUST_VERSION: 1.2.3\n")
	run := func(image string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), testenv.Bash(t), filepath.Join(root, "automation/ci/ci-image-inputs.sh"), image)
		output, err := cmd.Output()
		if err != nil {
			t.Fatalf("ci-image-inputs.sh %s: %v", image, err)
		}
		return strings.TrimSpace(string(output))
	}
	base := map[string]string{"go": run("go"), "parity": run("parity")}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(base["go"]) || base["go"] == base["parity"] {
		t.Fatalf("inputs digests %v", base)
	}
	change := func(file, content string, wantChanged map[string]bool) {
		t.Helper()
		path := filepath.Join(root, file)
		old, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := os.WriteFile(path, old, 0o600); err != nil {
				t.Fatal(err)
			}
		}()
		for _, image := range []string{"go", "parity"} {
			if changed := run(image) != base[image]; changed != wantChanged[image] {
				t.Errorf("editing %s: %s digest changed = %v, want %v", file, image, changed, wantChanged[image])
			}
		}
	}
	for _, file := range files["go"] {
		change(file, "edited\n", map[string]bool{"go": true, "parity": contains(files["parity"], file)})
	}
	for _, file := range files["parity"] {
		change(file, "edited\n", map[string]bool{"go": contains(files["go"], file), "parity": true})
	}
	// The Rust pin is read by the parity checks only.
	change(".github/workflows/ci.yml", "env:\n  RUST_VERSION: 9.9.9\n", map[string]bool{"go": false, "parity": true})
	change("automation/images/ci-go/packages.lock", "original automation/images/ci-go/packages.lock\n", map[string]bool{})
}

func contains(list []string, item string) bool {
	return slices.Contains(list, item)
}

// The proposal jobs run on ubuntu-24.04 (ci-images.yml propose-lock, propose-pin) and call propose-ci-image-change.py by its shebang. The script runs ci-image-inputs.sh by its shebang too, and finds the gh CLI by name; Windows can execute neither (WinError 193, and the runner's own gh.exe shadows the extensionless fake), so the tests that run the whole tool against a fake gh have no Windows run to prove. The tests that reject malformed input stop before either call and keep running there.
func requireShebangRunner(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the proposal tool runs on the Ubuntu runners: it starts scripts by shebang and a fake gh, which Windows cannot execute")
	}
}

// proposalRun runs propose-ci-image-change.py with a fake gh that logs every call and its standard input.
func proposalRun(t *testing.T, failPR bool, args ...string) (string, string, error) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "gh.log")
	fake := "#!/bin/sh\n{ printf 'ARGS:%s\\n' \"$*\"; if [ \"$1 $2\" = 'api graphql' ]; then cat; printf '\\n'; fi; } >> \"$GH_LOG\"\n" +
		"case \"$1 $2\" in\n  'api graphql') printf '{\"data\":{\"createCommitOnBranch\":{\"commit\":{\"oid\":\"" + strings.Repeat("c", 40) + "\"}}}}' ;;\n" +
		"  'pr create') [ \"$FAKE_PR_FAILS\" != 1 ] || { echo 'GraphQL: GitHub Actions is not permitted to create or approve pull requests' >&2; exit 1; } ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), hostPython(), append([]string{repoScript(t, "automation/ci/propose-ci-image-change.py")}, args...)...)
	fail := "0"
	if failPR {
		fail = "1"
	}
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "GH_LOG="+log, "FAKE_PR_FAILS="+fail,
		"REPO=MichaelKinsy/PiG", "BASE_SHA="+strings.Repeat("b", 40), "RUN_ID=77")
	output, err := cmd.CombinedOutput()
	logged, _ := os.ReadFile(log)
	return string(output), string(logged), err
}

func TestLockProposalIsOneSignedCommitOnANewBranch(t *testing.T) {
	requireShebangRunner(t)
	root := repoRoot(t)
	committed, err := os.ReadFile(filepath.Join(root, "automation/images/ci-go/packages.lock"))
	if err != nil {
		t.Fatal(err)
	}
	evidence := t.TempDir()
	resolved := strings.Replace(string(committed), "libcurl=", "libcurl=9.", 1)
	writeCIFixture(t, evidence, "pig-ci-go-evidence/packages.lock", resolved)
	// The parity image's evidence holds the committed lock: nothing to propose for it.
	parity, err := os.ReadFile(filepath.Join(root, "automation/images/ci-parity/packages.lock"))
	if err != nil {
		t.Fatal(err)
	}
	writeCIFixture(t, evidence, "pig-ci-parity-evidence/packages.lock", string(parity))
	output, log, err := proposalRun(t, false, "lock", "--evidence", evidence)
	if err != nil {
		t.Fatalf("%v\n%s\n%s", err, output, log)
	}
	if !strings.Contains(log, "ARGS:api repos/MichaelKinsy/PiG/git/refs -f ref=refs/heads/ci-images/lock-") || !strings.Contains(log, "sha="+strings.Repeat("b", 40)) {
		t.Errorf("the branch was not created from the base commit:\n%s", log)
	}
	_, body, _ := strings.Cut(log, "ARGS:api graphql --input -\n")
	jsonBody, _, _ := strings.Cut(body, "\nARGS:")
	var request struct {
		Variables struct {
			Input struct {
				ExpectedHeadOid string
				Message         struct{ Body string }
				FileChanges     struct {
					Additions []struct{ Path, Contents string }
				}
			}
		}
	}
	if err := json.Unmarshal([]byte(jsonBody), &request); err != nil {
		t.Fatalf("graphql request: %v\n%s", err, jsonBody)
	}
	input := request.Variables.Input
	if input.ExpectedHeadOid != strings.Repeat("b", 40) || len(input.FileChanges.Additions) != 1 || input.FileChanges.Additions[0].Path != "automation/images/ci-go/packages.lock" {
		t.Errorf("unexpected commit request: %+v", input)
	}
	if !strings.Contains(input.Message.Body, "Signed-off-by: github-actions[bot] <41898282+github-actions[bot]@users.noreply.github.com>") {
		t.Errorf("the commit is not signed off by the bot:\n%s", input.Message.Body)
	}
	if !strings.Contains(log, "ARGS:pr create") {
		t.Errorf("no pull request was attempted:\n%s", log)
	}
	// The setting that lets Actions open pull requests is off by default: the branch stays and the log names it.
	output, _, err = proposalRun(t, true, "lock", "--evidence", evidence)
	if err != nil || !strings.Contains(output, "::warning::branch ci-images/lock-") || !strings.Contains(output, "compare/main...ci-images/lock-") {
		t.Errorf("a refused pull request must leave the branch and say how to open it: %v\n%s", err, output)
	}
}

func TestLockProposalDoesNothingWhenLocksAreCurrent(t *testing.T) {
	root := repoRoot(t)
	evidence := t.TempDir()
	for image, directory := range map[string]string{"go": "ci-go", "parity": "ci-parity"} {
		data, err := os.ReadFile(filepath.Join(root, "automation/images", directory, "packages.lock"))
		if err != nil {
			t.Fatal(err)
		}
		writeCIFixture(t, evidence, "pig-ci-"+image+"-evidence/packages.lock", string(data))
	}
	output, log, err := proposalRun(t, false, "lock", "--evidence", evidence)
	if err != nil || log != "" || !strings.Contains(output, "locks are current") {
		t.Fatalf("current locks produced a proposal: %v\n%s\n%s", err, output, log)
	}
}

func TestProposalsRefuseMalformedInput(t *testing.T) {
	evidence := t.TempDir()
	writeCIFixture(t, evidence, "pig-ci-go-evidence/packages.lock", "not a lock line\n")
	if _, log, err := proposalRun(t, false, "lock", "--evidence", evidence); err == nil || log != "" {
		t.Errorf("a malformed lock was proposed: %v\n%s", err, log)
	}
	publication := t.TempDir()
	writeCIFixture(t, publication, "pig-ci-go-publication/image-ref.txt", "ghcr.io/michaelkinsy/pig-ci-go:go1.27.3-tools1-abc\n")
	if _, log, err := proposalRun(t, false, "pin", "--publication", publication); err == nil || log != "" {
		t.Errorf("a pin without a digest was proposed: %v\n%s", err, log)
	}
}

func TestPinProposalRecordsDigestAndInputs(t *testing.T) {
	requireShebangRunner(t)
	publication := t.TempDir()
	ref := "ghcr.io/michaelkinsy/pig-ci-go@sha256:" + strings.Repeat("d", 64)
	writeCIFixture(t, publication, "pig-ci-go-publication/image-ref.txt", ref+"\n")
	output, log, err := proposalRun(t, false, "pin", "--publication", publication)
	if err != nil {
		t.Fatalf("%v\n%s\n%s", err, output, log)
	}
	_, body, _ := strings.Cut(log, "ARGS:api graphql --input -\n")
	jsonBody, _, _ := strings.Cut(body, "\nARGS:")
	var request struct {
		Variables struct {
			Input struct {
				FileChanges struct {
					Additions []struct{ Path, Contents string }
				}
			}
		}
	}
	if err := json.Unmarshal([]byte(jsonBody), &request); err != nil {
		t.Fatalf("graphql request: %v\n%s", err, jsonBody)
	}
	additions := request.Variables.Input.FileChanges.Additions
	if !strings.Contains(log, "ci-images/pin-") || len(additions) != 1 || additions[0].Path != "automation/images/ci-go/published.env" {
		t.Fatalf("unexpected pin commit request:\n%s", log)
	}
	content, err := base64.StdEncoding.DecodeString(additions[0].Contents)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "IMAGE_REF="+ref+"\n") || !regexp.MustCompile(`INPUTS_SHA256=[0-9a-f]{64}\n`).Match(content) {
		t.Errorf("published.env does not record the digest and the inputs:\n%s", content)
	}
}
