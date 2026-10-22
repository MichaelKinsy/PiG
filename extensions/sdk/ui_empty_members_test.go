// SPDX-License-Identifier: MIT

package sdk

import (
	"encoding/json"
	"reflect"
	"testing"
)

// Pi's RPC mode writes "" and [] where an extension passes them and omits `undefined`. A Go string cannot be undefined: SetStatus(key, "") is an empty text,
// ClearStatus(key) is the call without one, and an empty placeholder or prefill is the unset member, left off the wire.
func TestEmptyAndUnsetUIMembersStayDistinctOnTheWire(t *testing.T) {
	ext := New("surface")
	ext.Command("empties", "", func(ctx Context, _ string) error {
		ctx.SetStatus("k", "")
		ctx.ClearStatus("k")
		ctx.Notify("", "")
		ctx.SetTitle("")
		ctx.SetEditorText("")
		_, _, _ = ctx.Select("T", []string{})
		_, _ = ctx.Confirm("", "")
		_, _, _ = ctx.Input("T1", "")
		_, _, _ = ctx.Input("T2", "ph")
		_, _, _ = ctx.InputWithOptions("T3", "", DialogOptions{Timeout: 50})
		_, _, _ = ctx.Editor("E1", "")
		_, _, _ = ctx.Editor("E2", "pre")
		return nil
	})
	host, _, done := surfaceHost(t, ext, nil)
	calls, resp := runSurfaceCommand(t, host, "empties", func(call *callMsg) *callResultMsg {
		return &callResultMsg{Result: json.RawMessage(`{"ok":false}`)}
	})
	if resp.Error != nil {
		t.Fatalf("response = %+v", resp)
	}
	want := []struct {
		method string
		args   map[string]any
	}{
		{"ui.setStatus", map[string]any{"key": "k", "text": ""}},
		{"ui.setStatus", map[string]any{"key": "k"}},
		{"ui.notify", map[string]any{"message": "", "level": ""}},
		{"ui.setTitle", map[string]any{"title": ""}},
		{"ui.setEditorText", map[string]any{"text": ""}},
		{"ui.select", map[string]any{"title": "T", "options": []any{}}},
		{"ui.confirm", map[string]any{"title": "", "message": ""}},
		{"ui.input", map[string]any{"title": "T1"}},
		{"ui.input", map[string]any{"title": "T2", "placeholder": "ph"}},
		{"ui.input", map[string]any{"title": "T3", "opts": map[string]any{"timeout": float64(50)}}},
		{"ui.editor", map[string]any{"title": "E1"}},
		{"ui.editor", map[string]any{"title": "E2", "prefill": "pre"}},
	}
	if len(calls) != len(want) {
		t.Fatalf("got %d calls, want %d: %+v", len(calls), len(want), calls)
	}
	for i, w := range want {
		if calls[i].Method != w.method {
			t.Fatalf("call %d = %s, want %s", i, calls[i].Method, w.method)
		}
		if args := decodeArgs(t, calls[i].Args); !reflect.DeepEqual(args, w.args) {
			t.Fatalf("call %d (%s) args = %#v, want %#v", i, w.method, args, w.args)
		}
	}
	surfaceShutdown(t, host, done)
}
