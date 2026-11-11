// A `send` the server routes to a worker that has started but is not ready yet (Load still running) is served on its own
// goroutine, while the plugins' last contributions are not in. Its Submit resumes scheduling, and the reserve pass
// snapshots the registry to plan the first request's tools: without the worker's load gate (p2runtime.LoadGate) the
// snapshot lacks a tool a plugin contributes just after, and the model's call of it is unoffered ("Tool ... is not
// available"). Pi's single event loop runs those contributions before it serves the send.
//
// The late plugin below is compiled into this test binary, which is also the session worker. It contributes its tool
// only after the worker has either planned a first turn (a request got past the gate: what the old code does) or has a
// request waiting at the gate (what the gate does), so the order of the race is forced, not waited for.

package loadgate_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/agent"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/artifacts"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/cache-warming"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat-mode"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/codemode"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/coding-tools"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/conversations"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/dev-tools"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/devices"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/discord"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/drafts"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/envs"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/file-index"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/files"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/history"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/locations"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/login"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/logs"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/mcp"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/models"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/ports"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/powershell"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/projects"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/prompt"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/proxy-marker"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/radius"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/search"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/send-file"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/settings"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/sharing"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/status"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/terminals"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/tree"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/views"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/web-shell"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/workspace"
)

func TestMain(m *testing.M) { p2test.Main(m) }

// probeEnv switches the late plugin on in a worker (the test's server passes it as worker environment).
const probeEnv = "P2_LOADGATE_PROBE"

// lateTool is the tool the late plugin contributes (the fake provider calls `mcp__…` tools by prompt).
const lateTool = "mcp__late"

var probeRuntime = func() *contracts.Contract {
	contract, err := contracts.Local("p2.loadgate-probe", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{}})
	if err != nil {
		panic(err)
	}
	return contract
}()

func init() {
	facet := p2runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
		return facet.Serve(probeRuntime, host.LocalFactory(serveProbe), host.ServeOptions{})
	})
	hosts.RegisterPlugin(hosts.Plugin{Name: "loadgate-probe", Worker: &facet})
}

// planned is closed when the main conversation holds an entry after the user's input: a turn got its tools planned.
type planned struct{ done chan struct{} }

func (p planned) Advance(_ context.Context, call chord.WatchCall[harness.ConversationView]) {
	if call.Kind == chord.WatchCallValue && len(call.Value.Entries) >= 2 {
		select {
		case <-p.done:
		default:
			close(p.done)
		}
	}
}
func (planned) End(chord.WatchEnd) {}

func serveProbe(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
	if os.Getenv(probeEnv) == "" {
		return contracts.Implementation{}, nil
	}
	harnessOf := p2runtime.HarnessOf(self)
	// The object goes live (and its contributions enter the tables) when this open returns: Load waits for it.
	self.Open(
		func(ctx context.Context) (any, error) {
			parts, err := harnessOf()
			if err != nil {
				return nil, err
			}
			opened, err := parts.Durable()
			if err != nil {
				return nil, err
			}
			seen := planned{done: make(chan struct{})}
			observation, err := opened.ObserveConversation(ctx, 1, seen, nil)
			if err != nil {
				return nil, err
			}
			defer observation.Detach()
			seen.Advance(ctx, chord.WatchCall[harness.ConversationView]{Kind: chord.WatchCallValue, Value: observation.Value()})
			select {
			case <-seen.done:
			case <-parts.Loaded.Blocked():
			case <-ctx.Done():
				return nil, context.Cause(ctx)
			}
			return nil, nil
		},
		func(any, host.NativeCloseReason, context.Context) error { return nil },
	)
	tool := &durable.ToolRegistration{
		ToolSchema: ai.ToolSchema{Name: lateTool, Description: "a tool a plugin contributes late", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
		Execute: func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "late ok"}}}, nil
		},
	}
	extension := &p2runtime.P2Extension{Extension: &durable.Extension{Name: "loadgate-probe"}, Tools: []p2runtime.P2Tool{{ToolRegistration: tool}}}
	self.(host.StoreSelf).Contribute(p2runtime.Extensions.Registry, p2runtime.Keyed[*p2runtime.P2Extension]{p2runtime.Entry("loadgate-probe", extension)})
	return contracts.Implementation{}, nil
}

func object(value any) *delta.JsonObject { o, _ := value.(*delta.JsonObject); return o }

// results are the tool results of toolName in a conversation view.
func results(view delta.JsonValue, toolName string) []*delta.JsonObject {
	var out []*delta.JsonObject
	entries, _ := object(view).Value("entries").([]any)
	for _, entry := range entries {
		model, _ := object(entry).Value("model").([]any)
		for _, each := range model {
			if message := object(each); message != nil && message.Value("role") == "toolResult" && message.Value("toolName") == toolName {
				out = append(out, message)
			}
		}
	}
	return out
}

func text(result *delta.JsonObject) string {
	var out strings.Builder
	parts, _ := result.Value("content").([]any)
	for _, part := range parts {
		if s, ok := object(part).Value("text").(string); ok {
			out.WriteString(s)
		}
	}
	return out.String()
}

func TestSendRoutedBeforeTheWorkerIsLoadedOffersThePluginsTool(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{WorkerEnv: map[string]string{probeEnv: "1"}})
	browser := fixture.Browser()
	sessions, err := p2test.HoldIn(browser, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := p2test.HoldIn(browser, chatcontract.Chat, delta.JsonObjectOf("session", object(created).Value("id"), "conversation", float64(1)), "participant")
	if err != nil {
		t.Fatal(err)
	}
	// The send is made at once: the server routes it as soon as the worker has started, while Load waits for the late plugin.
	if _, err := chat.Call("send", delta.JsonObjectOf("text", lateTool+" {}", "requestId", "r1"), p2test.Background); err != nil {
		t.Fatal(err)
	}
	view, err := p2test.Until(chat.Member("view"), func(view delta.JsonValue) bool { return len(results(view, lateTool)) == 1 }, 0)
	if err != nil {
		t.Fatal(err)
	}
	result := results(view, lateTool)[0]
	if result.Value("isError") == true || text(result) != "late ok" {
		t.Fatalf("the first turn did not offer %s: isError=%v %q", lateTool, result.Value("isError"), text(result))
	}
}
