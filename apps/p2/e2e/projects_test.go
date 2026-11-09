// Ports test/p2/projects.test.ts (Pi main c23b9ec1), the server cases: `Projects` is owner-only, follows projects.json and
// names this device by its hostname; membership is assigned to projects.json's projects only, followed by copies, and kept
// when the project goes.
package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	projectscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/projects/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/projects"
)

const piProject = `{"pi":[{"env":"mac","path":"~/projects/pi/pi"},{"path":"~/projects/pi/pi"}]}`

func writeProjects(t *testing.T, fixture *p2test.Fixture, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fixture.DataDir, "projects.json"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func projectCount(value delta.JsonValue) int {
	list, _ := value.([]any)
	return len(list)
}

func TestProjectsOwnerOnlyFollowsProjectsJSONAndNamesTheDevice(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{Plugins: []string{"devices", "projects"}})
	owner := fixture.Browser()
	projects, err := p2test.HoldIn(owner, projectscontract.Projects, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	defer projects.Release()
	hostname, _ := os.Hostname()
	if label, err := p2test.Until(projects.Member("local"), func(v delta.JsonValue) bool { return v != nil }, 0); err != nil || label != hostname {
		t.Fatalf("local = %v, %v; want %s", label, err, hostname)
	}
	if list, err := p2test.Until(projects.Member("projects"), func(v delta.JsonValue) bool { return v != nil }, 0); err != nil || projectCount(list) != 0 {
		t.Fatalf("projects = %v, %v", list, err)
	}
	writeProjects(t, fixture, piProject)
	if _, err := p2test.Until(projects.Member("projects"), func(v delta.JsonValue) bool {
		list, _ := v.([]any)
		if len(list) == 0 {
			return false
		}
		places, _ := list[0].(*delta.JsonObject).Value("places").([]any)
		return len(places) == 2 && places[1].(*delta.JsonObject).Value("env") == nil
	}, 0); err != nil {
		t.Fatal(err)
	}
	writeProjects(t, fixture, "{")
	if _, err := p2test.Until(projects.Member("failure"), func(v delta.JsonValue) bool { s, _ := v.(string); return s != "" }, 0); err != nil {
		t.Fatal(err)
	}
	if list, _ := projects.Member("projects").Value(); projectCount(list) != 1 || !strings.Contains(chordMarshal(t, list), `"name":"pi"`) {
		t.Fatalf("projects after a bad save = %v", list)
	}
	writeProjects(t, fixture, "{}")
	if _, err := p2test.Until(projects.Member("failure"), func(v delta.JsonValue) bool { return v == nil }, 0); err != nil {
		t.Fatal(err)
	}
	if list, _ := projects.Member("projects").Value(); projectCount(list) != 0 {
		t.Fatalf("projects after {} = %v", list)
	}
	guest := fixture.As(p2runtime.GuestPrincipal{ID: "guest", Name: "Guest", Home: "unknown", Sessions: map[string]p2runtime.GuestMode{"unknown": "full"}, Revision: 1})
	denied, err := guest.Hold(p2test.Background, projectscontract.Projects, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	p2test.ExpectChord(t, p2runtime.WhenLive(p2test.Background, denied), data.CodeForbidden)
	denied.Release()
}

func TestProjectMembership(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{})
	writeProjects(t, fixture, piProject)
	owner := fixture.Browser()
	projects, err := p2test.HoldIn(owner, projectscontract.Projects, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	defer projects.Release()
	if _, err := p2test.Until(projects.Member("projects"), func(v delta.JsonValue) bool { return projectCount(v) == 1 }, 0); err != nil {
		t.Fatal(err)
	}
	sessions, err := p2test.HoldIn(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	defer sessions.Release()
	id := "0b9cbb25-2c2d-4a38-9f0e-1f0d4c9a7e11"
	if _, err := projects.Call("assign", delta.JsonObjectOf("session", id, "project", "pi"), p2test.Background); err != nil {
		t.Fatal(err)
	}
	if _, err := projects.Call("assign", delta.JsonObjectOf("session", id, "project", "nope"), p2test.Background); err == nil || !strings.Contains(err.Error(), `No project "nope"`) {
		t.Fatalf("assign to a project projects.json does not name: %v", err)
	}
	create := func(args *delta.JsonObject) string {
		created, err := sessions.Call("create", args, p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		return object(created).Value("id").(string)
	}
	source := create(delta.JsonObjectOf("id", id, "cwd", fixture.Workspace, "title", "In pi"))
	other := create(delta.JsonObjectOf("cwd", fixture.Workspace, "title", "Free"))
	// A clone (and a fork: the same path, `SessionCopied`) belongs to its source's project; others to none.
	clone := func(from string) string {
		copied, err := sessions.Call("clone", delta.JsonObjectOf("id", from), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		return object(copied).Value("id").(string)
	}
	copied, plain := clone(source), clone(other)
	members, err := p2test.Until(projects.Member("sessions"), func(v delta.JsonValue) bool { return object(v).Value(copied) == "pi" }, 0)
	if err != nil {
		t.Fatal(err)
	}
	if object(members).Len() != 2 || object(members).Value(source) != "pi" || object(members).Value(plain) != nil {
		t.Fatalf("members = %s", chordMarshal(t, members))
	}
	// The session catalog is untouched: rows have no project.
	rows, _ := sessions.Member("sessions").Value()
	for _, row := range rows.([]any) {
		if object(row).Value("project") != nil {
			t.Fatalf("a session row has a project: %s", chordMarshal(t, row))
		}
	}
	// A project gone from projects.json keeps its members' names.
	writeProjects(t, fixture, "{}")
	if _, err := p2test.Until(projects.Member("projects"), func(v delta.JsonValue) bool { return projectCount(v) == 0 }, 0); err != nil {
		t.Fatal(err)
	}
	if now, _ := projects.Member("sessions").Value(); object(now).Value(source) != "pi" {
		t.Fatalf("members after the project went = %s", chordMarshal(t, now))
	}
}

func chordMarshal(t *testing.T, value delta.JsonValue) string {
	t.Helper()
	encoded, err := chordjson.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
