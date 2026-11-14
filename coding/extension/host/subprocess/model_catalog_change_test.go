package subprocess

import (
	"bytes"
	"context"
	"crypto/sha256"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// catalogChangeRig is a bridge over a registry state the test rewrites and the replicas named: each connection's queue holds the catalog frames
// sent to it.
func catalogChangeRig(t *testing.T, names ...string) (*UIBridge, map[string]any, []*Conn) {
	t.Helper()
	bridge := NewUIBridge(nil)
	state := map[string]any{"models": []map[string]any{{"id": "first"}}}
	bridge.SetHostAction("getModelRegistryState", func() map[string]any { return state })
	conns := make([]*Conn, len(names))
	for i, name := range names {
		conns[i] = NewConn(name, nil)
		bridge.RegisterExtConn(name, conns[i], true)
		takeCatalogFrames(t, conns[i], 1, "registration of "+name)
	}
	return bridge, state, conns
}

// takeCatalogFrames takes the frames queued for conn and fails unless there are want of them.
func takeCatalogFrames(t *testing.T, conn *Conn, want int, when string) [][]byte {
	t.Helper()
	var frames [][]byte
	for len(conn.outCh) > 0 {
		frame := <-conn.outCh
		conn.endWork()
		frames = append(frames, frame.data)
	}
	if len(frames) != want {
		t.Fatalf("%s: %d catalog frames queued for %s, want %d", when, len(frames), conn.name, want)
	}
	return frames
}

// A publication that tells a commit, not a difference, is sent to a replica only when its bytes differ from the last catalog frame queued for
// that replica; a PublishModelCatalog reaches every replica, equal bytes or not, and the next change publication is measured against it.
func TestPublishModelCatalogChangeSendsNothingAReplicaHolds(t *testing.T) {
	bridge, state, conns := catalogChangeRig(t, "first", "second")
	bridge.PublishModelCatalog()
	for _, conn := range conns {
		takeCatalogFrames(t, conn, 1, "the publication after the registration")
	}
	for range 2 {
		bridge.PublishModelCatalogChange()
		for _, conn := range conns {
			takeCatalogFrames(t, conn, 0, "a change publication of the state the replica holds")
		}
	}
	state["models"] = []map[string]any{{"id": "first"}, {"id": "second"}}
	want, err := standardCatalogFrame(bridge)
	if err != nil {
		t.Fatal(err)
	}
	bridge.PublishModelCatalogChange()
	for _, conn := range conns {
		if got := takeCatalogFrames(t, conn, 1, "a change publication of a changed state")[0]; !bytes.Equal(got, want) {
			t.Fatalf("the changed state reached %s as %s, want %s", conn.name, got, want)
		}
	}
	bridge.PublishModelCatalogChange()
	for _, conn := range conns {
		takeCatalogFrames(t, conn, 0, "a second change publication of the changed state")
	}
	bridge.PublishModelCatalog()
	for _, conn := range conns {
		if got := takeCatalogFrames(t, conn, 1, "a PublishModelCatalog of the state the replica holds")[0]; !bytes.Equal(got, want) {
			t.Fatalf("the repeated state reached %s as %s, want %s", conn.name, got, want)
		}
	}
	bridge.PublishModelCatalogChange()
	for _, conn := range conns {
		takeCatalogFrames(t, conn, 0, "a change publication after the PublishModelCatalog")
	}
}

// The record is the connection's own. The frame a registration sends is not recorded (the runtime applies the ready frame's catalog after it), so
// the first change publication reaches a newly registered connection while an older one, which holds the bytes, is sent nothing.
func TestModelCatalogRecordBelongsToTheConnection(t *testing.T) {
	bridge, _, conns := catalogChangeRig(t, "first")
	first := conns[0]
	bridge.PublishModelCatalogChange()
	takeCatalogFrames(t, first, 1, "the first change publication after a registration")
	second := NewConn("second", nil)
	bridge.RegisterExtConn("second", second, true)
	takeCatalogFrames(t, second, 1, "registration of second")
	bridge.PublishModelCatalogChange()
	takeCatalogFrames(t, first, 0, "a change publication of the state first holds")
	takeCatalogFrames(t, second, 1, "the first change publication after second registered")
	bridge.PublishModelCatalogChange()
	takeCatalogFrames(t, second, 0, "a change publication of the state second holds")

	bridge.RegisterExtConn("first", first, true)
	takeCatalogFrames(t, first, 1, "second registration of first")
	bridge.PublishModelCatalogChange()
	takeCatalogFrames(t, first, 1, "the first change publication after first registered again")
	takeCatalogFrames(t, second, 0, "a change publication of the state second holds, after first registered again")
}

// A batch whose deferred publications were all change publications ends in one; a PublishModelCatalog among them makes the one publication reach
// every replica.
func TestBatchedModelCatalogPublicationKeepsItsKind(t *testing.T) {
	bridge, _, conns := catalogChangeRig(t, "first")
	conn := conns[0]
	bridge.PublishModelCatalog()
	takeCatalogFrames(t, conn, 1, "the publication after the registration")

	end := bridge.BatchModelCatalogPublication()
	bridge.PublishModelCatalogChange()
	bridge.PublishModelCatalogChange()
	end()
	takeCatalogFrames(t, conn, 0, "a batch of change publications of the state the replica holds")

	end = bridge.BatchModelCatalogPublication()
	bridge.PublishModelCatalogChange()
	bridge.PublishModelCatalog()
	bridge.PublishModelCatalogChange()
	end()
	takeCatalogFrames(t, conn, 1, "a batch with a PublishModelCatalog")

	end = bridge.BatchModelCatalogPublication()
	bridge.PublishModelCatalogChange()
	end()
	takeCatalogFrames(t, conn, 0, "a batch of change publications after a batch with a PublishModelCatalog")
}

// The result of an extension's refresh call carries the registry state, which its replica applies in place of the last catalog frame. The host
// cannot tell where among the frames that happens, so that replica is sent every publication from then on; another replica is not.
func TestRefreshResultEndsTheCatalogRecord(t *testing.T) {
	bridge, _, conns := catalogChangeRig(t, "caller", "other")
	bridge.SetHostAction("refreshModelRegistry", func(context.Context, *bool, []string, *bool) (map[string]any, error) {
		return map[string]any{"aborted": false, "errors": map[string]string{}}, nil
	})
	bridge.PublishModelCatalog()
	for _, conn := range conns {
		takeCatalogFrames(t, conn, 1, "the publication after the registration")
	}
	result, err := bridge.HandleCallFrom("caller", conns[0], &CallPayload{Method: "refreshModelRegistry"})
	if err != nil {
		t.Fatal(err)
	}
	var refreshed struct {
		State map[string]json.RawMessage `json:"state"`
	}
	if err := json.Unmarshal(result.Result, &refreshed); err != nil || refreshed.State["models"] == nil {
		t.Fatalf("refresh result carries no registry state: %v\n%s", err, result.Result)
	}
	for range 2 {
		bridge.PublishModelCatalogChange()
		takeCatalogFrames(t, conns[0], 1, "a change publication after the caller's refresh result")
		takeCatalogFrames(t, conns[1], 0, "a change publication of the state the other replica holds")
	}
}

// The digest is that of the frame's bytes, member for member.
func TestModelRegistryUpdateDigestIsOfTheFrame(t *testing.T) {
	for _, state := range []map[string]any{
		{},
		{"models": json.RawMessage(`[{"id":"a"}]`)},
		{"models": json.RawMessage(`[{"id":"a"}]`), "error": "error <detail>", "registered": []map[string]any{{"name": "p"}}, "typedModels": json.RawMessage(nil)},
	} {
		update, err := newModelRegistryUpdate(state)
		if err != nil {
			t.Fatal(err)
		}
		frame := update.encode()
		if got, want := update.digest(), modelCatalogDigest(sha256.Sum256(frame)); got != want {
			t.Fatalf("digest of %s = %x, want the frame's %x", frame, got, want)
		}
		if len(frame) != update.size {
			t.Fatalf("frame %s has %d bytes, sized %d", frame, len(frame), update.size)
		}
	}
}

// A change publication of the state the replica holds reads the state and hashes it; it builds no frame. The catalog here is the size of the
// one a start sends (about 1.5 MB): the publications allocate far less than one frame of it, and none is sent.
func TestModelCatalogChangeUnchangedBuildsNoFrame(t *testing.T) {
	bridge := NewUIBridge(nil)
	models := json.RawMessage("[" + string(bytes.Repeat([]byte(`{"id":"model","name":"a model name","cost":{"input":1.25,"output":2.5}},`), 21000)) + `{"id":"last"}]`)
	bridge.SetHostAction("getModelRegistryState", func() map[string]any { return map[string]any{"models": models} })
	conn := NewConn("test", nil)
	conn.modelRegistry = true
	bridge.extConns = map[string]*Conn{"test": conn}
	bridge.PublishModelCatalog()
	<-conn.outCh
	conn.endWork()
	const publications = 5
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range publications {
		bridge.PublishModelCatalogChange()
	}
	runtime.ReadMemStats(&after)
	if got := after.TotalAlloc - before.TotalAlloc; got > uint64(len(models)) {
		t.Fatalf("%d unchanged publications allocated %d bytes, more than one %d-byte catalog frame", publications, got, len(models))
	}
	if len(conn.outCh) != 0 {
		t.Fatal("an unchanged catalog was sent")
	}
}
