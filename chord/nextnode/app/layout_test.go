package app

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Ports packages/chord/test/node/app/layout.test.ts: the data directory's layout (the directories made, the data
// directory 0700, the default data directory) and worker directories (one directory name per address key, distinct even
// on a case-insensitive file system, never leaving `workers/<kind>/`).

func docAddress(t *testing.T, id string) contracts.Address {
	t.Helper()
	kind, err := contracts.NewHostKind("doc", contracts.Prop("id", contracts.Str()))
	if err != nil {
		t.Fatal(err)
	}
	return kind.Address(delta.JsonObjectOf("id", id))
}

func TestTheDataDirectoryMakesTheLayoutsDirectoriesTheDataDirectoryWithMode0700(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	layout, err := CreateLayout(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dataDir)
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "logs,plugins,server,workers" {
		t.Fatalf("entries = %v", names)
	}
	if mode := modeOf(t, dataDir); mode != 0o700 {
		t.Fatalf("mode = %o", mode)
	}
	want := []string{filepath.Join(dataDir, "server", "store.sqlite"), filepath.Join(dataDir, "owner-token"), filepath.Join(dataDir, "server", "cookie-secret")}
	if got := []string{layout.ServerStore, layout.OwnerToken, layout.CookieSecret}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if got := DefaultDataDir("acme"); got != filepath.Join(home, ".acme") {
		t.Fatalf("DefaultDataDir = %q", got)
	}
}

func TestWorkerDirectoriesEncodeAnAddressKeyAsOneDirectoryNameUnderItsKind(t *testing.T) {
	dir := WorkerDirectory("/data", docAddress(t, "../../etc/x"))
	if filepath.Dir(dir) != "/data/workers/doc" || filepath.Base(dir) != "doc%7C%2E%2E%2F%2E%2E%2Fetc%2Fx" {
		t.Fatalf("dir = %q", dir)
	}
	if got := filepath.Base(WorkerDirectory("/data", docAddress(t, "."))); got != "doc%7C%2E" {
		t.Fatalf("dot = %q", got)
	}
}

func TestWorkerDirectoriesKeepKeysDifferingOnlyInCaseApart(t *testing.T) {
	seen := map[string]bool{}
	for _, id := range []string{"a", "A", "%41", "%2541"} {
		seen[strings.ToLower(filepath.Base(WorkerDirectory("/data", docAddress(t, id))))] = true
	}
	if len(seen) != 4 {
		t.Fatalf("names = %v", seen)
	}
}

func TestWorkerDirectoriesNameALongKeyByItsHash(t *testing.T) {
	name := filepath.Base(WorkerDirectory("/data", docAddress(t, strings.Repeat("x", 200))))
	if !regexp.MustCompile(`^sha256-[0-9a-f]{64}$`).MatchString(name) {
		t.Fatalf("name = %q", name)
	}
}
