package subprocess

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// unsetMembersUI implements the optional interfaces that tell Pi's undefined from an empty string: [extension.StatusClearer], [extension.PlaceholderOmitter]
// and [extension.PrefillOmitter]. Every call, with the member it carried, lands in calls.
type unsetMembersUI struct {
	*mockUIContext
	calls []string
}

func (u *unsetMembersUI) SetStatus(key, text string) {
	u.calls = append(u.calls, "status:"+key+":"+text)
}
func (u *unsetMembersUI) ClearStatus(key string) { u.calls = append(u.calls, "status:"+key+":<unset>") }
func (u *unsetMembersUI) Input(_ context.Context, title, placeholder string, _ extension.ExtensionUIDialogOptions) (string, error) {
	u.calls = append(u.calls, "input:"+title+":"+placeholder)
	return "", nil
}
func (u *unsetMembersUI) InputWithoutPlaceholder(_ context.Context, title string, _ extension.ExtensionUIDialogOptions) (string, error) {
	u.calls = append(u.calls, "input:"+title+":<unset>")
	return "", nil
}
func (u *unsetMembersUI) Editor(_ context.Context, title, prefill string) (string, error) {
	u.calls = append(u.calls, "editor:"+title+":"+prefill)
	return "", nil
}
func (u *unsetMembersUI) EditorWithoutPrefill(_ context.Context, title string) (string, error) {
	u.calls = append(u.calls, "editor:"+title+":<unset>")
	return "", nil
}

// Pi's setStatus(key, undefined) and setStatus(key, "") are different requests in RPC mode. A call without `text` reaches a UIContext that can tell them apart as
// ClearStatus and an empty text as SetStatus(key, ""); a UIContext that cannot gets the empty text for both, as it did before.
func TestUIBridge_HandleCall_SetStatusTellsUndefinedFromEmpty(t *testing.T) {
	ui := &unsetMembersUI{mockUIContext: &mockUIContext{}}
	b := newTestBridge(ui)
	for _, args := range []string{`{"key":"k","text":""}`, `{"key":"k"}`, `{"key":"k","text":null}`, `{"key":"k","text":"v"}`} {
		if _, err := call(b, "ui.setStatus", args); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"status:k:", "status:k:<unset>", "status:k:<unset>", "status:k:v"}
	if !reflect.DeepEqual(ui.calls, want) {
		t.Fatalf("calls = %q, want %q", ui.calls, want)
	}

	plain := &mockUIContext{}
	if _, err := call(newTestBridge(plainWorkingUI{plain}), "ui.setStatus", `{"key":"k"}`); err != nil {
		t.Fatal(err)
	}
	if len(plain.statusCalls) != 1 || plain.statusCalls[0].key != "k" || plain.statusCalls[0].text != "" {
		t.Errorf("an absent text reached a plain UIContext as %+v, want one empty text", plain.statusCalls)
	}
}

// A status set before the UI is bound is replayed when it is, as the extension set it: an absent text replays as ClearStatus, an empty one as SetStatus.
func TestUIBridge_SetUIContextReplaysStatusesAsSet(t *testing.T) {
	first := &unsetMembersUI{mockUIContext: &mockUIContext{}}
	b := newTestBridge(first)
	for _, args := range []string{`{"key":"a","text":""}`, `{"key":"b"}`} {
		if _, err := call(b, "ui.setStatus", args); err != nil {
			t.Fatal(err)
		}
	}
	second := &unsetMembersUI{mockUIContext: &mockUIContext{}}
	b.SetUIContext(second)
	got := map[string]bool{}
	for _, c := range second.calls {
		got[c] = true
	}
	if len(second.calls) != 2 || !got["status:a:"] || !got["status:b:<unset>"] {
		t.Fatalf("replayed %q, want status:a: and status:b:<unset>", second.calls)
	}
}

// Pi's input(title, placeholder?) and editor(title, prefill?) leave the member undefined when the extension passes none. An absent member reaches a UIContext
// that can tell as InputWithoutPlaceholder / EditorWithoutPrefill, and an empty one as an empty argument; a UIContext that cannot gets the empty string for both.
func TestUIBridge_HandleCall_DialogsTellAbsentMembersFromEmptyOnes(t *testing.T) {
	ui := &unsetMembersUI{mockUIContext: &mockUIContext{}}
	b := newTestBridge(ui)
	for _, c := range []struct{ method, args string }{
		{"ui.input", `{"title":"a"}`},
		{"ui.input", `{"title":"b","placeholder":""}`},
		{"ui.input", `{"title":"c","placeholder":null}`},
		{"ui.input", `{"title":"d","placeholder":"p"}`},
		{"ui.editor", `{"title":"e"}`},
		{"ui.editor", `{"title":"f","prefill":""}`},
		{"ui.editor", `{"title":"g","prefill":"x"}`},
	} {
		if _, err := call(b, c.method, c.args); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"input:a:<unset>", "input:b:", "input:c:<unset>", "input:d:p", "editor:e:<unset>", "editor:f:", "editor:g:x"}
	if !reflect.DeepEqual(ui.calls, want) {
		t.Fatalf("calls = %q, want %q", ui.calls, want)
	}
}

type plainDialogUI struct {
	extension.UIContext
	calls []string
}

func (u *plainDialogUI) Input(_ context.Context, title, placeholder string, _ extension.ExtensionUIDialogOptions) (string, error) {
	u.calls = append(u.calls, "input:"+title+":"+placeholder)
	return "", nil
}
func (u *plainDialogUI) Editor(_ context.Context, title, prefill string) (string, error) {
	u.calls = append(u.calls, "editor:"+title+":"+prefill)
	return "", nil
}

func TestUIBridge_HandleCall_DialogsWithoutOmittersFallBackToEmpty(t *testing.T) {
	ui := &plainDialogUI{UIContext: &mockUIContext{}}
	b := newTestBridge(ui)
	for _, c := range []struct{ method, args string }{{"ui.input", `{"title":"a"}`}, {"ui.editor", `{"title":"e"}`}} {
		if _, err := call(b, c.method, c.args); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"input:a:", "editor:e:"}
	if !reflect.DeepEqual(ui.calls, want) {
		t.Fatalf("calls = %q, want %q", ui.calls, want)
	}
}
