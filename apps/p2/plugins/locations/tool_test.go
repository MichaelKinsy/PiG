package locations

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/durable"
)

// Ports the first case of test/p2/locations.test.ts (Pi main c23b9ec1), "location tools: schemas and defaults".
func TestLocationToolsSchemasAndDefaults(t *testing.T) {
	extension, err := CreateExtension(Tools{
		Server: stubServer{},
		Locate: func(context.Context, durable.ConversationId, durable.DocumentReader) (p2runtime.Location, error) {
			return p2runtime.Location{Cwd: "/"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if extension.Extension.Name != "p2-locations" {
		t.Fatalf("name = %q", extension.Extension.Name)
	}
	var names []string
	for _, tool := range extension.Tools {
		names = append(names, tool.Name)
		// Offered to the model by default (not deferred): every main agent has them.
		if tool.Deferred {
			t.Errorf("%s is deferred", tool.Name)
		}
		if tool.Origin != "owner" || tool.Replay != durable.ReplaySafe {
			t.Errorf("%s: origin %q, replay %v", tool.Name, tool.Origin, tool.Replay)
		}
	}
	if strings.Join(names, ",") != "location_envs,location_dirs,location_move" {
		t.Fatalf("tools = %v", names)
	}
	declared, err := json.Marshal(extension.Tools[2].ToolSchema)
	if err != nil {
		t.Fatal(err)
	}
	text := string(declared)
	location, env, cwd := strings.Index(text, `"location":`), strings.Index(text, `"env":`), strings.Index(text, `"cwd":`)
	if location < 0 || !(location < env && env < cwd) {
		t.Fatalf("location_move parameters are not location, env, cwd in this order: %s", text)
	}
}

type stubServer struct{}

func (stubServer) Envs(context.Context) ([]EnvInfo, error) { return nil, nil }
func (stubServer) Directories(context.Context, *string, string) (p2runtime.DirectoryListing, error) {
	return p2runtime.DirectoryListing{Path: "/"}, nil
}

func TestSandboxAtIsDecidedByTheConfiguredTypeNeverTheName(t *testing.T) {
	envs := []EnvInfo{{Name: "gondolin", Type: "ssh"}, {Name: "box", Type: "gondolin", Host: "gpu"}}
	box, plain := "box", "gondolin"
	for _, test := range []struct {
		here p2runtime.Location
		want string
	}{
		{p2runtime.Location{Cwd: "/", Env: &box}, "box"},
		{p2runtime.Location{Cwd: "/", Env: &plain}, ""},
		{p2runtime.Location{Cwd: "/"}, ""},
	} {
		if got := SandboxAt(test.here, envs); got != test.want {
			t.Errorf("SandboxAt(%+v) = %q, want %q", test.here, got, test.want)
		}
	}
}

func TestEnvsAreListedWithTheirLastTestWhileItTestsTheCurrentConfiguration(t *testing.T) {
	configs := mustObject(t, `{"gpu":{"type":"ssh","host":"box","user":"me","port":2222},"vm":{"type":"gondolin","host":"gpu"}}`)
	tests := func(revision string) *jsonObject {
		return mustObject(t, `{"gpu":{"revision":"`+revision+`","test":{"ok":true,"platform":"linux","arch":"x64"}},"vm":{"revision":"x","test":{"ok":false,"error":"no"}}}`)
	}
	got := marshal(t, envInfos(configs, tests(revisionOf(t, configs, "gpu"))))
	want := `[{"name":"gpu","type":"ssh","host":"box","user":"me","port":2222,"tested":{"ok":true,"platform":"linux","arch":"x64"}},{"name":"vm","type":"gondolin","host":"gpu"}]`
	if got != want {
		t.Errorf("envs = %s\nwant   %s", got, want)
	}
	if got := marshal(t, envInfos(configs, tests("stale"))); strings.Contains(got, "tested") {
		t.Errorf("a test of an older configuration counts: %s", got)
	}
}
