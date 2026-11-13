package cli

// pi: packages/coding-agent/src/cli/project-trust.ts

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// cancelingTrustUI answers every dialog with context.Canceled.
type cancelingTrustUI struct{ extension.UIContext }

func (cancelingTrustUI) Select(context.Context, string, []string, extension.ExtensionUIDialogOptions) (string, error) {
	return "", context.Canceled
}

func (cancelingTrustUI) Confirm(context.Context, string, string, extension.ExtensionUIDialogOptions) (bool, error) {
	return false, context.Canceled
}

func (cancelingTrustUI) Input(context.Context, string, string, extension.ExtensionUIDialogOptions) (string, error) {
	return "", context.Canceled
}

// Pi project-trust.ts:31-38 confirm awaits showStartupSelector and answers false when the dialog resolves undefined: a dismissed Confirm is false, not an error.
func TestProjectTrustContextDismissedConfirmAnswersFalse(t *testing.T) {
	trust := buildProjectTrustContext("/project", appModeInteractive, true, cancelingTrustUI{extension.NoopUIContext}, false, &bytes.Buffer{}, false)
	if confirmed, err := trust.UI.Confirm(t.Context(), "trust?", "message", extension.ExtensionUIDialogOptions{}); confirmed || err != nil {
		t.Fatalf("Confirm = %v, %v; want false, nil", confirmed, err)
	}
}

// A cancellation of the caller itself is not a dismissed dialog: with the handler context cancelled, every dialog returns the cancellation. A live interactive-mode UI hands its own answer through unchanged.
func TestProjectTrustContextCallerCancellationPropagates(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	trust := buildProjectTrustContext("/project", appModeInteractive, true, cancelingTrustUI{extension.NoopUIContext}, false, &bytes.Buffer{}, false)
	if _, err := trust.UI.Select(ctx, "pick", []string{"a"}, extension.ExtensionUIDialogOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Select err = %v, want context.Canceled", err)
	}
	if _, err := trust.UI.Confirm(ctx, "t", "m", extension.ExtensionUIDialogOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Confirm err = %v, want context.Canceled", err)
	}
	if _, err := trust.UI.Input(ctx, "t", "p", extension.ExtensionUIDialogOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Input err = %v, want context.Canceled", err)
	}
	live := buildProjectTrustContext("/project", appModeInteractive, true, cancelingTrustUI{extension.NoopUIContext}, true, &bytes.Buffer{}, false)
	if _, err := live.UI.Select(t.Context(), "pick", []string{"a"}, extension.ExtensionUIDialogOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("live Select err = %v, want the live UI answer unchanged", err)
	}
}
