package devtools

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
)

// Ports apps/p2/test/p2/dev-tools.test.ts.

// callAPI is a tool call's API in conversation conversation, whose run placed the inputs `data` (entry i+1 each); only what
// the requirement checks read is there.
type callAPI struct {
	durable.ToolExecutionApi
	conversation durable.ConversationId
	inputs       []inputEntry
}

type inputEntry struct {
	kind string
	data any
}

func person(kind string) inputEntry {
	return inputEntry{p2runtime.InputEntry, delta.JsonObjectOf("author", delta.JsonObjectOf("kind", kind, "id", kind, "name", kind), "text", "x", "attachments", []any{})}
}

var (
	owner = person("owner")
	guest = person("guest")
)

func (api callAPI) ConversationId() durable.ConversationId { return api.conversation }

func (api callAPI) SnapshotErased(_ context.Context, token durable.AnyDocToken, _ ...any) (durable.JsonObject, error) {
	if token.AnyDefinition().Kind == "pi.live" {
		ids := make([]any, len(api.inputs))
		for i := range api.inputs {
			ids[i] = float64(i + 1)
		}
		return delta.JsonObjectOf("run", delta.JsonObjectOf("inputs", ids)), nil
	}
	return nil, nil
}

func (api callAPI) Commit(_ context.Context, change func(tx durable.Tx) (any, error), _ ...durable.CommitOptions) (any, error) {
	return change(callTx{inputs: api.inputs})
}

type callTx struct {
	durable.Tx
	inputs []inputEntry
}

func (callTx) Submission(id durable.SubmissionId) (*durable.SubmissionRecord, error) {
	entry := durable.EntryId(id)
	return &durable.SubmissionRecord{Type: durable.SubmissionTypeInput, Entry: &entry}, nil
}

func (tx callTx) Entry(id durable.EntryId) (*durable.EntryRecord, error) {
	input := tx.inputs[id-1]
	return &durable.EntryRecord{Id: id, Kind: input.kind, Data: input.data}, nil
}

func run(t *testing.T, tool *durable.ToolRegistration, args map[string]any, api durable.ToolExecutionApi) durable.ToolExecutionResult {
	t.Helper()
	result, err := tool.Execute(p2test.Background, args, api)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func isError(result durable.ToolExecutionResult) bool {
	return result.IsError != nil && *result.IsError
}

func TestReloadPluginsAdmitsOnlyARootTurnTheOwnerStartedWorkingOnP2(t *testing.T) {
	calls := 0
	extension := NewExtension(func(_ context.Context, plugins []string) (p2runtime.DevResult, error) {
		calls++
		if plugins == nil {
			plugins = []string{"dirty"}
		}
		return p2runtime.DevResult{OK: true, Message: "Reloaded " + plugins[0]}, nil
	}, func() bool { return true })
	// As the worker's registry runs it: with its requirements checked.
	tool := p2runtime.GuardTool(extension.Tools[0])
	if tool.Name != "reload_plugins" || tool.Replay != durable.ReplayUnsafe || extension.Tools[0].Origin != "owner" || extension.Tools[0].Scope != "main" {
		t.Fatalf("tool = %+v %+v", tool, extension.Tools[0].ToolAccess)
	}
	if isError(run(t, tool, map[string]any{"plugins": []any{"chat"}}, callAPI{conversation: 1, inputs: []inputEntry{owner}})) || calls != 1 {
		t.Fatalf("the owner's turn: calls = %d", calls)
	}
	peer := func(origin bool) inputEntry {
		data := delta.JsonObjectOf("from", delta.JsonObjectOf("id", "peer", "title", "Peer"), "text", "x")
		if origin {
			data.Set("origin", "owner")
		}
		return inputEntry{p2runtime.PeerMessageEntry, data}
	}
	if isError(run(t, tool, map[string]any{}, callAPI{conversation: 1, inputs: []inputEntry{peer(true)}})) || calls != 2 {
		t.Fatalf("the owner's peer message: calls = %d", calls)
	}
	for _, inputs := range [][]inputEntry{{guest}, {owner, guest}, {peer(false)}, {{kind: "pi.user"}}, {}} {
		if !isError(run(t, tool, map[string]any{}, callAPI{conversation: 1, inputs: inputs})) {
			t.Errorf("inputs %v were admitted", inputs)
		}
	}
	if !isError(run(t, tool, map[string]any{}, callAPI{conversation: 2, inputs: []inputEntry{owner}})) {
		t.Error("a subagent's turn was admitted")
	}
	elsewhere := NewExtension(func(context.Context, []string) (p2runtime.DevResult, error) {
		t.Fatal("Non-p2 sessions cannot use the local channel")
		return p2runtime.DevResult{}, nil
	}, func() bool { return false })
	if !isError(run(t, p2runtime.GuardTool(elsewhere.Tools[0]), map[string]any{}, callAPI{conversation: 1, inputs: []inputEntry{owner}})) || calls != 2 {
		t.Fatalf("a session not on p2: calls = %d", calls)
	}
}

func TestReloadPluginsReturnsARefusalAsAnErrorResultWithoutReplayingIt(t *testing.T) {
	extension := NewExtension(func(context.Context, []string) (p2runtime.DevResult, error) {
		return p2runtime.DevResult{Message: "Reload refused: broken source"}, nil
	}, func() bool { return true })
	result := run(t, extension.Tools[0].ToolRegistration, map[string]any{}, callAPI{conversation: 1, inputs: []inputEntry{owner}})
	if !isError(result) || len(result.Output) != 1 {
		t.Fatalf("result = %+v", result)
	}
}

// serveDevChannel is a dev supervisor on directory's socket: execute answers each command.
func serveDevChannel(t *testing.T, directory string, execute func(p2runtime.DevCommand) p2runtime.DevResult) {
	t.Helper()
	listener, err := net.Listen("unix", filepath.Join(directory, "dev.sock"))
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body any
		_ = json.NewDecoder(r.Body).Decode(&body)
		command, err := p2runtime.ParseDevCommand(body)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "message": err.Error()})
			return
		}
		result := execute(command)
		_ = json.NewEncoder(w).Encode(result)
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
}

// checkout is the p2 app directory the sessions below work on or beside.
func checkout(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func devToolsHost(t *testing.T, dataDir, cwd string) *p2test.TestHost {
	t.Helper()
	worker := &p2runtime.WorkerParts{
		Init:     p2runtime.WorkerInit{App: checkout(t), DataDir: dataDir, Fake: true, Generation: 1},
		Location: p2runtime.NewSessionLocation(p2runtime.Location{Cwd: cwd}),
	}
	address := p2runtime.SessionKind.Address(delta.JsonObjectOf("id", "fixture"))
	return p2test.NewTestHost(t, p2test.TestHostOptions{
		Address: &address, Worker: true,
		Facets: []host.Facet{p2test.WorkerFacet(&p2runtime.HarnessParts{}, worker, nil), Facet()},
	})
}

func extensionsOf(t *testing.T, fixture *p2test.TestHost) *p2runtime.P2Extension {
	t.Helper()
	var found *p2runtime.P2Extension
	if err := p2test.Poll(func() (bool, error) {
		// Read inside a section of the host's loop, where contributions change.
		source, err := fixture.Host.Registry(p2test.Background, p2runtime.Extensions.Registry, host.GetOptions{})
		if err != nil {
			return false, nil
		}
		table, tableErr := p2runtime.Extensions.Table(source)
		ok := tableErr == nil
		if !ok {
			return false, nil
		}
		found, _ = table.Get("p2-dev-tools")
		return found != nil, nil
	}, 0, "the p2-dev-tools extension"); err != nil {
		t.Fatal(err)
	}
	return found
}

func TestWorkerFacetRegistersReloadPluginsAndTheOwnerToolUsesOnlyItsOwnPrivateDataDirectoryChannel(t *testing.T) {
	directory, err := os.MkdirTemp("", "p2-dev-tool-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	calls := 0
	serveDevChannel(t, directory, func(command p2runtime.DevCommand) p2runtime.DevResult {
		calls++
		if command.Action != "reload" || !slices.Equal(command.Plugins, []string{"chat"}) {
			t.Errorf("command = %+v", command)
		}
		return p2runtime.DevResult{OK: true, Message: "Reloaded chat"}
	})
	extension := extensionsOf(t, devToolsHost(t, directory, checkout(t)))
	tool := p2runtime.GuardTool(extension.Tools[0])
	if isError(run(t, tool, map[string]any{"plugins": []any{"chat"}}, callAPI{conversation: 1, inputs: []inputEntry{owner}})) || calls != 1 {
		t.Fatalf("owner: calls = %d", calls)
	}
	if !isError(run(t, tool, map[string]any{}, callAPI{conversation: 1, inputs: []inputEntry{guest}})) || calls != 1 {
		t.Fatalf("guest: calls = %d", calls)
	}
}

func TestReloadPluginsIsOfferedOnlyInDevModeForASessionOnThisP2Checkout(t *testing.T) {
	offered := func(dev bool, cwd string) bool {
		t.Helper()
		if dev {
			t.Setenv("P2_DEV", "1")
		} else {
			t.Setenv("P2_DEV", "")
		}
		fixture := devToolsHost(t, t.TempDir(), cwd)
		time.Sleep(20 * time.Millisecond)
		source, err := fixture.Host.Registry(p2test.Background, p2runtime.ExtensionSwitches.Registry, host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		table, tableErr := p2runtime.ExtensionSwitches.Table(source)
		ok := tableErr == nil
		if !ok {
			t.Fatalf("switches: %v", tableErr)
		}
		on, _ := table.Get("p2-dev-tools")
		return on != nil && *on
	}
	if !offered(true, checkout(t)) {
		t.Error("dev mode on the checkout: not offered")
	}
	if offered(false, checkout(t)) {
		t.Error("not dev mode: offered")
	}
	if offered(true, t.TempDir()) {
		t.Error("another directory: offered")
	}
}
