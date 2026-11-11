package cli

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// temporaryNpmPackageFixture installs npm:example 1.0.0 as a cached temporary package and returns the root a resolution must return.
func temporaryNpmPackageFixture(t *testing.T, f packageProcessFixture) string {
	t.Helper()
	ref, err := source.Parse("npm:example", source.Options{Bare: source.BareReject})
	if err != nil {
		t.Fatal(err)
	}
	root, err := packagemanager.TemporaryPackagePath(f.agent, "npm", "")
	if err != nil {
		t.Fatal(err)
	}
	installed := packagemanager.NpmPackagePath(root, ref)
	writePackageResource(t, filepath.Join(installed, "package.json"), `{"name":"example","version":"1.0.0"}`)
	writePackageResource(t, filepath.Join(installed, "extensions", "index.ts"), "export default function() {};")
	return installed
}

func processCallsOf(calls []packageProcessCall, command, subcommand string) []packageProcessCall {
	var matched []packageProcessCall
	for _, call := range calls {
		if call.Command == command && len(call.Args) > 0 && call.Args[0] == subcommand {
			matched = append(matched, call)
		}
	}
	return matched
}

// Pi cd6a8f7f (#11375) package-manager.test.ts "should refresh unpinned temporary npm sources when a newer version exists": the registry reports 1.1.0 for the cached 1.0.0,
// the refresh installs example@latest, and a failed install keeps loading the cached package.
func TestTemporaryNpmSourceRefreshesWhenANewerVersionExists(t *testing.T) {
	f := newPackageProcessFixture(t, `if(args[0]==='view')console.log('"1.1.0"');if(args[0]==='install'){console.error('network down');process.exit(1)}`)
	installed := temporaryNpmPackageFixture(t, f)

	resolved, err := resolveCLIExtensionSource(f.cwd, f.agent, f.settings, "npm:example", nil)
	if err != nil || resolved != installed {
		t.Fatalf("resolved = %q, %v; want the cached package %q", resolved, err, installed)
	}
	installs := processCallsOf(f.calls(t), "npm", "install")
	if len(installs) != 1 || !slices.Contains(installs[0].Args, "example@latest") {
		t.Fatalf("npm install calls = %+v, want one that installs example@latest", installs)
	}
}

// "should not refresh temporary npm sources when already up to date or pinned": the unpinned source asks the registry once, the exact version never does.
func TestTemporaryNpmSourceDoesNotRefreshWhenUpToDateOrPinned(t *testing.T) {
	f := newPackageProcessFixture(t, `if(args[0]==='view')console.log('"1.0.0"');`)
	installed := temporaryNpmPackageFixture(t, f)

	for _, input := range []string{"npm:example", "npm:example@1.0.0"} {
		resolved, err := resolveCLIExtensionSource(f.cwd, f.agent, f.settings, input, nil)
		if err != nil || resolved != installed {
			t.Fatalf("%s resolved = %q, %v; want %q", input, resolved, err, installed)
		}
	}
	calls := f.calls(t)
	if views := processCallsOf(calls, "npm", "view"); len(views) != 1 {
		t.Fatalf("npm view calls = %+v, want exactly one (the unpinned source)", views)
	}
	if installs := processCallsOf(calls, "npm", "install"); len(installs) != 0 {
		t.Fatalf("npm install calls = %+v, want none", installs)
	}
}

// npmHasAvailableUpdate returns false in offline mode: no registry lookup and no install.
func TestTemporaryNpmSourceDoesNotRefreshOffline(t *testing.T) {
	f := newPackageProcessFixture(t, `if(args[0]==='view')console.log('"1.1.0"');`)
	t.Setenv("PI_OFFLINE", "1")
	installed := temporaryNpmPackageFixture(t, f)

	resolved, err := resolveCLIExtensionSource(f.cwd, f.agent, f.settings, "npm:example", nil)
	if err != nil || resolved != installed {
		t.Fatalf("resolved = %q, %v; want %q", resolved, err, installed)
	}
	if calls := f.calls(t); len(calls) != 0 {
		t.Fatalf("offline resolution ran %+v, want nothing", calls)
	}
}

// temporaryGitCheckoutFixture creates the cached temporary checkout of a git source and returns the root a resolution must return.
func temporaryGitCheckoutFixture(t *testing.T, f packageProcessFixture, input string) string {
	t.Helper()
	ref, err := source.Parse(input, source.Options{Bare: source.BareReject})
	if err != nil {
		t.Fatal(err)
	}
	checkout, err := packagemanager.TemporaryGitCheckoutPath(f.agent, ref)
	if err != nil {
		t.Fatal(err)
	}
	writePackageResource(t, filepath.Join(checkout, "extensions", "index.ts"), "export default function() {};")
	return checkout
}

// Pi bec60c54 (#11375) package-manager.test.ts "should refresh temporary git sources with a branch ref": a ref names a moving branch, so the cached checkout is fetched and reset to it.
func TestTemporaryGitSourceWithABranchRefIsRefreshed(t *testing.T) {
	f := newPackageProcessFixture(t, `if(args[0]==='rev-parse')console.log('0123456789abcdef');`)
	const input = "git:github.com/example/repo@main"
	checkout := temporaryGitCheckoutFixture(t, f, input)
	var events []packagemanager.ProgressEvent
	resolved, err := resolveCLIExtensionSource(f.cwd, f.agent, f.settings, input, func(event packagemanager.ProgressEvent) { events = append(events, event) })
	if err != nil || resolved != checkout {
		t.Fatalf("resolved = %q, %v; want the cached checkout %q", resolved, err, checkout)
	}
	requirePackageProcessCall(t, f.calls(t), "git", []string{"fetch", "origin", "main"}, checkout)
	if len(events) == 0 || events[0].Type != "start" || events[0].Action != "pull" || events[0].Source != input {
		t.Fatalf("progress = %+v, want a pull of %s", events, input)
	}
}

// "should not refresh temporary git sources pinned to the checked-out commit": the abbreviated hash is the head's prefix, compared without case.
func TestTemporaryGitSourcePinnedToTheCheckedOutCommitIsNotRefreshed(t *testing.T) {
	f := newPackageProcessFixture(t, `if(args[0]==='rev-parse')console.log('ABC1234def5678901234567890123456789012345\n');`)
	const input = "git:github.com/example/repo@abc1234"
	checkout := temporaryGitCheckoutFixture(t, f, input)
	var events []packagemanager.ProgressEvent
	resolved, err := resolveCLIExtensionSource(f.cwd, f.agent, f.settings, input, func(event packagemanager.ProgressEvent) { events = append(events, event) })
	if err != nil || resolved != checkout {
		t.Fatalf("resolved = %q, %v; want %q", resolved, err, checkout)
	}
	calls := f.calls(t)
	if fetches := processCallsOf(calls, "git", "fetch"); len(fetches) != 0 || len(events) != 0 {
		t.Fatalf("git fetch calls = %+v, progress = %+v; want none for the commit that is checked out", fetches, events)
	}
	requirePackageProcessCall(t, calls, "git", []string{"rev-parse", "HEAD"}, checkout)
}

// A commit-hash ref that is not the checked-out head is fetched, and offline mode refreshes nothing.
func TestTemporaryGitSourcePinnedToAnotherCommitIsRefreshedUnlessOffline(t *testing.T) {
	f := newPackageProcessFixture(t, `if(args[0]==='rev-parse')console.log('fedcba9876543210');`)
	const input = "git:github.com/example/repo@abc1234"
	checkout := temporaryGitCheckoutFixture(t, f, input)
	if _, err := resolveCLIExtensionSource(f.cwd, f.agent, f.settings, input, nil); err != nil {
		t.Fatal(err)
	}
	requirePackageProcessCall(t, f.calls(t), "git", []string{"fetch", "origin", "abc1234"}, checkout)

	offline := newPackageProcessFixture(t, `if(args[0]==='rev-parse')console.log('fedcba9876543210');`)
	t.Setenv("PI_OFFLINE", "1")
	temporaryGitCheckoutFixture(t, offline, input)
	if _, err := resolveCLIExtensionSource(offline.cwd, offline.agent, offline.settings, input, nil); err != nil {
		t.Fatal(err)
	}
	if calls := offline.calls(t); len(calls) != 0 {
		t.Fatalf("offline resolution ran %+v, want nothing", calls)
	}
}
