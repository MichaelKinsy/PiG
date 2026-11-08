package locations

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
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

// conversationAPI is a tool call's API of conversation id: the refusals come before anything reads the conversation's
// documents, so nothing else is there.
type conversationAPI struct {
	durable.ToolExecutionApi
	id durable.ConversationId
}

func (api conversationAPI) ConversationId() durable.ConversationId { return api.id }

// listingServer lists every directory it is asked for as holding src, and records the environments it listed.
type listingServer struct {
	envs   []EnvInfo
	listed []string
}

func (server *listingServer) Envs(context.Context) ([]EnvInfo, error) { return server.envs, nil }

func (server *listingServer) Directories(_ context.Context, env *string, path string) (p2runtime.DirectoryListing, error) {
	if env == nil {
		server.listed = append(server.listed, "null")
	} else {
		server.listed = append(server.listed, *env)
	}
	return p2runtime.DirectoryListing{Path: path, Directories: []string{path + "/src"}}, nil
}

func resultText(result durable.ToolExecutionResult) string {
	var text strings.Builder
	for _, part := range result.Output {
		if content, ok := part.(ai.TextContent); ok {
			text.WriteString(content.Text)
		}
	}
	return text.String()
}

// Ports "location tools keep a sandboxed conversation in its sandbox" of test/p2/sandboxes.test.ts (Pi main c23b9ec1).
func TestLocationToolsKeepASandboxedConversationInItsSandbox(t *testing.T) {
	envs := []EnvInfo{
		{Name: "slayer", Type: "ssh", Host: "slayer.example"},
		{Name: "box1", Type: "gondolin", Host: "slayer"},
	}
	slayer, box1 := "slayer", "box1"
	for _, test := range []struct {
		here p2runtime.Location
		want string
	}{
		{p2runtime.Location{Env: &box1, Cwd: "/workspace"}, "box1"},
		{p2runtime.Location{Env: &slayer, Cwd: "/"}, ""},
		{p2runtime.Location{Cwd: "/"}, ""},
	} {
		if got := SandboxAt(test.here, envs); got != test.want {
			t.Errorf("SandboxAt(%+v) = %q, want %q", test.here, got, test.want)
		}
	}
	server := &listingServer{envs: envs}
	extension, err := CreateExtension(Tools{
		Server: server,
		Locate: func(context.Context, durable.ConversationId, durable.DocumentReader) (p2runtime.Location, error) {
			return p2runtime.Location{Env: &box1, Cwd: "/workspace"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	tool := func(name string) p2runtime.P2Tool {
		t.Helper()
		for _, each := range extension.Tools {
			if each.Name == name {
				return each
			}
		}
		t.Fatalf("no tool %s", name)
		return p2runtime.P2Tool{}
	}
	api := conversationAPI{id: 2}
	run := func(name string, args map[string]any) durable.ToolExecutionResult {
		t.Helper()
		result, err := tool(name).Execute(context.Background(), args, api)
		if err != nil {
			t.Fatalf("%s %v: %v", name, args, err)
		}
		return result
	}
	listedEnvs := resultText(run("location_envs", map[string]any{}))
	for _, pattern := range []string{`in sandbox box1`, `cannot leave it`} {
		if !regexp.MustCompile(pattern).MatchString(listedEnvs) {
			t.Errorf("location_envs %q does not match %s", listedEnvs, pattern)
		}
	}
	if regexp.MustCompile(`slayer\.example|this device`).MatchString(listedEnvs) {
		t.Errorf("location_envs shows what is outside the sandbox: %q", listedEnvs)
	}
	for _, args := range []map[string]any{{"location": "slayer:/home"}, {"location": "local:/tmp"}, {"env": nil, "cwd": "/tmp"}} {
		for _, name := range []string{"location_dirs", "location_move"} {
			result := run(name, args)
			if result.IsError == nil || !*result.IsError {
				t.Errorf("%s %v did not fail", name, args)
			}
			if text := resultText(result); !regexp.MustCompile(`sandbox box1 and cannot leave it`).MatchString(text) {
				t.Errorf("%s %v: %q", name, args, text)
			}
		}
	}
	if len(server.listed) != 0 {
		t.Fatalf("something outside the sandbox was listed: %v", server.listed)
	}
	inside := run("location_dirs", map[string]any{"location": "/workspace"})
	if inside.IsError != nil && *inside.IsError {
		t.Errorf("location_dirs inside the sandbox failed: %s", resultText(inside))
	}
	if text := resultText(inside); !strings.Contains(text, "box1:/workspace") {
		t.Errorf("location_dirs inside the sandbox: %q", text)
	}
	if strings.Join(server.listed, ",") != "box1" {
		t.Errorf("listed = %v, want [box1]", server.listed)
	}
}
