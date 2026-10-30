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
