package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

type unversionedLister struct{}

func (unversionedLister) Commands() []extension.ResolvedCommand { return nil }

// refusingLister reports that it has no version.
type refusingLister struct{ unversionedLister }

func (refusingLister) CommandsVersion() (uint64, bool) { return 0, false }

// A state is a published catalog and the version of the runner's commands: the version holds across readings of one state and changes with either part, and a lister without a version is always a new state.
func TestCommandCatalogVersionNumbersStates(t *testing.T) {
	ext := &extension.Extension{Name: "ext"}
	ext.InitializeDeclarationRegistry()
	runner := inproc.NewRunner([]extension.Extension{*ext}, t.TempDir())
	sourceA, sourceB := new(int), new(int)
	var version CommandCatalogVersion
	first := version.Version(sourceA, runner)
	if again := version.Version(sourceA, runner); again != first {
		t.Fatalf("an unchanged state was renumbered: %d then %d", first, again)
	}
	other := version.Version(sourceB, runner)
	if other == first {
		t.Fatal("a new published catalog kept the number")
	}
	back := version.Version(sourceA, runner)
	if back == first || back == other {
		t.Fatalf("a number repeated: %d, %d, %d", first, other, back)
	}
	// A reloaded runner with the same number of command writes is another state.
	sibling := &extension.Extension{Name: "sibling"}
	sibling.InitializeDeclarationRegistry()
	reloaded := inproc.NewRunner([]extension.Extension{*sibling}, t.TempDir())
	swapped := version.Version(sourceA, reloaded)
	if swapped == first || swapped == other || swapped == back {
		t.Fatalf("a new runner kept a number: %d", swapped)
	}
	if v := version.Version(sourceA, runner); v == first || v == other || v == back || v == swapped {
		t.Fatalf("the old runner came back with a number it had: %d", v)
	}
	ext.SetRegisteredCommand("late", extension.RegisteredCommand{Description: "late"})
	afterRegistration := version.Version(sourceA, runner)
	if afterRegistration == other || afterRegistration == first {
		t.Fatalf("a registration did not renumber: %d", afterRegistration)
	}
	for _, lister := range []ExtensionCommandLister{unversionedLister{}, refusingLister{}} {
		a, b := version.Version(sourceA, lister), version.Version(sourceA, lister)
		if a == b {
			t.Fatalf("%T repeated number %d", lister, a)
		}
	}
	if a, b := version.Version(sourceA, nil), version.Version(sourceA, nil); a != b {
		t.Fatalf("a catalog without a runner was renumbered: %d, %d", a, b)
	}
}

// The command catalog an extension holds follows its runner: a command registered between two state pushes, a late one included, reaches the next push, and each push equals a direct encoding of the catalog.
func TestExtensionStateCommandsFollowTheRunnerAcrossRegistrations(t *testing.T) {
	ext, peer := &extension.Extension{Name: "ext"}, &extension.Extension{Name: "peer"}
	ext.InitializeDeclarationRegistry()
	peer.InitializeDeclarationRegistry()
	runner := inproc.NewRunner([]extension.Extension{*ext, *peer}, t.TempDir())
	catalog := SlashCommandCatalog{Runner: runner, CWD: t.TempDir()}
	var version CommandCatalogVersion
	bridge := subprocess.NewUIBridge(func() {})
	var builds int
	bridge.SetHostAction("getCommands", func() []subprocess.CommandInfo { builds++; return catalog.SubprocessCommands() })
	bridge.SetHostAction("getCommandsVersion", func() uint64 { return version.Version(&catalog, catalog.Runner) })
	push := func(step string) {
		t.Helper()
		want, err := json.Marshal(catalog.SubprocessCommands())
		if err != nil {
			t.Fatal(err)
		}
		if got := string(bridge.Snapshot(nil, 0, false).Commands); got != string(want) {
			t.Fatalf("%s:\n got %s\nwant %s", step, got, want)
		}
	}
	push("no commands")
	push("again")
	if builds != 1 {
		t.Fatalf("an unchanged catalog was built %d times over two pushes, want 1", builds)
	}
	ext.SetRegisteredCommand("alpha", extension.RegisteredCommand{Description: "a"})
	push("a command registered after the first push")
	peer.SetRegisteredCommand("alpha", extension.RegisteredCommand{Description: "collides"})
	push("a colliding command from another extension")
	ext.SetRegisteredCommand("alpha", extension.RegisteredCommand{Description: "replaced"})
	push("a replaced command")
	before := builds
	push("unchanged")
	if builds != before {
		t.Fatal("an unchanged catalog was built again")
	}
}
