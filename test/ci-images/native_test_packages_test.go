// SPDX-License-Identifier: MIT

package ciimages

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The clipboard tests of internal/nativeplatform, the Node-hosted native-clipboard-linux upstream test and the xcb oracle they compile need Xvfb, xclip and the
// libxcb headers on the runner. The nightly race job once installed its own shorter list, and ubuntu libs, rest and host failed with `"xclip": executable file not
// found` and `xcb/xcb.h: No such file`. Every job that runs those tests installs them through one script, so the list has one owner.
func TestEveryJobThatRunsNativeTestsInstallsThePackagesThroughTheSharedScript(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "automation/ci/install-native-test-packages.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, pkg := range []string{"tmux", "fd-find", "ripgrep", "build-essential", "xvfb", "xclip", "pkg-config", "libxcb1-dev"} {
		if !regexp.MustCompile(`\s` + regexp.QuoteMeta(pkg) + `(\s|$)`).MatchString(script) {
			t.Errorf("install-native-test-packages.sh does not install %s", pkg)
		}
	}
	if !strings.Contains(script, "install-apt-packages.sh") {
		t.Error("install-native-test-packages.sh must install through install-apt-packages.sh, which survives an unreachable mirror")
	}
	shared := "automation/ci/install-native-test-packages.sh"
	users := map[string]int{}
	for _, name := range []string{"ci.yml", "nightly.yml"} {
		for jobName, job := range readWorkflow(t, name).Jobs {
			for _, step := range job.Steps {
				if strings.Contains(step.Run, shared) {
					users[name+" "+jobName]++
				}
				for line := range strings.SplitSeq(step.Run, "\n") {
					if strings.Contains(line, "install-apt-packages.sh") && (strings.Contains(line, "xclip") || strings.Contains(line, "xvfb") || strings.Contains(line, "libxcb")) {
						t.Errorf("%s job %s installs the native test packages itself (%q); call %s", name, jobName, strings.TrimSpace(line), shared)
					}
				}
			}
		}
	}
	for _, want := range []string{"nightly.yml race", "ci.yml linux", "ci.yml node-runtime"} {
		if users[want] == 0 {
			t.Errorf("job %q does not run %s: %v", want, shared, users)
		}
	}
}
