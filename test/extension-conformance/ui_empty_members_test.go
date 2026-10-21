package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// emptyMembersUI records the dialog and fire-and-forget UI calls the host forwards, telling an empty member from an absent one: Pi's RPC mode writes `""` and
// `[]` as the extension passed them and omits `undefined` (rpc-mode.ts createExtensionUIContext), so the host UI must receive them apart. It implements the
// optional interfaces the host uses for that ([extension.StatusClearer], [extension.PlaceholderOmitter], [extension.PrefillOmitter]); every other member is the
// harness UI's own.
type emptyMembersUI struct {
	*recordingUI
	mu    sync.Mutex
	calls []string
}

func (u *emptyMembersUI) record(format string, args ...any) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls = append(u.calls, fmt.Sprintf(format, args...))
}

func (u *emptyMembersUI) recorded() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.calls...)
}

func (u *emptyMembersUI) SetStatus(key, text string) { u.record("status:%q:%q", key, text) }
func (u *emptyMembersUI) ClearStatus(key string)     { u.record("status:%q:<unset>", key) }
func (u *emptyMembersUI) Notify(message, level string) {
	// The host reports each dialog's ui_prompt scope through the notify hook; those are not the extension's calls.
	if strings.HasPrefix(message, "ui_prompt:") {
		return
	}
	u.record("notify:%q:%q", message, level)
}
func (u *emptyMembersUI) SetTitle(title string)     { u.record("title:%q", title) }
func (u *emptyMembersUI) SetEditorText(text string) { u.record("editorText:%q", text) }
func (u *emptyMembersUI) Select(_ context.Context, title string, options []string, _ extension.ExtensionUIDialogOptions) (string, error) {
	wire, _ := json.Marshal(options)
	if options == nil {
		wire = []byte("<unset>")
	}
	u.record("select:%q:%s", title, wire)
	return "second", nil
}
func (u *emptyMembersUI) Confirm(_ context.Context, title, message string, _ extension.ExtensionUIDialogOptions) (bool, error) {
	u.record("confirm:%q:%q", title, message)
	return true, nil
}
func (u *emptyMembersUI) Input(_ context.Context, title, placeholder string, _ extension.ExtensionUIDialogOptions) (string, error) {
	u.record("input:%q:%q", title, placeholder)
	return "typed", nil
}
func (u *emptyMembersUI) InputWithoutPlaceholder(_ context.Context, title string, _ extension.ExtensionUIDialogOptions) (string, error) {
	u.record("input:%q:<unset>", title)
	return "typed", nil
}
func (u *emptyMembersUI) Editor(_ context.Context, title, prefill string) (string, error) {
	u.record("editor:%q:%q", title, prefill)
	return "edited", nil
}
func (u *emptyMembersUI) EditorWithoutPrefill(_ context.Context, title string) (string, error) {
	u.record("editor:%q:<unset>", title)
	return "edited", nil
}

var (
	_ extension.StatusClearer      = (*emptyMembersUI)(nil)
	_ extension.PlaceholderOmitter = (*emptyMembersUI)(nil)
	_ extension.PrefillOmitter     = (*emptyMembersUI)(nil)
)

// Pi's RPC mode (rpc-mode.ts createExtensionUIContext) writes the empty string, the empty list and the like where an extension passes them, and omits only what the
// extension left undefined: setStatus(k, "") and setStatus(k, undefined), notify(""), setTitle(""), setEditorText(""), select("T", []), confirm("", ""), and an
// input/editor with or without a placeholder/prefill are all different requests. Every SDK's spelling of each reaches the host UI as the same call, in call order, with the host
// in RPC mode. The probe's values are empty strings and lists, which no SDK's default for a call it never makes can equal.
//
// A native SDK has no undefined string: Go and Rust spell an unset placeholder or prefill as the empty string (a language mechanic, not a divergence), so
// their empty placeholder is the unset one and their T2/E2 calls do not exist. Node and Python tell the two apart (undefined / None), and write "" as given. Go,
// Python and Rust remove a status with ClearStatus / clear_status, Node with setStatus(k, undefined).
func TestConformance_UIEmptyMembersAgreeAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	rpcRowMode = "rpc"
	t.Cleanup(func() { rpcRowMode = "" })
	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			ui := &emptyMembersUI{recordingUI: h.ui}
			h.bridge.SetUIContext(ui)
			h.bridge.SetNotifyFunc(ui.Notify)
			tellsEmptyFromUnset := strings.Contains(tc.name, "node") || strings.Contains(tc.name, "python")
			want := []string{
				`status:"k":""`,
				`status:"k":<unset>`,
				`notify:"":""`,
				`notify:"typed":"warning"`,
				`title:""`,
				`editorText:""`,
				`select:"T":[]`,
				`confirm:"":""`,
				`input:"T1":<unset>`,
			}
			if tellsEmptyFromUnset {
				want = append(want, `input:"T2":""`)
			}
			want = append(want, `input:"T3":"ph"`, `editor:"E1":<unset>`)
			if tellsEmptyFromUnset {
				want = append(want, `editor:"E2":""`)
			}
			want = append(want, `editor:"E3":"pre"`)
			if !h.runner.ExecuteCommand(context.Background(), "ui_wire_empties_probe", "") {
				t.Fatal("fixture has no ui_wire_empties_probe command")
			}
			var got []string
			pollUntilConformance(t, "ui_wire_empties_probe: calls never arrived ("+strconv.Itoa(len(want))+" expected)", func() bool {
				got = ui.recorded()
				return len(got) >= len(want)
			})
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("host UI calls = %q, want %q", got, want)
			}
		})
	}
}
