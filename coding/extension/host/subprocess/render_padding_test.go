package subprocess

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestRenderProxyOutputPadRefreshRejectsStaleGeneration(t *testing.T) {
	component, peer, _, invalidated := newRenderProxyRig(t)
	padded, ok := any(component).(interface{ SetOutputPad(int) })
	if !ok {
		t.Fatal("message renderer proxy cannot update outputPad")
	}
	padded.SetOutputPad(1)
	_ = component.Render(80)
	first := readFramed(t, peer)
	assertRenderPadding(t, first, false, 1)
	padded.SetOutputPad(0)
	component.SetExpanded(true)
	replyRender(t, peer, first, []string{"stale-padding"})
	second := readFramed(t, peer)
	assertRenderPadding(t, second, true, 0)
	if got := component.Render(80); !reflect.DeepEqual(got, []string{"[notice]"}) {
		t.Fatalf("stale padding published: %q", got)
	}
	replyRender(t, peer, second, []string{"unpadded"})
	waitForRenderProxy(t, func() bool { return invalidated.Load() > 0 })
	if got := component.Render(80); !reflect.DeepEqual(got, []string{"unpadded"}) {
		t.Fatalf("current padding = %q", got)
	}
	component.mu.Lock()
	generation := component.generation
	component.mu.Unlock()
	padded.SetOutputPad(0)
	component.mu.Lock()
	unchanged := component.generation == generation
	component.mu.Unlock()
	if !unchanged {
		t.Fatal("unchanged outputPad started another generation")
	}
	padded.SetOutputPad(1)
	third := readFramed(t, peer)
	assertRenderPadding(t, third, true, 1)
}

func TestEntryRenderProxyDoesNotSendOutputPad(t *testing.T) {
	component, peer, _, _ := newRenderProxyRig(t)
	component.method = "render_entry"
	component.payloadKey = "entry"
	component.SetExpanded(true)
	component.SetOutputPad(1)
	_ = component.Render(40)
	frame := readFramed(t, peer)
	var args struct {
		Options map[string]any `json:"options"`
	}
	if err := json.Unmarshal(frame.Request.Args, &args); err != nil {
		t.Fatal(err)
	}
	if want := (map[string]any{"expanded": true}); !reflect.DeepEqual(args.Options, want) {
		t.Fatalf("entry options = %v, want %v", args.Options, want)
	}
}

func assertRenderPadding(t *testing.T, frame Envelope, expanded bool, padding int) {
	t.Helper()
	var args struct {
		Options map[string]any `json:"options"`
	}
	if frame.Request == nil {
		t.Fatalf("not a render request: %+v", frame)
	}
	if err := json.Unmarshal(frame.Request.Args, &args); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"expanded": expanded, "outputPad": float64(padding)}
	if !reflect.DeepEqual(args.Options, want) {
		t.Fatalf("options = %v, want %v", args.Options, want)
	}
}
