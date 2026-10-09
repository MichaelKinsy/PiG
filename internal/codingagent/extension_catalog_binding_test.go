package codingagent

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// This adapter deliberately exposes only the original binding contract.
type legacyModelCatalogBridge struct{ bridge *subprocess.UIBridge }

func (b legacyModelCatalogBridge) SetHostAction(key string, fn any) { b.bridge.SetHostAction(key, fn) }
func (b legacyModelCatalogBridge) PublishModelCatalog()             { b.bridge.PublishModelCatalog() }

func captureModelPublications(t testing.TB, legacy bool, catalog func() []*ai.Model, registry *ModelRegistry) [][]byte {
	t.Helper()
	server, client := net.Pipe()
	conn := subprocess.NewConn("catalog", server)
	conn.Start(t.Context())
	defer func() { _ = client.Close(); _ = conn.Close("test done") }()
	bridge := subprocess.NewUIBridge(nil)
	bridge.RegisterExtConn("catalog", conn, true)
	var target ModelOperationBridge = bridge
	if legacy {
		target = legacyModelCatalogBridge{bridge}
	}
	detach := WireModelOperations(target, ModelOperationBindings{ModelCatalog: func(...string) []*ai.Model { return catalog() }, Registry: registry})
	defer detach()
	if err := conn.Send(&subprocess.Envelope{Type: subprocess.MsgNotify, Notify: &subprocess.NotifyPayload{Method: "test-catalog-fence"}}); err != nil {
		t.Fatal(err)
	}
	var frames [][]byte
	for {
		var header [4]byte
		if _, err := io.ReadFull(client, header[:]); err != nil {
			t.Fatal(err)
		}
		frame := make([]byte, binary.BigEndian.Uint32(header[:]))
		if _, err := io.ReadFull(client, frame); err != nil {
			t.Fatal(err)
		}
		var envelope subprocess.Envelope
		if err := json.Unmarshal(frame, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Notify != nil && envelope.Notify.Method == "test-catalog-fence" {
			break
		}
		frames = append(frames, frame)
	}
	// Registration plus the three existing binding publications, including an unchanged final snapshot.
	if len(frames) != 4 {
		t.Fatalf("got %d frames before the queue fence; want registration plus three publications", len(frames))
	}
	return frames
}

func TestModelCatalogBindingKeepsRegistrationOrdering(t *testing.T) {
	registry := NewModelRegistry(t.TempDir())
	for _, provider := range []string{"z-last", "a-first"} {
		if err := registry.RegisterExtensionProvider(provider, extension.ProviderConfig{API: ai.APIOpenAICompletions, BaseURL: "https://example.test/v1", Models: []extension.ProviderModelConfig{{ID: "second"}, {ID: "first"}}}); err != nil {
			t.Fatal(err)
		}
	}
	want := captureModelPublications(t, true, registry.GetAllModelData, registry)
	got := captureModelPublications(t, false, registry.GetAllModelData, registry)
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("publication %d changed provider/model ordering", i)
		}
	}
	// The initial value getter uses catalog order; the full registry state applies registration order.
	firstA := bytes.Index(got[1], []byte(`"provider":"a-first"`))
	firstZ := bytes.Index(got[1], []byte(`"provider":"z-last"`))
	fullA := bytes.Index(got[2], []byte(`"provider":"a-first"`))
	fullZ := bytes.Index(got[2], []byte(`"provider":"z-last"`))
	if firstA < 0 || firstZ < firstA || fullZ < 0 || fullA < fullZ {
		t.Fatal("fixture did not distinguish catalog and registration ordering")
	}
}

func TestModelCatalogBindingKeepsPublicationBytesAndCalls(t *testing.T) {
	for _, kind := range []string{"ordinary", "opaque", "signed-zero", "changing"} {
		t.Run(kind, func(t *testing.T) {
			registry := NewModelRegistry(t.TempDir())
			run := func(legacy bool) ([][]byte, int, int) {
				reads, marshals := 0, 0
				model := &ai.Model{ID: "model", ProviderMeta: ai.ProviderMetadata{ProviderID: "custom", BaseURL: "https://example.test/<escaped>"}, Input: []string{}, SamplingParams: map[string]any{}}
				if kind == "opaque" {
					model.SamplingParams["value"] = catalogCountingValue{calls: &marshals}
				}
				getter := func() []*ai.Model {
					reads++
					if kind == "changing" {
						model.DisplayName = string(rune('a' + reads))
					}
					if kind == "signed-zero" && reads == 2 {
						model.Capabilities.InputCostPer1M = -1
						model.Capabilities.InputCostPer1M *= 0
					}
					return []*ai.Model{model}
				}
				frames := captureModelPublications(t, legacy, getter, registry)
				return frames, reads, marshals
			}
			want, wantReads, wantMarshals := run(true)
			got, gotReads, gotMarshals := run(false)
			if gotReads != wantReads || gotMarshals != wantMarshals {
				t.Fatalf("callbacks reads=%d/%d marshals=%d/%d", gotReads, wantReads, gotMarshals, wantMarshals)
			}
			for i := range want {
				if !bytes.Equal(got[i], want[i]) {
					t.Fatalf("publication %d differs:\ngot %s\nwant%s", i, got[i], want[i])
				}
			}
			for _, frame := range got {
				if !json.Valid(frame) {
					t.Fatal("invalid notification JSON")
				}
			}
		})
	}
}

// replicaRegistry is a real ModelRegistry carrying one extension-registered
// model next to the built-in catalog, and a ModelCatalog binding that counts
// how often the host builds the catalog from it.
func replicaRegistry(t *testing.T) (*ModelRegistry, ModelOperationBindings, *atomic.Int32) {
	t.Helper()
	registry := NewModelRegistry(t.TempDir())
	if err := registry.RegisterExtensionProvider("replica-provider", extension.ProviderConfig{API: ai.APIOpenAICompletions, BaseURL: "https://replica.test/v1", Models: []extension.ProviderModelConfig{{ID: "replica-model"}}}); err != nil {
		t.Fatal(err)
	}
	builds := &atomic.Int32{}
	return registry, ModelOperationBindings{Registry: registry, ModelCatalog: func(...string) []*ai.Model {
		builds.Add(1)
		return registry.GetAllModelData()
	}}, builds
}

// registryUpdates counts the model_registry_update notifies queued on conn
// before a fence, reading its peer end.
func registryUpdates(t *testing.T, conn *subprocess.Conn, peer net.Conn) int {
	t.Helper()
	if err := conn.Send(&subprocess.Envelope{Type: subprocess.MsgNotify, Notify: &subprocess.NotifyPayload{Method: "test-registry-fence"}}); err != nil {
		t.Fatal(err)
	}
	updates := 0
	for {
		var header [4]byte
		if _, err := io.ReadFull(peer, header[:]); err != nil {
			t.Fatal(err)
		}
		frame := make([]byte, binary.BigEndian.Uint32(header[:]))
		if _, err := io.ReadFull(peer, frame); err != nil {
			t.Fatal(err)
		}
		var envelope subprocess.Envelope
		if err := json.Unmarshal(frame, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Notify == nil {
			continue
		}
		switch envelope.Notify.Method {
		case "test-registry-fence":
			return updates
		case "model_registry_update":
			updates++
		}
	}
}

// pig additive (D19): only a runtime that declared
// RegisterPayload.WantsModelRegistry replicates the registry. Wiring the
// Session's model operations publishes the catalog three times; a connection
// that reads getModelRegistryState on use receives none of it, and while only
// such connections are registered the host never builds the catalog.
func TestModelCatalogReachesOnlyRegistryReplicas(t *testing.T) {
	_, bindings, builds := replicaRegistry(t)
	bridge := subprocess.NewUIBridge(func() {})
	open := func(name string, replicates bool) (*subprocess.Conn, net.Conn) {
		server, client := net.Pipe()
		conn := subprocess.NewConn(name, server)
		conn.Start(t.Context())
		t.Cleanup(func() { _ = client.Close(); _ = conn.Close("test done") })
		bridge.RegisterExtConn(name, conn, replicates)
		return conn, client
	}
	reader, readerPeer := open("reader", false)
	detach := WireModelOperations(bridge, bindings)
	defer detach()
	if got := builds.Load(); got != 0 {
		t.Fatalf("catalog builds with only an on-use reader registered = %d, want 0", got)
	}
	if got := registryUpdates(t, reader, readerPeer); got != 0 {
		t.Fatalf("on-use reader received %d registry snapshots while model operations were wired, want 0", got)
	}

	replica, replicaPeer := open("replica", true)
	bridge.PublishModelCatalog()
	if got := registryUpdates(t, replica, replicaPeer); got != 2 {
		t.Fatalf("replica received %d registry snapshots, want its bootstrap plus one publication", got)
	}
	if got := registryUpdates(t, reader, readerPeer); got != 0 {
		t.Fatalf("on-use reader received %d registry snapshots, want 0", got)
	}
	if builds.Load() == 0 {
		t.Fatal("the replica's snapshots were not built from the registry")
	}
}

// pig additive (D19): the Go, Rust and Python SDKs answer ctx.modelRegistry by
// calling getModelRegistryState when an extension reads it, so they never
// declare RegisterPayload.WantsModelRegistry. A Piglet Binary paid for the
// whole catalog (more than 1 MB of JSON) in the ready payload and three
// publications on its startup path for a fused Go extension that discarded
// every copy. Loading such an extension with the Session's model operations
// wired builds no catalog, while the extension's own read still returns the
// host's registry.
func TestGoSDKExtensionStartsWithoutModelRegistrySnapshot(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	_, bindings, builds := replicaRegistry(t)
	bridge := subprocess.NewUIBridge(func() {})
	detach := WireModelOperations(bridge, bindings)
	defer detach()
	found := make(chan map[string]any, 1)
	ext := sdk.New("fused-registry")
	ext.Command("read", "read the model registry", func(ctx sdk.Context, _ string) error {
		models, err := ctx.ModelRegistry().GetAll()
		var model map[string]any
		for _, candidate := range models {
			if candidate["provider"] == "replica-provider" && candidate["id"] == "replica-model" {
				model = candidate
			}
		}
		found <- model
		return err
	})
	host := subprocess.NewHost(t.TempDir())
	host.SetUIBridge(bridge)
	defer host.Shutdown("test done")
	loaded, err := host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: "fused-registry", Enabled: true}, ext.RunWithConn)
	if err != nil {
		t.Fatal(err)
	}
	bridge.PublishModelCatalog()
	if got := builds.Load(); got != 0 {
		t.Fatalf("catalog builds for a Go SDK extension during startup = %d, want 0", got)
	}

	if err := loaded.Commands["read"].Handler(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	if model := <-found; model == nil || model["provider"] != "replica-provider" || model["id"] != "replica-model" {
		t.Fatalf("ctx.modelRegistry.getAll() lookup of replica-provider/replica-model = %v, want the host's registered model", model)
	}
	if got := builds.Load(); got != 1 {
		t.Fatalf("catalog builds for one read = %d, want 1", got)
	}
}

// pig additive (D19): a Node member answers ctx.modelRegistry from the snapshot
// the host replicates into it. A Node cell defers each member's ready until
// every factory has finished, so the member receives RegisterExtConn's
// model_registry_update first; the deferred ready payload then replaces the
// replica with its models and must carry the catalog, or the member reads an
// empty registry until the next publication (a reload re-activates a cell with
// model operations already wired).
func TestNodeModelRegistryReplicaSurvivesDeferredReady(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required: %v", err)
	}
	for _, count := range []int{1, 2} {
		t.Run(map[int]string{1: "one-member", 2: "two-members"}[count], func(t *testing.T) {
			t.Setenv("PIG_HOME", t.TempDir())
			_, bindings, _ := replicaRegistry(t)
			bridge := subprocess.NewUIBridge(func() {})
			detach := WireModelOperations(bridge, bindings)
			defer detach()
			root := t.TempDir()
			var configs []subprocess.ExtConfig
			for i := range count {
				name := fmt.Sprintf("replica%d", i)
				entry := filepath.Join(root, name+".mjs")
				source := fmt.Sprintf(`export default function(pi) {
 pi.registerTool({name:%q,label:"probe",description:"probe",parameters:{type:"object",properties:{}},execute:async(_id,_args,_signal,_update,ctx)=>{
  const model=ctx.modelRegistry.find("replica-provider","replica-model");
  return {content:[{type:"text",text:JSON.stringify({pid:process.pid,model:model?model.provider+"/"+model.id:null})}]};
 }});
}`, name)
				if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
					t.Fatal(err)
				}
				configs = append(configs, subprocess.ExtConfig{Name: name, Source: entry, Enabled: true, RuntimeKind: "subprocess", RuntimeLanguage: "node", EntrypointKind: "factory", SDKName: "pi-node"})
			}
			host := subprocess.NewHost(t.TempDir())
			host.SetUIBridge(bridge)
			t.Cleanup(func() { host.Shutdown("test done") })
			exts, errs := host.LoadAll(t.Context(), configs)
			if len(errs) > 0 {
				t.Fatal(errs)
			}
			pids := map[float64]bool{}
			tools := map[string]extension.RegisteredTool{}
			for _, ext := range exts {
				maps.Copy(tools, ext.Tools)
			}
			for i := range count {
				name := fmt.Sprintf("replica%d", i)
				tool, ok := tools[name]
				if !ok {
					t.Fatalf("%s did not register its probe tool", name)
				}
				result, err := tool.Definition.Execute(t.Context(), "probe", json.RawMessage(`{}`), nil)
				if err != nil {
					t.Fatal(err)
				}
				var got struct {
					PID   float64 `json:"pid"`
					Model *string `json:"model"`
				}
				if err := json.Unmarshal([]byte(result.Text()), &got); err != nil {
					t.Fatal(err)
				}
				pids[got.PID] = true
				if got.Model == nil || *got.Model != "replica-provider/replica-model" {
					t.Fatalf("%s ctx.modelRegistry.find(replica-provider, replica-model) = %v, want the host's registered model", name, got.Model)
				}
			}
			if len(exts) != count || len(pids) != 1 {
				t.Fatalf("%d members ran in %d Node processes, want %d in 1", len(exts), len(pids), count)
			}
		})
	}
}
