package cli

import (
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	sourceref "github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// Ports packages/coding-agent/test/package-manager.test.ts:1266 ("should reject
// paths outside git install roots"). The upstream test builds the source object
// directly because the parser never produces it (the parser refusal is in
// coding/source/ref_test.go); every scope resolves its checkout through
// gitCheckoutRelative, so each scope root refuses it with upstream's
// resolveManagedPath message. Pi's temporary scope contains under git-<host> with a hash segment; these escaping inputs are refused by both roots and are unreachable through the source parser.
func TestUpstreamGitInstallPathRejectsPathsOutsideRoots(t *testing.T) {
	traversal := sourceref.Ref{Kind: sourceref.KindGit, GitRepo: "git@evil.example:../../victim/repo", GitHost: "evil.example", GitPath: "../../victim/repo"}
	cwd, agentDir := t.TempDir(), t.TempDir()
	roots := map[string]string{
		"user":      codingagent.GitInstallRoot(cwd, agentDir, false),
		"project":   codingagent.GitInstallRoot(cwd, agentDir, true),
		"temporary": filepath.Join(agentDir, "tmp", "extensions"),
	}
	for scope, root := range roots {
		t.Run(scope, func(t *testing.T) {
			got, err := packagemanager.GitCheckoutRelative(runtime.GOOS, root, traversal)
			if err == nil || !strings.Contains(err.Error(), "outside package install root") {
				t.Fatalf("gitCheckoutRelative = %q, %v; want the outside-install-root refusal", got, err)
			}
		})
	}
}

// Ports packages/coding-agent/test/package-manager.test.ts:1202 and :1217.
// Pi emits start/install and error events before returning the failed install.
// The second case also pins the git clone argv for a github.com URL without a git: prefix.
// Pi: packages/coding-agent/src/core/package-manager.ts:95 (ProgressEvent.message).
func TestUpstreamInstallProgressAndGitURLWithoutPrefix(t *testing.T) {
	for _, tc := range []struct {
		name, source, tool string
		wantCall           []string
	}{
		// package-manager.test.ts:1202
		{"should emit progress events on install attempt", "npm:nonexistent-package@1.0.0", "npm", nil},
		// package-manager.test.ts:1217
		{"should recognize github URLs without git: prefix", "https://github.com/nonexistent/repo", "git", []string{"clone", "https://github.com/nonexistent/repo"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPackageProcessFixture(t, `if(command===`+"'"+tc.tool+"'"+`)throw new Error('simulated install failure');`)
			var events []packagemanager.ProgressEvent
			var err error
			stderr := captureStderr(t, func() {
				err = packagemanager.InstallPackageArtifacts(f.cwd, f.settings.AgentDir(), f.settings, codingagent.PackageSource{Source: tc.source}, false, func(event packagemanager.ProgressEvent) { events = append(events, event) })
			})
			if err == nil || !strings.Contains(stderr, "simulated install failure") {
				t.Fatalf("error = %v, stderr = %q; want the simulated failure propagated", err, stderr)
			}
			wantEvents := []packagemanager.ProgressEvent{
				{Type: "start", Action: "install", Source: tc.source, Message: new("Installing " + tc.source + "...")},
				{Type: "error", Action: "install", Source: tc.source, Message: new(err.Error())},
			}
			if !reflect.DeepEqual(events, wantEvents) {
				t.Fatalf("progress = %+v; want start then error and no success: %+v", events, wantEvents)
			}
			if tc.wantCall != nil {
				calls := f.calls(t)
				if i := slices.IndexFunc(calls, func(c packageProcessCall) bool { return c.Command == "git" }); i < 0 || !slices.Equal(calls[i].Args[:2], tc.wantCall) || len(calls[i].Args) != 3 {
					t.Fatalf("git calls = %+v; want clone %s <checkout>", calls, tc.source)
				}
			}
		})
	}
}

// Ports packages/coding-agent/test/package-manager.test.ts:670 ("should emit
// progress events"): resolving a local extension path starts no install, so
// nothing runs and no progress can be reported.
func TestUpstreamLocalExtensionResolutionStartsNoInstall(t *testing.T) {
	f := newPackageProcessFixture(t, `throw new Error('a local path must not start a process');`)
	extension := filepath.Join(f.cwd, "ext.ts")
	writePackageResource(t, extension, "export default function() {}")
	var events []packagemanager.ProgressEvent
	root, err := resolveCLIExtensionSource(f.cwd, f.agent, f.settings, extension, func(event packagemanager.ProgressEvent) { events = append(events, event) })
	if err != nil || root != extension {
		t.Fatalf("resolveCLIExtensionSource = %q, %v", root, err)
	}
	if len(events) != 0 {
		t.Fatalf("unexpected progress: %+v", events)
	}
	if calls := f.calls(t); len(calls) != 0 {
		t.Fatalf("processes started: %+v", calls)
	}
}
