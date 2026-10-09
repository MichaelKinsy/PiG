package experimental

import (
	"testing"
)

// upstream: packages/coding-agent/src/experimental/client.ts:41-44 and client-runtime.ts:95-97. With two discovered servers a prompt that needs a new Session, and a plugin selection, fail with Pi's diagnostics before any Session is created or attached.
func TestClientRuntimeRejectsAmbiguousServerSelection(t *testing.T) {
	requirePOSIXServerDirectory(t)
	setupExperimentalRemoteTest(t)
	directory := socketDir(t)
	for _, serverID := range []string{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"} {
		runtime, err := StartServer(t.Context(), StartServerOptions{Directory: &directory, ServerId: &serverID, Provider: new("anthropic"), Model: new("claude-sonnet-4-5")})
		if err != nil {
			t.Fatal(err)
		}
		trackExperimentalServer(t, runtime)
	}
	for _, row := range []struct {
		name    string
		command ClientCommand
		want    string
	}{
		{"a prompt without a Session", ClientCommand{Command: "client", Prompt: new("hello")}, "Client prompt requires exactly one discovered server to create a Session"},
		{"a plugin selection", ClientCommand{Command: "client", PluginPackages: []string{"./plugin"}}, "Plugin selection requires exactly one local server"},
	} {
		t.Run(row.name, func(t *testing.T) {
			if _, err := RunClient(t.Context(), row.command, RunClientOptions{Directory: &directory}); err == nil || err.Error() != row.want {
				t.Fatalf("RunClient = %v, want %q", err, row.want)
			}
		})
	}
}

// upstream: packages/coding-agent/src/experimental/client-runtime.ts:225-230 derives the server ID from the --connect path's file name: it must be "<lowercase UUIDv4>.sock", in any directory.
func TestRouteFromExplicitPathRequiresAServerIDSocketName(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000001"
	for _, row := range []struct {
		path    string
		want    string
		invalid bool
	}{
		{path: "/run/pi/" + id + ".sock", want: id},
		{path: "relative/" + id + ".sock", want: id},
		{path: "/run/pi/" + id, invalid: true},
		{path: "/run/pi/server.sock", invalid: true},
		{path: "/run/pi/00000000-0000-1000-8000-000000000001.sock", invalid: true},
		{path: "/run/pi/00000000-0000-4000-8000-00000000000A.sock", invalid: true},
	} {
		route, err := routeFromExplicitPath(row.path)
		if row.invalid {
			if err == nil || err.Error() != "--connect path must end with <uuidv4-server-id>.sock" {
				t.Fatalf("%s = %v, %v", row.path, route, err)
			}
			continue
		}
		if err != nil || route.ServerId != row.want || route.Transport != "unix" || *route.Path != row.path {
			t.Fatalf("%s = %+v, %v", row.path, route, err)
		}
	}
}
