// Ports test/p2/projects.test.ts (Pi main c23b9ec1): parsing and validation, the observed file keeping its last valid
// state. The owner-only `Projects` and membership cases run on a full server (e2e/projects_test.go).
package projects_test

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/projects"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/projects/contract"
)

func TestMain(m *testing.M) { p2test.Main(m) }

const pi = `{"pi":[{"env":"mac","path":"~/projects/pi/pi"},{"path":"~/projects/pi/pi"}]}`

func ptr(text string) *string { return &text }

func TestProjectsJSONOrderKeptInvalidFilesSayWhy(t *testing.T) {
	got, err := projects.ParseProjects(`// comments and trailing commas, as in the other configuration files
		{ "web": [{ "path": "/srv/web" }], "pi": [{ "env": "mac", "path": "~/projects/pi/pi" }, { "path": "~/projects/pi/pi" },], }`)
	if err != nil {
		t.Fatal(err)
	}
	want := []contract.Project{
		{Name: "web", Places: []contract.ProjectPlace{{Path: "/srv/web"}}},
		{Name: "pi", Places: []contract.ProjectPlace{{Env: ptr("mac"), Path: "~/projects/pi/pi"}, {Path: "~/projects/pi/pi"}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("projects = %+v, want %+v", got, want)
	}
	if empty, err := projects.ParseProjects("{}"); err != nil || len(empty) != 0 {
		t.Fatalf("{} = %v, %v", empty, err)
	}
	for content, pattern := range map[string]string{
		"{":                            "syntax",
		"[]":                           "must be an object",
		`{ "pi": [] }`:                 "pi: a list of places",
		`{ "pi": [{ "env": "mac" }] }`: "pi: a place is",
		`{ "pi": [{ "path": "/a", "host": "x" }] }`:                                  "pi: a place is",
		`{ "pi": [{ "env": "Mac", "path": "/a" }] }`:                                 "invalid environment name Mac",
		`{ "pi": [{ "path": "projects/pi" }] }`:                                      "absolute or start with ~",
		`{ "pi": [{ "env": "mac", "path": "/a" }, { "env": "mac", "path": "/b" }] }`: "two places on mac",
		`{ "pi": [{ "path": "/a" }, { "path": "~/b" }] }`:                            "two places on this device",
		`{ "": [{ "path": "/a" }] }`:                                                 "Invalid project name",
	} {
		_, err := projects.ParseProjects(content)
		if err == nil || !regexp.MustCompile(pattern).MatchString(err.Error()) {
			t.Errorf("%s: error %v, want /%s/", content, err, pattern)
		}
	}
}

func TestObservedProjectsKeepTheirLastValidStateAndSayWhy(t *testing.T) {
	p2test.CheckLeaks(t, 0)
	directory := t.TempDir()
	store := projects.NewProjectStore(directory)
	t.Cleanup(store.Close)
	if err := store.Start(p2test.Background); err != nil {
		t.Fatal(err)
	}
	if snapshot := store.Snapshot(); len(snapshot.Projects) != 0 || snapshot.Error != "" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	path := filepath.Join(directory, "projects.json")
	if err := os.WriteFile(path, []byte(pi), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p2test.Poll(func() (bool, error) { return len(store.Snapshot().Projects) == 1, nil }, 20*time.Second, "projects"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{ "pi": [{ "path": "relative" }] }`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p2test.Poll(func() (bool, error) { return store.Snapshot().Error != "", nil }, 20*time.Second, "the failure"); err != nil {
		t.Fatal(err)
	}
	snapshot := store.Snapshot()
	if !strings.HasPrefix(snapshot.Error, "Could not load projects.json (Project pi: the path must be absolute") {
		t.Fatalf("error = %q", snapshot.Error)
	}
	if len(snapshot.Projects) != 1 || snapshot.Projects[0].Name != "pi" || len(snapshot.Projects[0].Places) != 2 {
		t.Fatalf("projects = %+v", snapshot.Projects)
	}
}
