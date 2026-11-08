package chat_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/chat"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/models"
	modelscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	statuscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/env/node"
	"github.com/MichaelKinsy/PiG/durable/harness"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

// A bare session worker host as Pi's chat tests build it (test/p2/shell.test.ts `shellHost`): a real Durable harness with
// the fake provider and a real local execution environment, the chat worker facet, principals over memory links.

var (
	owner = p2runtime.OwnerPrincipal{Access: p2runtime.AccessLocal}
	alice = p2runtime.GuestPrincipal{ID: "alice", Name: "Alice", Home: "s1", Sessions: map[string]p2runtime.GuestMode{"s1": p2runtime.GuestFull}, Revision: 1}
	bob   = p2runtime.GuestPrincipal{ID: "bob", Name: "Bob", Home: "s1", Sessions: map[string]p2runtime.GuestMode{"s1": p2runtime.GuestView}, Revision: 1}
	where = delta.JsonObjectOf("session", "s1", "conversation", float64(1))
)

// localEnvironments is the one local directory every conversation works in.
type localEnvironments struct {
	dir string
	env durableenv.ExecutionEnv
}

func (e localEnvironments) Session() p2runtime.Location { return p2runtime.Location{Cwd: e.dir} }
func (e localEnvironments) Locate(context.Context, durable.ConversationId, durable.DocumentReader) (p2runtime.Location, error) {
	return p2runtime.Location{Cwd: e.dir}, nil
}
func (e localEnvironments) At(context.Context, p2runtime.Location) (durableenv.ExecutionEnv, error) {
	return e.env, nil
}
func (e localEnvironments) Sandbox(context.Context, *string) (string, error) { return "", nil }

type sessionHost struct {
	*p2test.TestHost
	durable  harness.Harness
	dir      string
	liveness *p2runtime.Liveness
	// pushes counts the worker's status pushes.
	pushes *pushCounter
	// configured records `Sessions.configureSelf` calls.
	configured chan *delta.JsonObject
	// settings is the worker's settings.json value the models plugin would observe.
	settings *p2test.Cell
}

// newSessionHost opens the host with the chat facet. Settings is the worker's settings.json value (the owner's name).
func newSessionHost(t *testing.T, settings *delta.JsonObject, extra ...host.Facet) *sessionHost {
	t.Helper()
	p2test.NoLeakedGoroutines(t)
	dir := t.TempDir()
	fakeModels := models.CreateFakeModels(ai.CreateModelsOptions{})
	// Keep almost nothing verbatim, so a short conversation has something to compact.
	settingsOf := func() *harness.HarnessSettings {
		keep := 1
		return &harness.HarnessSettings{Compaction: &harness.CompactionPolicyPatch{KeepRecentTokens: &keep}}
	}
	opened, err := harness.OpenHarness(p2test.Background, mustStorage(t, filepath.Join(dir, "session.sqlite")), harness.HarnessOptions{
		Models: fakeModels, Registry: harness.CreateRegistry(), Settings: settingsOf,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close(p2test.Background) })
	if _, err := opened.Root(p2test.Background, &harness.RootOptions{Agent: &harness.AgentChange{
		Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"}), Cwd: harness.SetTo(dir),
	}}); err != nil {
		t.Fatal(err)
	}
	opened.Resume()
	fixture := &sessionHost{durable: opened, dir: dir, liveness: p2runtime.NewLiveness(), pushes: &pushCounter{}, configured: make(chan *delta.JsonObject, 16)}
	address := p2runtime.SessionKind.Address(delta.JsonObjectOf("id", "s1"))
	parts := &p2runtime.HarnessParts{
		Durable: func() (harness.Harness, error) { return opened, nil },
		Environments: func() (p2runtime.ExecutionEnvironments, error) {
			return localEnvironments{dir: dir, env: node.NewNodeExecutionEnv(node.NodeExecutionEnvOptions{Cwd: dir})}, nil
		},
		Relocate: func(context.Context, p2runtime.Location) error { return nil },
	}
	var created *p2test.TestHost
	worker := &p2runtime.WorkerParts{Liveness: fixture.liveness, Location: p2runtime.NewSessionLocation(p2runtime.Location{Cwd: dir})}
	// The models plugin's contribution: the harness configuration the chat worker reads the catalog from.
	configuration := host.DefineFacet(func(_ host.FacetHost, self host.FacetSelf) error {
		return p2runtime.HarnessConfig.Contribute(self, p2runtime.Entry("default", p2runtime.HarnessConfigEntry{
			Models: func() durable.Models { return fakeModels }, Settings: settingsOf,
		}))
	})
	facets := append([]host.Facet{p2test.WorkerFacet(parts, worker, func() host.Host { return created.Host }), configuration, chat.Facet()}, extra...)
	created = p2test.NewTestHost(t, p2test.TestHostOptions{
		Address: &address, Worker: true, Facets: facets,
		Stubs: []p2test.Stub{
			// No server here: the worker's status pushes go nowhere.
			{Contract: statuscontract.StatusReport, Factory: func(*delta.JsonObject, p2runtime.Principal, p2test.StubSelf) p2test.StubMembers {
				return p2test.StubMembers{Methods: map[string]p2test.StubMethod{"push": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
					fixture.pushes.add()
					return nil, nil
				}}}
			}},
			// No settings.json unless given: the owner's entries carry the default name.
			{Contract: modelscontract.WorkerModels, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
				fixture.settings = self.Cell(settings)
				return p2test.StubMembers{State: map[string]any{"settings": fixture.settings}}
			}},
		},
	})
	fixture.TestHost = created
	return fixture
}

func mustStorage(t *testing.T, path string) durable.Storage {
	t.Helper()
	storage, err := sqlitenode.OpenNodeSqliteStorage(path, sqlitenode.NodeSqliteStorageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return storage
}

// hold is a principal's reference to the chat of conversation 1, narrowed to variant.
func (f *sessionHost) hold(t *testing.T, principal p2runtime.Principal, variant string) *host.Reference {
	t.Helper()
	ref, err := p2test.HoldIn(f.Client(principal), chatcontract.Chat, where, variant)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ref.Release)
	return ref
}

// until is the first value of a state member that satisfies predicate.
func until(t *testing.T, source p2test.JSONSource, predicate func(delta.JsonValue) bool) delta.JsonValue {
	t.Helper()
	value, err := p2test.Until(source, predicate, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func call(ref *host.Reference, method string, args delta.JsonValue) (delta.JsonValue, error) {
	return ref.Call(method, args, p2test.Background)
}

func mustCall(t *testing.T, ref *host.Reference, method string, args delta.JsonValue) delta.JsonValue {
	t.Helper()
	result, err := call(ref, method, args)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	return result
}

func obj(value delta.JsonValue) *delta.JsonObject {
	object, _ := value.(*delta.JsonObject)
	return object
}

func list(value delta.JsonValue) []any {
	items, _ := value.([]any)
	return items
}

func text(value delta.JsonValue, key string) string {
	got, _ := obj(value).Value(key).(string)
	return got
}

// pushCounter counts the worker's status pushes.
type pushCounter struct{ n atomic.Int64 }

func (c *pushCounter) add()       { c.n.Add(1) }
func (c *pushCounter) count() int { return int(c.n.Load()) }

func marshal(t *testing.T, value any) string {
	t.Helper()
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(encoded.String(), "\n")
}
