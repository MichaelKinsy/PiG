package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// packages/coding-agent/test/package-manager.test.ts:1202,1217 require a start event and an error event before installation rejects.
func TestPackageInstallFailureProgressUpstream(t *testing.T) {
	for _, tc := range []struct{ name, input, failure string }{
		{"should emit progress events on install attempt", "npm:nonexistent-package@1.0.0", "simulated npm install failure"},
		{"should recognize github URLs without git: prefix", "https://github.com/nonexistent/repo", "simulated git clone failure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A failing real child replaces Pi's rejected runCommand mock. The diagnostic remains on stderr; the rejection is the real command exit error.
			f := newPackageProcessFixture(t, `throw new Error('`+tc.failure+`');`)
			var events []packagemanager.ProgressEvent
			var installErr error
			stderr := captureStderr(t, func() {
				installErr = packagemanager.InstallPackageArtifacts(f.cwd, f.settings.AgentDir(), f.settings, codingagent.PackageSource{Source: tc.input}, false, func(event packagemanager.ProgressEvent) {
					if event.Type == "start" && len(f.calls(t)) != 0 {
						t.Error("start reported after child execution")
					}
					events = append(events, event)
				})
			})
			if installErr == nil || !strings.Contains(stderr, tc.failure) {
				t.Fatalf("installation error=%v stderr=%q", installErr, stderr)
			}
			want := []packagemanager.ProgressEvent{
				{Type: "start", Action: "install", Source: tc.input, Message: new("Installing " + tc.input + "...")},
				{Type: "error", Action: "install", Source: tc.input, Message: new(installErr.Error())},
			}
			if !reflect.DeepEqual(events, want) {
				t.Fatalf("progress=%+v, want %+v before return", events, want)
			}
			if strings.HasPrefix(tc.input, "https:") {
				requirePackageProcessCall(t, f.calls(t), "git", []string{"clone", tc.input, filepath.Join(f.agent, "git", "github.com", "nonexistent", "repo")}, "")
			}
		})
	}
}

// packages/coding-agent/test/package-manager.test.ts:670 resolves a local extension without emitting install progress.
func TestPackageLocalResolutionProgressUpstream(t *testing.T) {
	f := newPackageProcessFixture(t, `throw new Error('unexpected process');`)
	path := filepath.Join(f.cwd, "ext.ts")
	writePackageResource(t, path, "export default function() {}")
	var events []packagemanager.ProgressEvent
	resolved, err := resolveCLIExtensionSource(f.cwd, f.agent, f.settings, path, func(event packagemanager.ProgressEvent) {
		events = append(events, event)
	})
	if err != nil || resolved != path || len(events) != 0 || len(f.calls(t)) != 0 {
		t.Fatalf("resolved=%q error=%v events=%+v", resolved, err, events)
	}
}

// package-manager.ts:withProgress reports exactly one terminal event for either result; its caller waits for callbacks too.
func TestPackageInstallCompletionProgressUpstream(t *testing.T) {
	f := newPackageResourceFixture(t)
	path := filepath.Join(f.cwd, "ext.ts")
	writePackageResource(t, path, "export default function() {}")
	var events []packagemanager.ProgressEvent
	if err := packagemanager.InstallPackageArtifacts(f.cwd, f.settings.AgentDir(), f.settings, codingagent.PackageSource{Source: path}, false, func(event packagemanager.ProgressEvent) {
		events = append(events, event)
	}); err != nil {
		t.Fatal(err)
	}
	want := []packagemanager.ProgressEvent{
		{Type: "start", Action: "install", Source: path, Message: new("Installing " + path + "...")},
		{Type: "complete", Action: "install", Source: path},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events=%+v, want %+v", events, want)
	}
}

// Package refresh failures report an error before resolve returns the cached checkout, as refreshTemporaryGitSource does upstream.
func TestPackageTemporaryRefreshProgressUpstream(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "error"}[failure], func(t *testing.T) {
			body := `if(args[0]==='rev-parse')console.log(args[1]==='--abbrev-ref'?'origin/main':'current-head');`
			if failure {
				body += `if(args[0]==='fetch')throw new Error('simulated refresh failure');`
			}
			f := newPackageProcessFixture(t, body)
			const input = "git:github.com/example/repo"
			root, err := packagemanager.TemporaryPackagePath(f.agent, "git-github.com", "example/repo")
			if err != nil {
				t.Fatal(err)
			}
			writePackageResource(t, filepath.Join(root, "extensions", "index.ts"), "export default function() {};")
			var events []packagemanager.ProgressEvent
			var resolved string
			stderr := captureStderr(t, func() {
				resolved, err = resolveCLIExtensionSource(f.cwd, f.agent, f.settings, input, func(event packagemanager.ProgressEvent) {
					events = append(events, event)
				})
			})
			if err != nil || resolved != root {
				t.Fatalf("cached resolution=%q error=%v", resolved, err)
			}
			want := []packagemanager.ProgressEvent{
				{Type: "start", Action: "pull", Source: input, Message: new("Refreshing " + input + "...")},
				{Type: "complete", Action: "pull", Source: input},
			}
			if failure {
				want[1].Type = "error"
				want[1].Message = new("git fetch --prune --no-tags origin +refs/heads/main:refs/remotes/origin/main failed with code 1")
				if !strings.Contains(stderr, "simulated refresh failure") {
					t.Fatalf("stderr=%q", stderr)
				}
			}
			if !reflect.DeepEqual(events, want) {
				t.Fatalf("events=%+v, want %+v", events, want)
			}
		})
	}
}

func TestPackageUpdateFailureProgressUpstream(t *testing.T) {
	f := newPackageProcessFixture(t, `throw new Error('simulated npm update failure');`)
	const input = "npm:example"
	if err := f.settings.SetPackages([]codingagent.PackageSource{{Source: input}}); err != nil {
		t.Fatal(err)
	}
	var events []packagemanager.ProgressEvent
	var updateErr error
	stderr := captureStderr(t, func() {
		updateErr = packageManagerFor(f.cwd, f.settings, func(event packagemanager.ProgressEvent) {
			events = append(events, event)
		}).Update(input)
	})
	if updateErr == nil || !strings.Contains(stderr, "simulated npm update failure") {
		t.Fatalf("error=%v stderr=%q", updateErr, stderr)
	}
	want := []packagemanager.ProgressEvent{
		{Type: "start", Action: "update", Source: input, Message: new("Updating " + input + "...")},
		{Type: "error", Action: "update", Source: input, Message: new(updateErr.Error())},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events=%+v, want %+v", events, want)
	}
}

func BenchmarkPackageLocalInstallProgress(b *testing.B) {
	cwd, agent := b.TempDir(), b.TempDir()
	path := filepath.Join(cwd, "ext.ts")
	if err := os.WriteFile(path, []byte("export default function() {}"), 0o600); err != nil {
		b.Fatal(err)
	}
	settings := codingagent.NewSettingsManager(cwd, agent)
	pkg := codingagent.PackageSource{Source: path}
	callback := func(event packagemanager.ProgressEvent) {
		if event.Source != path || event.Action != "install" {
			b.Fatal("incorrect progress")
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		if err := packagemanager.InstallPackageArtifacts(cwd, settings.AgentDir(), settings, pkg, false, callback); err != nil {
			b.Fatal(err)
		}
	}
}

// packages/coding-agent/test/package-manager.test.ts:1266 passes an already parsed traversal source to each production Git install-path boundary.
func TestPackageGitInstallRootTraversalUpstream(t *testing.T) {
	f := newPackageResourceFixture(t)
	ref := source.Ref{Kind: source.KindGit, GitRepo: "git@evil.example:../../victim/repo", GitHost: "evil.example", GitPath: "../../victim/repo"}
	for _, scope := range []string{"user", "project", "temporary"} {
		t.Run(scope, func(t *testing.T) {
			var err error
			if scope == "temporary" {
				_, err = packagemanager.TemporaryGitCheckoutPath(f.agent, ref)
			} else {
				root := filepath.Join(f.agent, "git")
				if scope == "project" {
					root = filepath.Join(codingagent.ProjectConfigDir(f.cwd), "git")
				}
				_, err = packagemanager.GitCheckoutRelative(runtime.GOOS, root, ref)
			}
			if err == nil || !strings.Contains(err.Error(), "outside package install root") {
				t.Fatalf("error=%v, want outside package install root", err)
			}
		})
	}
}
