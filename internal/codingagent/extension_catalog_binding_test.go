package codingagent

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
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
	bridge.RegisterExtConn("catalog", conn)
	var target ModelOperationBridge = bridge
	if legacy {
		target = legacyModelCatalogBridge{bridge}
	}
	detach := WireModelOperations(target, ModelOperationBindings{ModelCatalog: catalog, Registry: registry})
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
		if err := registry.RegisterProvider(provider, extension.ProviderConfig{API: ai.APIOpenAICompletions, BaseURL: "https://example.test/v1", Models: []extension.ProviderModelConfig{{ID: "second"}, {ID: "first"}}}); err != nil {
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
