package codingagent

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// A start refreshes the model catalogs right after it bound the model operations (RefreshModelCatalogs: models.json is reloaded, then the
// catalogs are refreshed), and each of the two steps notifies the registry's observers whether or not it changed anything. A replica is sent the
// catalog for the binding and nothing for a step that left it as it was: every frame is the whole catalog, which the extension's process decodes
// on the thread that answers the host. A step that changed the registry, and a provider another extension registers, still reach the replica.
func TestCatalogRefreshSendsAReplicaOnlyAChangedCatalog(t *testing.T) {
	t.Setenv("PIG_OFFLINE", "1")
	agentDir := t.TempDir()
	registry := NewModelRegistry(agentDir)
	server, client := net.Pipe()
	conn := subprocess.NewConn("catalog", server)
	conn.Start(t.Context())
	peer := newLivePeer(client)
	defer func() { peer.close(); _ = conn.Close("test done") }()
	bridge := subprocess.NewUIBridge(nil)
	bridge.RegisterExtConn("catalog", conn, true)
	detach := WireModelOperations(bridge, ModelOperationBindings{ModelCatalog: func(...string) []*ai.Model { return registry.GetAllModelData() }, Registry: registry})
	defer detach()

	fences := 0
	// sent returns the catalog frames the host queued since the last call.
	sent := func(when string, want int) [][]byte {
		t.Helper()
		fences++
		fence := fmt.Sprintf("test-catalog-fence-%d", fences)
		if err := conn.Send(&subprocess.Envelope{Type: subprocess.MsgNotify, Notify: &subprocess.NotifyPayload{Method: fence}}); err != nil {
			t.Fatal(err)
		}
		published, err := peer.untilNotify(fence)
		if err != nil {
			t.Fatal(err)
		}
		frames := make([][]byte, len(published))
		for i, frame := range published {
			if frame.kind() != "notify:model_registry_update" {
				t.Fatalf("%s: frame %d is %s, want a model_registry_update", when, i, frame.kind())
			}
			frames[i] = frame.raw
		}
		if len(frames) != want {
			t.Fatalf("%s: the replica was sent %d catalog frames, want %d", when, len(frames), want)
		}
		return frames
	}
	refresh := func() {
		t.Helper()
		if result, err := RefreshModelCatalogs(t.Context(), registry); err != nil || result.Aborted {
			t.Fatalf("catalog refresh: aborted=%v err=%v", result.Aborted, err)
		}
	}

	sent("registration and binding", 2)
	refresh()
	sent("a refresh that changed nothing", 0)

	models := `{"providers":{"late":{"baseUrl":"http://127.0.0.1:9/v1","api":"openai-completions","apiKey":"sk","models":[{"id":"late-model"}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	refresh()
	if frame := sent("a refresh that reloaded a changed models.json", 1)[0]; !bytes.Contains(frame, []byte(`"late-model"`)) {
		t.Fatal("the frame after the models.json change does not hold its model")
	}
	refresh()
	sent("a second refresh of the changed models.json", 0)

	withdraw := bridge.RecordProviderRegistration("other-extension", []byte(`{"baseUrl":"https://other.test/v1"}`))
	if frame := sent("a provider another extension registered", 1)[0]; !bytes.Contains(frame, []byte(`"other-extension"`)) {
		t.Fatal("the frame after the registration does not hold the provider")
	}
	withdraw()
	if frame := sent("the registration withdrawn", 1)[0]; bytes.Contains(frame, []byte(`"other-extension"`)) {
		t.Fatal("the frame after the withdrawal still holds the provider")
	}
	refresh()
	sent("a refresh after the withdrawal", 0)
}
