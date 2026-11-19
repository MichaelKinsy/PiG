//go:build !pig_strip_node_extensions

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A startup that went on past a failed -e install runs the -e extension in a host process of the build. Pi's extensions end with its process; here the build's Host.Shutdown must stop that process and wait until it has exited, so none outlives the build (a Windows process still ending holds its files open, and the test's temporary directories could not be removed).
func TestCLIPackageSourceInstallFailureHostProcessEndsWithShutdown(t *testing.T) {
	f := newPackageProcessFixture(t, "process.exit(1);")
	pidFile := filepath.Join(t.TempDir(), "host-pid")
	quoted, err := json.Marshal(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	writeResourceLoaderFixture(t, filepath.Join(f.agent, "extensions", "probe.js"), `import {writeFileSync} from "node:fs"; export default function() { writeFileSync(`+string(quoted)+`, String(process.pid)); }`)
	build, err := newCLIGlobTestBuilder(t, f, "--print", "-e", "npm:eprobe-missing").buildResources(t.Context(), cliBuildInput{CWD: f.cwd})
	if err != nil {
		t.Fatalf("startup error = %v, want startup to go on after the failed npm command", err)
	}
	defer build.Services.Close()
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the extension factory did not run: %v", err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	if !extensionProcessAlive(pid) {
		t.Fatalf("the extension host process %d ended before the build was released, so the test does not show Shutdown waiting for it", pid)
	}
	build.Host.Shutdown("test")
	if extensionProcessAlive(pid) {
		t.Fatalf("the extension host process %d outlived Host.Shutdown", pid)
	}
}

// Pi 6dfb54678: resolveExtensionSources catches the failed install of a missing npm or git -e source, so the source contributes nothing and is reported as an extension warning, every
// later -e source is resolved too, and the extension factories still run.
func TestCLIPackageSourceInstallFailureWarnsAndStartupContinues(t *testing.T) {
	for _, tc := range []struct{ source, second, command string }{
		{"npm:eprobe-missing", "npm:eprobe-second", "npm"},
		{cliGitSource, "git:https://example.com/owner/second", "git"},
	} {
		t.Run(tc.command, func(t *testing.T) {
			f := newPackageProcessFixture(t, "process.exit(1);")
			marker := filepath.Join(t.TempDir(), "factory-ran")
			quoted, err := json.Marshal(marker)
			if err != nil {
				t.Fatal(err)
			}
			writeResourceLoaderFixture(t, filepath.Join(f.agent, "extensions", "probe.js"), `import {writeFileSync} from "node:fs"; export default function() { writeFileSync(`+string(quoted)+`, "ran"); }`)
			build, err := newCLIGlobTestBuilder(t, f, "--print", "-e", tc.source, "-e", tc.second).buildResources(t.Context(), cliBuildInput{CWD: f.cwd})
			if err != nil {
				t.Fatalf("startup error = %v, want startup to go on after the failed %s command", err, tc.command)
			}
			releaseCLIBuildAtCleanup(t, build)
			if _, statErr := os.Stat(marker); statErr != nil {
				t.Fatalf("the extension factory did not run: %v", statErr)
			}
			secondResolved := false
			for _, call := range f.calls(t) {
				secondResolved = secondResolved || strings.Contains(strings.Join(call.Args, " "), "second")
			}
			if !secondResolved {
				t.Fatal("the source after the failure was not resolved")
			}
			for _, source := range []string{tc.source, tc.second} {
				warned := false
				for _, diagnostic := range build.ExtensionDiagnostics {
					warned = warned || strings.Contains(diagnostic.Message, "Extension package \""+source+"\": failed to install (") && strings.Contains(diagnostic.Message, "Check the source and try again.")
				}
				if !warned {
					t.Fatalf("no install warning for %s in %+v", source, build.ExtensionDiagnostics)
				}
			}
		})
	}
}
