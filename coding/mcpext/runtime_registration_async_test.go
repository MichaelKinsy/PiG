package mcpext_test

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/mcp"
)

// registrationRig is the built-in MCP extension behind the production registry and the production Runner: a server registered on the
// runtime reaches the extension's mcp_servers_change handler through the Runner's listener. The transport of the server named "slow"
// is not created until release is called, as a server that is still connecting.
type registrationRig struct {
	host       *fakeHost
	ext        *mcpext.Extension
	runtime    *extension.ExtensionRuntime
	runner     *inproc.Runner
	connecting *serverList
	release    func()
}

func newRegistrationRig(t *testing.T) *registrationRig {
	t.Helper()
	rig := &registrationRig{host: newFakeHost(), runtime: extension.CreateExtensionRuntime(), connecting: &serverList{}}
	rig.host.registerBuiltin(mcpext.CodemodeToolName, "builtin:codemode")
	slow := make(chan struct{})
	rig.release = sync.OnceFunc(func() { close(slow) })
	rig.ext = mcpext.New(rig.host, mcpext.Options{
		LoadConfig: func(context.Context) mcpext.LoadedMcpConfig { return mcpext.LoadedMcpConfig{} },
		CreateTransport: func(entry mcpext.McpServerEntry, _ string, _ mcp.AuthProvider) (mcp.Transport, error) {
			rig.connecting.add(entry)
			if entry.Name == "slow" {
				<-slow
			}
			client, server := createFakeServer(&callLog{}, func() []map[string]any { return serverTools }, false)
			_ = server.Start()
			return client, nil
		},
		Credentials: mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, ""),
		LogPath:     t.TempDir() + "/mcp.log",
	})
	t.Cleanup(rig.ext.SessionShutdown)
	t.Cleanup(rig.release)

	cwd := t.TempDir()
	notes := &notifications{}
	// The handler the built-in registers (register.go: api.OnMcpServersChange), on an extension of the Runner.
	builtin := extension.Extension{Path: "builtin:mcp", ResolvedPath: "builtin:mcp"}
	builtin.InitializeEventHandlers()
	builtin.AddEventHandler("mcp_servers_change", 1, func(args ...any) (any, error) {
		rig.host.setServers(rig.runtime.McpServers().List()...)
		ctx := eventContext(cwd, notes)
		ctx.Context = args[1].(context.Context)
		rig.ext.McpServersChange(ctx)
		return nil, nil
	})
	rig.runner = inproc.NewRunner([]extension.Extension{builtin}, t.TempDir(), rig.runtime)
	rig.runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)
	rig.ext.SessionStart(eventContext(cwd, notes))
	return rig
}

func (rig *registrationRig) register(t *testing.T, name string) {
	t.Helper()
	if err := rig.runtime.RegisterMcpServer("/ext/plugin.ts", name, json.RawMessage(`{"url":"http://`+name+`.invalid"}`)); err != nil {
		t.Fatal(err)
	}
}

func (rig *registrationRig) listed() []string {
	var names []string
	for _, server := range rig.ext.Servers() {
		names = append(names, server.Entry.Name)
	}
	return names
}

func (rig *registrationRig) started(name string) bool {
	return slices.ContainsFunc(rig.connecting.all(), func(entry mcpext.McpServerEntry) bool { return entry.Name == name })
}

// A server an extension registers at run time (pi.registerMcpServer) reaches the built-in MCP extension inside the registration: Pi's
// listener calls emit without awaiting it (runner.ts:465-468), so the handler has listed the server and activated the discovery tool
// (extensions/mcp/index.ts:1218-1235) when registerMcpServer returns, and only what follows its first await (index.ts:1236), the
// connection, is still under way. A second server registered meanwhile is a second emit: it is listed and its connection starts while
// the first one is still connecting.
func TestAServerRegisteredAtRunTimeIsListedWhenTheRegistrationReturns(t *testing.T) {
	rig := newRegistrationRig(t)

	rig.register(t, "slow")
	if got := rig.listed(); !slices.Equal(got, []string{"slow"}) {
		t.Fatalf("servers listed when the registration returned = %v, want [slow]", got)
	}
	if got := rig.host.GetActiveTools(); !slices.Contains(got, mcpext.CodemodeToolName) {
		t.Fatalf("active tools when the registration returned = %v, want the discovery tool %s among them", got, mcpext.CodemodeToolName)
	}

	rig.register(t, "fast")
	if got := rig.listed(); !slices.Equal(got, []string{"slow", "fast"}) {
		t.Fatalf("servers listed when the second registration returned = %v, want [slow fast]", got)
	}
	waitFor(t, "the second server's connection to start while the first server is still connecting", func() bool { return rig.started("fast") })
	waitFor(t, "the second server's tools while the first server is still connecting", func() bool {
		return slices.Contains(rig.host.callable(), "mcp__fast__search")
	})
	if slices.Contains(rig.host.callable(), "mcp__slow__search") {
		t.Fatalf("the first server connected before its transport was released: callable = %v", rig.host.callable())
	}
	rig.release()
	waitFor(t, "the first server's tools", func() bool { return slices.Contains(rig.host.callable(), "mcp__slow__search") })
}

// Go addition: the Runner owns the emission that runs the handler, and the Session joins it when it closes. Invalidating the Runner
// ends the handler's context, and the handler stops waiting for the servers that are still connecting: they stay under the session,
// which closes them when it ends.
func TestAnInvalidatedRunnerEndsTheMcpServersChangeHandlersWaitForItsConnections(t *testing.T) {
	rig := newRegistrationRig(t)
	rig.register(t, "slow")
	waitFor(t, "the server's connection to start", func() bool { return rig.started("slow") })
	rig.runner.Invalidate("")
	drained := make(chan struct{})
	go func() {
		rig.runner.DrainMcpServersChanges()
		close(drained)
	}()
	waitFor(t, "the emission to end while its server is still connecting", func() bool {
		select {
		case <-drained:
			return true
		default:
			return false
		}
	})
}
