package extensionconformance

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// dialogValuesUI answers each dialog with the JSON its title names, as Pi's RPC mode resolves a dialog with whatever JSON the client sent
// (rpc-mode.ts:137-150 `"value" in r ? r.value : undefined`, `"confirmed" in r ? r.confirmed : false`). It implements [extension.DialogValueUIContext]; the
// string-typed methods are never reached for these calls.
type dialogValuesUI struct {
	*recordingUI
	mu       sync.Mutex
	notified []string
}

var dialogReplies = map[string]string{
	"S1": `5`, "S2": `null`, "S3": `{"a":[1,"x"]}`, "S4": ``, // empty: undefined
	"C1": `"yes"`, "C2": `null`, "C3": `0`, "C4": `false`,
	"I1": `[1,2]`, "I2": ``,
	"E1": `7.5`, "E2": `true`,
}

func (u *dialogValuesUI) reply(title string) (json.RawMessage, error) {
	if reply := dialogReplies[title]; reply != "" {
		return json.RawMessage(reply), nil
	}
	return nil, context.Canceled
}

func (u *dialogValuesUI) SelectValue(_ context.Context, title string, _ []string, _ extension.ExtensionUIDialogOptions) (json.RawMessage, error) {
	return u.reply(title)
}
func (u *dialogValuesUI) ConfirmValue(_ context.Context, title, _ string, _ extension.ExtensionUIDialogOptions) (json.RawMessage, error) {
	return u.reply(title)
}
func (u *dialogValuesUI) InputValue(_ context.Context, title string, _ *string, _ extension.ExtensionUIDialogOptions) (json.RawMessage, error) {
	return u.reply(title)
}
func (u *dialogValuesUI) EditorValue(_ context.Context, title string, _ *string) (json.RawMessage, error) {
	return u.reply(title)
}

func (u *dialogValuesUI) Notify(message, level string) {
	// The host reports each dialog's ui_prompt scope through the notify hook; those are not the extension's calls.
	if strings.HasPrefix(message, "ui_prompt:") {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.notified = append(u.notified, message)
}

func (u *dialogValuesUI) notifications() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.notified...)
}

var _ extension.DialogValueUIContext = (*dialogValuesUI)(nil)

// Pi's RPC mode resolves a dialog with whatever JSON the client's extension_ui_response carries, so select(), input() and editor() can return a number, null, an
// object, an array or a boolean, and confirm() returns the client's `confirmed` as sent. Every SDK hands the extension that value, with undefined (ok false) apart from
// null. The sentinels are non-string values (and `null`, `0` and `false` for confirm) that no SDK's default for a dialog (an empty string, undefined, false) can equal.
func TestConformance_UIDialogValuesAgreeAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	want := []string{
		`S1=5`, `S2=null`, `S3={"a":[1,"x"]}`, `S4=<undefined>`,
		`C1="yes"`, `C2=null`, `C3=0`, `C4=false`,
		`I1=[1,2]`, `I2=<undefined>`,
		`E1=7.5`, `E2=true`,
	}
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
			ui := &dialogValuesUI{recordingUI: h.ui}
			h.bridge.SetUIContext(ui)
			h.bridge.SetNotifyFunc(ui.Notify)
			if !h.runner.ExecuteCommand(context.Background(), "ui_dialog_values_probe", "") {
				t.Fatal("fixture has no ui_dialog_values_probe command")
			}
			var got []string
			pollUntilConformance(t, "ui_dialog_values_probe: reports never arrived", func() bool {
				got = ui.notifications()
				return len(got) >= len(want)
			})
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("dialog results = %q, want %q", got, want)
			}
		})
	}
}
