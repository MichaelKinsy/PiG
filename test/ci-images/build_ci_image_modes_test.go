// SPDX-License-Identifier: MIT

package ciimages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildScriptFixture runs build-ci-image.sh against a docker that logs every invocation and, for a build with --output, writes the
// resolved lock the Dockerfile's apk-resolved stage would export.
func buildScriptFixture(t *testing.T, env ...string) (root, log string, output []byte, err error) {
	t.Helper()
	root = t.TempDir()
	copyCIFixture(t, root, "automation/ci/build-ci-image.sh")
	copyCIFixture(t, root, "automation/images/ci-go/metadata.env")
	for path, content := range map[string]string{
		".go-version":                           "9.8.7\n",
		"automation/images/ci-go/packages.lock": "git=1\n",
		"bin/go":                                "#!/bin/sh\nprintf 'go9.8.7\\n'\n",
		"bin/docker": `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$DOCKER_LOG"
case "$1" in
  build)
    while [ "$#" -gt 0 ]; do
      if [ "$1" = --output ]; then dest=${2#type=local,dest=}; printf 'git=2\nlibcurl=8.23.0-r0\n' > "$dest/packages.lock"; fi
      shift
    done ;;
  run)
    shift
    while [[ "$1" != bash ]]; do shift; done
    exec "$@" ;;
  *) exit 99 ;;
esac
`,
	} {
		writeCIFixture(t, root, path, content)
	}
	log = filepath.Join(root, "docker.log")
	cmd := mockedBash(t, filepath.Join(root, "bin"), filepath.Join(root, "automation/ci/build-ci-image.sh"), "go")
	cmd.Env = append(os.Environ(), "SOURCE_REVISION="+strings.Repeat("a", 40), "CI_BASE_DIR=", "DOCKER_LOG="+log)
	cmd.Env = append(cmd.Env, env...)
	output, err = cmd.CombinedOutput()
	return root, log, output, err
}

// The refresh resolves the lock from the index and builds nothing; the verify job checks a published image without building it.
func TestBuildScriptResolvesTheLockWithoutBuildingTheImage(t *testing.T) {
	root, log, output, err := buildScriptFixture(t, "RESOLVE_APK_LOCK=1")
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	got, readErr := os.ReadFile(filepath.Join(root, "automation/images/ci-go/packages.lock"))
	if readErr != nil || string(got) != "git=2\nlibcurl=8.23.0-r0\n" {
		t.Errorf("the lock was not replaced by the resolved one: %q %v", got, readErr)
	}
	logged, _ := os.ReadFile(log)
	if !strings.Contains(string(logged), "--target apk-resolved") || strings.Contains(string(logged), " -t ") || strings.Contains(string(logged), "run ") {
		t.Errorf("resolving must build only the apk-resolved stage and tag and run nothing:\n%s", logged)
	}
}

func TestBuildScriptChecksAnExistingImageWithoutBuilding(t *testing.T) {
	ref := "ghcr.io/michaelkinsy/pig-ci-go@sha256:" + strings.Repeat("e", 64)
	_, log, output, err := buildScriptFixture(t, "VERIFY_IMAGE_REF="+ref)
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	logged, _ := os.ReadFile(log)
	if strings.Contains(string(logged), "build ") || !strings.Contains(string(logged), "run --rm --platform linux/amd64 "+ref) {
		t.Errorf("an existing image must be checked, not built:\n%s", logged)
	}
	if !strings.HasSuffix(strings.TrimSpace(string(output)), ref) {
		t.Errorf("the script reports %q, want the checked reference", output)
	}
}

func TestBuildScriptRefusesToResolveAndVerifyTogether(t *testing.T) {
	_, _, output, err := buildScriptFixture(t, "RESOLVE_APK_LOCK=1", "VERIFY_IMAGE_REF=x")
	if err == nil || !strings.Contains(string(output), "exclusive") {
		t.Fatalf("accepted both modes: %v\n%s", err, output)
	}
}
