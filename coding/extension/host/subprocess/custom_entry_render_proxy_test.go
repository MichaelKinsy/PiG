package subprocess

// pi: packages/coding-agent/src/modes/interactive/components/custom-entry.ts

import (
	"encoding/json"
	"net"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// custom-entry.ts CustomEntryComponent asks the registered entry renderer for the component of the whole session entry with the
// expand state, and shows nothing but that component. A subprocess extension's renderer is reached over the wire: the host hands
// the transcript a proxy that paints a placeholder at once, sends one render_entry request carrying the entry, the options and the
// width, paints the answer, and asks again with the new expand state when the entry is toggled.
func TestCustomEntryRendererProxyRendersTheEntryOverTheWire(t *testing.T) {
	hostEnd, peer := net.Pipe()
	conn := NewConn("custom-entry", hostEnd)
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	me := withConn(&managedExt{config: ExtConfig{Name: "custom-entry"}, host: h}, conn)
	ctx := t.Context()
	conn.Start(ctx)
	t.Cleanup(func() {
		_ = peer.Close()
		_ = hostEnd.Close()
		<-conn.done
	})

	parent := "parent-1"
	entry := extension.CustomEntry{
		Type: "custom", ID: "entry-1", ParentID: &parent, Timestamp: "2026-01-01T00:00:00.000Z",
		CustomType: "note",
		Data:       map[string]any{"text": "hi"},
	}
	component := h.makeEntryRenderer(me, "note")(entry, extension.EntryRenderOptions{}, nil)

	if got := component.Render(80); len(got) != 1 || got[0] != "[note]" {
		t.Fatalf("first paint = %#v, want the placeholder until the extension answers", got)
	}
	request := readFramed(t, peer)
	if request.Request == nil || request.Request.Method != "render_entry" || request.Request.Tool != "note" {
		t.Fatalf("request = %+v, want render_entry for note", request.Request)
	}
	var args struct {
		Entry   extension.CustomEntry        `json:"entry"`
		Options extension.EntryRenderOptions `json:"options"`
		Width   int                          `json:"width"`
	}
	if err := json.Unmarshal(request.Request.Args, &args); err != nil {
		t.Fatal(err)
	}
	if args.Entry.ID != "entry-1" || args.Entry.ParentID == nil || *args.Entry.ParentID != parent || args.Entry.Timestamp != entry.Timestamp ||
		args.Entry.CustomType != "note" || args.Options.Expanded || args.Width != 80 {
		t.Fatalf("args = %+v, want the whole entry, collapsed options and width 80", args)
	}
	replyRender(t, peer, request, []string{"entry:hi:expanded=false"})
	waitForRenderProxy(t, func() bool {
		lines := component.Render(80)
		return len(lines) == 1 && lines[0] == "entry:hi:expanded=false"
	})

	toggler, ok := component.(interface{ SetExpanded(bool) })
	if !ok {
		t.Fatal("the entry component cannot be expanded")
	}
	toggler.SetExpanded(true)
	second := readFramed(t, peer)
	if err := json.Unmarshal(second.Request.Args, &args); err != nil {
		t.Fatal(err)
	}
	if !args.Options.Expanded {
		t.Fatal("the expand toggle asked the extension with collapsed options")
	}
	replyRender(t, peer, second, []string{"entry:hi:expanded=true"})
	waitForRenderProxy(t, func() bool {
		lines := component.Render(80)
		return len(lines) == 1 && lines[0] == "entry:hi:expanded=true"
	})
}
