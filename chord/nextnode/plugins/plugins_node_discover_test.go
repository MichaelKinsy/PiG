package plugins

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// Not upstream's: discoverNode's order of precedence (plugins_node_discover.go), each place on a hermetic tree of fake `node`
// executables (writeFakeNode), never the Node of the machine.

// isolateNodeSearch leaves discoverNode nothing to find but what a test places: PIG_NODE and every variable a search reads is
// empty, the home directory is empty and the system directories are none.
func isolateNodeSearch(t *testing.T) {
	t.Helper()
	for _, name := range []string{"PIG_NODE", "NVM_BIN", "NVM_DIR", "NVM_HOME", "NVM_SYMLINK", "FNM_DIR", "VOLTA_HOME", "ASDF_DATA_DIR", "MISE_DATA_DIR", "XDG_DATA_HOME", "APPDATA", "LOCALAPPDATA", "ProgramFiles"} {
		t.Setenv(name, "")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	setSystemNodeDirs(t)
}

// setSystemNodeDirs makes dirs the system directories until the test ends.
func setSystemNodeDirs(t *testing.T, dirs ...string) {
	t.Helper()
	saved := systemNodeDirs.Swap(&dirs)
	t.Cleanup(func() { systemNodeDirs.Store(saved) })
}

// nodePlace is an install location of Node discoverNode searches: setup points the environment at root and returns the
// directory a `node` goes into.
type nodePlace struct {
	name  string
	setup func(t *testing.T, root string) string
	// shadowed is set for a place another place's variable replaces when both are set, so the two cannot be installed together.
	shadowed bool
}

func envPlace(name, variable string, below ...string) nodePlace {
	return nodePlace{name: name, setup: func(t *testing.T, root string) string {
		t.Setenv(variable, filepath.Join(root, name))
		return filepath.Join(append([]string{root, name}, below...)...)
	}}
}

// shadowedPlace marks place as one another place's variable replaces when both are set.
func shadowedPlace(place nodePlace) nodePlace {
	place.shadowed = true
	return place
}

// nodePlaces are the places after PATH in the order discoverNode tries them.
func nodePlaces() []nodePlace {
	if runtime.GOOS == "windows" {
		return []nodePlace{
			envPlace("nvm-symlink", "NVM_SYMLINK"),
			envPlace("nvm-home", "NVM_HOME", "v22.19.0"),
			// %APPDATA%\nvm is where nvm-windows lives when NVM_HOME is empty; a set NVM_HOME replaces it, as $NVM_DIR replaces ~/.nvm.
			shadowedPlace(envPlace("appdata", "APPDATA", "nvm", "v22.19.0")),
			envPlace("fnm", "FNM_DIR", "node-versions", "v22.19.0", "installation"),
			envPlace("volta", "VOLTA_HOME", "tools", "image", "node", "22.19.0"),
			envPlace("program-files", "ProgramFiles", "nodejs"),
			envPlace("local-app-data", "LOCALAPPDATA", "Programs", "nodejs"),
		}
	}
	return []nodePlace{
		envPlace("nvm-bin", "NVM_BIN"),
		envPlace("nvm-dir", "NVM_DIR", "versions", "node", "v22.19.0", "bin"),
		{name: "nvm-home-dir", shadowed: true, setup: func(t *testing.T, root string) string {
			t.Setenv("HOME", filepath.Join(root, "nvm-home-dir"))
			t.Setenv("USERPROFILE", filepath.Join(root, "nvm-home-dir"))
			return filepath.Join(root, "nvm-home-dir", ".nvm", "versions", "node", "v24.0.0", "bin")
		}},
		envPlace("fnm", "FNM_DIR", "node-versions", "v22.19.0", "installation", "bin"),
		envPlace("volta", "VOLTA_HOME", "tools", "image", "node", "22.19.0", "bin"),
		envPlace("asdf", "ASDF_DATA_DIR", "shims"),
		envPlace("mise", "MISE_DATA_DIR", "installs", "node", "22.19.0", "bin"),
		{name: "system", setup: func(t *testing.T, root string) string {
			dir := filepath.Join(root, "system", "bin")
			setSystemNodeDirs(t, dir)
			return dir
		}},
	}
}

func discoverFake(t *testing.T) (string, error) {
	t.Helper()
	return discoverNode(background, nil)
}

func TestDiscoverNodeFindsNodeInEachPlaceAfterPATH(t *testing.T) {
	for _, place := range nodePlaces() {
		t.Run(place.name, func(t *testing.T) {
			isolateNodeSearch(t)
			t.Setenv("PATH", t.TempDir())
			want := writeFakeNode(t, place.setup(t, t.TempDir()), "v22.19.0", "", 0)
			got, err := discoverFake(t)
			if err != nil || got != want {
				t.Fatalf("discoverNode = %q, %v; want %q", got, err, want)
			}
		})
	}
}

func TestDiscoverNodeFindsNodeOnPATH(t *testing.T) {
	isolateNodeSearch(t)
	want := writeFakeNode(t, t.TempDir(), "v24.1.0", "", 0)
	t.Setenv("PATH", filepath.Dir(want))
	if got, err := discoverFake(t); err != nil || got != want {
		t.Fatalf("discoverNode = %q, %v; want %q", got, err, want)
	}
}

func TestDiscoverNodeFindsNodeNamedByPIGNODE(t *testing.T) {
	isolateNodeSearch(t)
	onPath := writeFakeNode(t, t.TempDir(), "v24.1.0", "", 0)
	t.Setenv("PATH", filepath.Dir(onPath))
	want := writeFakeNode(t, t.TempDir(), "v22.19.0", "", 0)
	t.Setenv("PIG_NODE", want)
	if got, err := discoverFake(t); err != nil || got != want {
		t.Fatalf("discoverNode = %q, %v; want PIG_NODE's %q before PATH's", got, err, want)
	}
}

// PIG_NODE is the one candidate: what is wrong with it is the error, and the good Node on PATH is not used instead.
func TestDiscoverNodeWithAnUnusablePIGNODEIsAnErrorWhateverElseIsInstalled(t *testing.T) {
	requirement := "evaluating plugins needs Node.js 22.19.0 or newer; "
	for _, test := range []struct {
		name   string
		make   func(t *testing.T) string
		wanted string
	}{
		{"missing", func(t *testing.T) string { return filepath.Join(t.TempDir(), "no-node") }, `PIG_NODE="`},
		{"too old", func(t *testing.T) string { return writeFakeNode(t, t.TempDir(), "v22.18.0", "", 0) }, ": found v22.18.0"},
		{"broken", func(t *testing.T) string { return writeFakeNode(t, t.TempDir(), "", "broken install", 4) }, ": node --version failed: broken install"},
		{"not a version", func(t *testing.T) string { return writeFakeNode(t, t.TempDir(), "hello", "", 0) }, `: node --version returned "hello"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			isolateNodeSearch(t)
			good := writeFakeNode(t, t.TempDir(), "v24.1.0", "", 0)
			t.Setenv("PATH", filepath.Dir(good))
			places := nodePlaces()
			writeFakeNode(t, places[0].setup(t, t.TempDir()), "v24.1.0", "", 0)
			t.Setenv("PIG_NODE", test.make(t))
			got, err := discoverFake(t)
			if err == nil || got != "" || !strings.HasPrefix(err.Error(), requirement+"PIG_NODE=") || !strings.Contains(err.Error(), test.wanted) {
				t.Fatalf("discoverNode = %q, %v; want an error that names PIG_NODE (%s)", got, err, test.wanted)
			}
		})
	}
}

func TestDiscoverNodePrefersPATHToTheInstallLocationsAndEachPlaceToTheNext(t *testing.T) {
	isolateNodeSearch(t)
	places := slices.DeleteFunc(nodePlaces(), func(place nodePlace) bool { return place.shadowed })
	root := t.TempDir()
	var paths []string
	for _, place := range places {
		paths = append(paths, writeFakeNode(t, place.setup(t, root), "v22.19.0", "", 0))
	}
	t.Setenv("PATH", t.TempDir())
	for i, want := range paths {
		got, err := discoverFake(t)
		if err != nil || got != want {
			t.Fatalf("with %s installed and the places before it empty: discoverNode = %q, %v; want %q", places[i].name, got, err, want)
		}
		// Make the one found too old to take the next place's turn in the next round.
		writeFakeNode(t, filepath.Dir(want), "v18.0.0", "", 0)
	}
	onPath := writeFakeNode(t, t.TempDir(), "v22.19.0", "", 0)
	t.Setenv("PATH", filepath.Dir(onPath))
	if got, err := discoverFake(t); err != nil || got != onPath {
		t.Fatalf("with PATH satisfying: discoverNode = %q, %v; want %q", got, err, onPath)
	}
}

func TestDiscoverNodeSkipsATooOldCandidateForANewerLaterOne(t *testing.T) {
	isolateNodeSearch(t)
	old := writeFakeNode(t, t.TempDir(), "v22.18.0", "", 0)
	t.Setenv("PATH", filepath.Dir(old))
	places := nodePlaces()
	writeFakeNode(t, places[0].setup(t, t.TempDir()), "v20.0.0", "", 0)
	want := writeFakeNode(t, places[len(places)-1].setup(t, t.TempDir()), "v22.19.0", "", 0)
	if got, err := discoverFake(t); err != nil || got != want {
		t.Fatalf("discoverNode = %q, %v; want %q", got, err, want)
	}
}

func TestDiscoverNodeWhenEveryNodeIsTooOldNamesTheNewestVersionFound(t *testing.T) {
	isolateNodeSearch(t)
	old := writeFakeNode(t, t.TempDir(), "v22.1.0", "", 0)
	t.Setenv("PATH", filepath.Dir(old))
	places := nodePlaces()
	newest := writeFakeNode(t, places[0].setup(t, t.TempDir()), "v22.18.0", "", 0)
	writeFakeNode(t, places[1].setup(t, t.TempDir()), "v20.9.0", "", 0)
	_, err := discoverFake(t)
	want := "evaluating plugins needs Node.js 22.19.0 or newer; found v22.18.0 at " + newest
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func TestDiscoverNodeFindsNothingAndNamesTheRequirementAndWhereItLooked(t *testing.T) {
	isolateNodeSearch(t)
	t.Setenv("PATH", t.TempDir())
	places := nodePlaces()
	var dirs []string
	for _, place := range places[:2] {
		dirs = append(dirs, filepath.Dir(writeFakeNode(t, place.setup(t, t.TempDir()), "v22.19.0", "", 0)))
	}
	for _, dir := range dirs {
		// Remove the executables: the directories stay, as an install whose Node was deleted.
		matches, _ := filepath.Glob(filepath.Join(dir, "node*"))
		for _, match := range matches {
			if err := os.Remove(match); err != nil {
				t.Fatal(err)
			}
		}
	}
	_, err := discoverFake(t)
	if err == nil {
		t.Fatal("discoverNode found a Node")
	}
	text := err.Error()
	for _, want := range []string{"evaluating plugins needs Node.js 22.19.0 or newer; node was not found on PATH or in ", "PIG_NODE"} {
		if !strings.Contains(text, want) {
			t.Fatalf("error = %q, want it to contain %q", text, want)
		}
	}
	if !strings.Contains(text, dirs[0]) {
		t.Fatalf("error = %q does not name where it looked", text)
	}
}

// A directory of versions is searched newest first by version number (v22.19.0 is newer than v9.0.0).
func TestDiscoverNodeTakesTheNewestInstalledVersionOfAVersionManager(t *testing.T) {
	isolateNodeSearch(t)
	t.Setenv("PATH", t.TempDir())
	root := t.TempDir()
	var versionsDir string
	if runtime.GOOS == "windows" {
		t.Setenv("NVM_HOME", root)
		versionsDir = root
	} else {
		t.Setenv("NVM_DIR", root)
		versionsDir = filepath.Join(root, "versions", "node")
	}
	var want string
	for _, version := range []string{"v9.0.0", "v22.19.0", "v22.20.1", "v20.5.0"} {
		dir := filepath.Join(versionsDir, version)
		if runtime.GOOS != "windows" {
			dir = filepath.Join(dir, "bin")
		}
		path := writeFakeNode(t, dir, version, "", 0)
		if version == "v22.20.1" {
			want = path
		}
	}
	if got, err := discoverFake(t); err != nil || got != want {
		t.Fatalf("discoverNode = %q, %v; want %q", got, err, want)
	}
}

// A Node that prints its version without the `v` is a version (the extension host's own check adds it too).
func TestDiscoverNodeTakesAVersionPrintedWithoutTheVPrefix(t *testing.T) {
	isolateNodeSearch(t)
	want := writeFakeNode(t, t.TempDir(), "22.19.0", "", 0)
	t.Setenv("PATH", filepath.Dir(want))
	if got, err := discoverFake(t); err != nil || got != want {
		t.Fatalf("discoverNode = %q, %v; want %q", got, err, want)
	}
	old := writeFakeNode(t, t.TempDir(), "22.18.0", "", 0)
	t.Setenv("PATH", filepath.Dir(old))
	_, err := discoverFake(t)
	if wanted := "found v22.18.0 at " + old; err == nil || !strings.HasSuffix(err.Error(), wanted) {
		t.Fatalf("error = %v, want it to end with %q", err, wanted)
	}
}

// fnm's base directory without $FNM_DIR is its own default: the XDG data directory, then ~/.fnm, and on macOS the place fnm
// used before 1.37 (Library/Application Support); on Windows %APPDATA%\fnm. Each is searched; the first that has a Node wins.
func TestDiscoverNodeFindsFnmAtItsDefaultDirectoriesWithoutFNMDIR(t *testing.T) {
	type place struct {
		name string
		// base is fnm's base directory below the home (or APPDATA on Windows).
		base []string
	}
	var places []place
	switch runtime.GOOS {
	case "windows":
		places = []place{{"appdata", []string{"fnm"}}}
	case "darwin":
		places = []place{{"xdg default", []string{".local", "share", "fnm"}}, {"dot fnm", []string{".fnm"}}, {"library", []string{"Library", "Application Support", "fnm"}}}
	default:
		places = []place{{"xdg default", []string{".local", "share", "fnm"}}, {"dot fnm", []string{".fnm"}}}
	}
	install := func(t *testing.T, base []string, version string) string {
		home := os.Getenv("HOME")
		if runtime.GOOS == "windows" {
			home = os.Getenv("APPDATA")
		}
		dir := filepath.Join(append([]string{home}, base...)...)
		dir = filepath.Join(dir, "node-versions", version, "installation")
		if runtime.GOOS != "windows" {
			dir = filepath.Join(dir, "bin")
		}
		return writeFakeNode(t, dir, version, "", 0)
	}
	setup := func(t *testing.T) {
		isolateNodeSearch(t)
		t.Setenv("PATH", t.TempDir())
		if runtime.GOOS == "windows" {
			t.Setenv("APPDATA", t.TempDir())
		}
	}
	for _, each := range places {
		t.Run(each.name, func(t *testing.T) {
			setup(t)
			want := install(t, each.base, "v22.19.0")
			if got, err := discoverFake(t); err != nil || got != want {
				t.Fatalf("discoverNode = %q, %v; want %q", got, err, want)
			}
		})
	}
	t.Run("the first default that has a Node wins", func(t *testing.T) {
		setup(t)
		var installed []string
		for _, each := range places {
			installed = append(installed, install(t, each.base, "v22.19.0"))
		}
		if got, err := discoverFake(t); err != nil || got != installed[0] {
			t.Fatalf("discoverNode = %q, %v; want %q", got, err, installed[0])
		}
	})
	t.Run("FNM_DIR replaces the defaults", func(t *testing.T) {
		setup(t)
		install(t, places[0].base, "v22.19.0")
		other := t.TempDir()
		t.Setenv("FNM_DIR", other)
		if got, err := discoverFake(t); err == nil {
			t.Fatalf("discoverNode = %q, want it to look only at FNM_DIR", got)
		}
		want := writeFakeNode(t, filepath.Join(other, "node-versions", "v22.19.0", "installation", func() string {
			if runtime.GOOS == "windows" {
				return ""
			}
			return "bin"
		}()), "v22.19.0", "", 0)
		if got, err := discoverFake(t); err != nil || got != want {
			t.Fatalf("discoverNode = %q, %v; want %q", got, err, want)
		}
	})
}
