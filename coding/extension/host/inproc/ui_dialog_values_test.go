package inproc_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// valueUI is a bound UI whose dialogs resolve with the JSON a client sent (Pi's RPC mode), and records the placeholder and prefill it received.
type valueUI struct {
	extension.UIContext
	reply                      json.RawMessage
	err                        error
	sawPlaceholder, sawPrefill *string
}

func (u *valueUI) SelectValue(context.Context, string, []string, extension.ExtensionUIDialogOptions) (json.RawMessage, error) {
	return u.reply, u.err
}
func (u *valueUI) ConfirmValue(context.Context, string, string, extension.ExtensionUIDialogOptions) (json.RawMessage, error) {
	return u.reply, u.err
}
func (u *valueUI) InputValue(_ context.Context, _ string, placeholder *string, _ extension.ExtensionUIDialogOptions) (json.RawMessage, error) {
	u.sawPlaceholder = placeholder
	return u.reply, u.err
}
func (u *valueUI) EditorValue(_ context.Context, _ string, prefill *string) (json.RawMessage, error) {
	u.sawPrefill = prefill
	return u.reply, u.err
}

// An in-process extension sees the same dialog results as an SDK extension: the UI the runner hands it is an [extension.DialogValueUIContext] that returns the bound UI's
// raw JSON (a number, null, an object, confirm's own value), keeps undefined (context.Canceled) apart from null, passes an unset placeholder or prefill as nil, and still
// reports each prompt. mutation-checked: a wrapper that answers through the string-typed methods fails every case.
func TestUIPrompt_InProcessExtensionsGetRawDialogValues(t *testing.T) {
	for _, test := range []struct {
		name  string
		reply string
		err   error
		want  string // "" = undefined
	}{
		{"number", `5`, nil, `5`},
		{"null", `null`, nil, `null`},
		{"object", `{"a":[1,"x"]}`, nil, `{"a":[1,"x"]}`},
		{"undefined", ``, context.Canceled, ``},
	} {
		t.Run(test.name, func(t *testing.T) {
			rec := newPromptRecorder()
			r := inproc.NewRunner([]extension.Extension{promptExtension("a", rec)}, ".")
			bound := &valueUI{UIContext: extension.NoopUIContext, reply: json.RawMessage(test.reply), err: test.err}
			r.SetUIContext(bound, extension.ModeRPC)
			values, ok := runnerUI(t, r).(extension.DialogValueUIContext)
			if !ok {
				t.Fatal("the UI handed to an in-process extension is not an extension.DialogValueUIContext")
			}
			check := func(method string, got json.RawMessage, err error) {
				t.Helper()
				if test.want == "" {
					if !errors.Is(err, context.Canceled) {
						t.Errorf("%s: err = %v, want context.Canceled", method, err)
					}
					return
				}
				if err != nil || string(got) != test.want {
					t.Errorf("%s = %s, %v, want %s", method, got, err, test.want)
				}
			}
			got, err := values.SelectValue(context.Background(), "T", []string{"a"}, extension.ExtensionUIDialogOptions{})
			check("SelectValue", got, err)
			got, err = values.InputValue(context.Background(), "T", nil, extension.ExtensionUIDialogOptions{})
			check("InputValue", got, err)
			got, err = values.EditorValue(context.Background(), "T", nil)
			check("EditorValue", got, err)
			if bound.sawPlaceholder != nil || bound.sawPrefill != nil {
				t.Errorf("an unset placeholder/prefill reached the bound UI as %v/%v, want nil", bound.sawPlaceholder, bound.sawPrefill)
			}
			if test.err == nil {
				// confirm answers `false` for an undefined result; here the bound UI's own value is returned unchanged.
				got, err = values.ConfirmValue(context.Background(), "T", "m", extension.ExtensionUIDialogOptions{})
				check("ConfirmValue", got, err)
			}
			dialogs := 4
			if test.err != nil {
				dialogs = 3
			}
			if events := rec.wait(t, 2*dialogs); len(events) < 2*dialogs {
				t.Errorf("prompt events = %v, want a start and an end for each of the %d dialogs", events, dialogs)
			}
		})
	}
}
