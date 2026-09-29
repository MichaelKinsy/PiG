package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi tui.ts:745-775: unfocus() without options restores focus, and unfocus({target}) focuses exactly target, including null or undefined. The Node handle names the target the host can resolve: null, the extension's editor component, or another of its mounted overlays.
func TestNodeOverlayUnfocusSendsExplicitTarget(t *testing.T) {
	nodeCellRequireNode(t)
	module, err := filepath.Abs("runtime-node/overlay-handle.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";
const { mountedOverlayHandle } = await import(pathToFileURL(%q));
const calls = [];
const other = { render: () => [] }, editor = { render: () => [] }, own = { render: () => [] };
const runtime = {
  customOverlays: new Map(),
  editorHost: { session: { component: editor } },
  callSync: (method, args) => { calls.push(args.target ?? args.action); return { hidden: false, focused: false, visible: true }; },
};
runtime.customOverlays.set("other", { key: "other", active: true, component: other });
const overlay = { key: "own", active: true, component: own, renderFrame() {} };
runtime.customOverlays.set("own", overlay);
const handle = mountedOverlayHandle(runtime, overlay, { hidden: false, focused: true, visible: true });
handle.unfocus();
handle.unfocus({ target: null });
handle.unfocus({});
handle.unfocus({ target: other });
handle.unfocus({ target: editor });
handle.unfocus({ target: own });
assert.throws(() => handle.unfocus({ target: { render: () => [] } }), /D73/);
assert.deepEqual(calls, ["unfocus", { kind: "null" }, { kind: "null" }, { kind: "overlay", key: "other" }, { kind: "editor" }, { kind: "overlay", key: "own" }]);
`, module)
	if out, err := exec.CommandContext(testbudget.Context(t), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("overlay unfocus: %v\n%s", err, out)
	}
}

type focusRecordingOverlay struct {
	extension.RemoteOverlayHandle
	focus []*extension.RemoteOverlayFocusTarget
}

func (o *focusRecordingOverlay) Control(_ context.Context, _ string, _ bool, focus *extension.RemoteOverlayFocusTarget) (extension.RemoteOverlayState, error) {
	o.focus = append(o.focus, focus)
	return extension.RemoteOverlayState{}, nil
}

// The host resolves a remote unfocus target to the mounted overlay of the same extension, the editor, or null; an unknown key fails instead of focusing nothing.
func TestUIBridgeResolvesOverlayUnfocusTarget(t *testing.T) {
	owner := &Conn{}
	own, other := &focusRecordingOverlay{}, &focusRecordingOverlay{}
	bridge := &UIBridge{customOverlays: map[string]extension.RemoteOverlayHandle{
		customOverlayOwnerPrefix("ext", owner) + "own":   &overlayProxy{target: own},
		customOverlayOwnerPrefix("ext", owner) + "other": &overlayProxy{target: other},
	}}
	for _, args := range []string{
		`{"key":"own","action":"unfocus"}`,
		`{"key":"own","action":"unfocus","target":{"kind":"null"}}`,
		`{"key":"own","action":"unfocus","target":{"kind":"editor"}}`,
		`{"key":"own","action":"unfocus","target":{"kind":"overlay","key":"other"}}`,
	} {
		if _, err := bridge.handleCustomControl(t.Context(), "ext", owner, json.RawMessage(args)); err != nil {
			t.Fatalf("%s: %v", args, err)
		}
	}
	want := []*extension.RemoteOverlayFocusTarget{nil, {}, {Editor: true}, {Overlay: other}}
	if !reflect.DeepEqual(own.focus, want) {
		t.Fatalf("focus targets = %#v, want %#v", own.focus, want)
	}
	if _, err := bridge.handleCustomControl(t.Context(), "ext", owner, json.RawMessage(`{"key":"own","action":"unfocus","target":{"kind":"overlay","key":"missing"}}`)); err == nil {
		t.Fatal("unknown overlay target was accepted")
	}
}
