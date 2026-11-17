package subprocess

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// pendingDialogUI is a UI whose dialogs stay open until the call that asked for them ends, as Pi's do until the user answers: it reports each dialog it opens.
type pendingDialogUI struct {
	*testUIContext
	opened chan string
}

func (u *pendingDialogUI) hold(ctx context.Context, kind string) error {
	select {
	case u.opened <- kind:
	default:
	}
	<-ctx.Done()
	return ctx.Err()
}

func (u *pendingDialogUI) Select(ctx context.Context, _ string, _ []string, _ extension.ExtensionUIDialogOptions) (string, error) {
	return "", u.hold(ctx, "select")
}

func (u *pendingDialogUI) Confirm(ctx context.Context, _, _ string, _ extension.ExtensionUIDialogOptions) (bool, error) {
	return false, u.hold(ctx, "confirm")
}

func (u *pendingDialogUI) Input(ctx context.Context, _, _ string, _ extension.ExtensionUIDialogOptions) (string, error) {
	return "", u.hold(ctx, "input")
}

func (u *pendingDialogUI) Editor(ctx context.Context, _, _ string) (string, error) {
	return "", u.hold(ctx, "editor")
}

// writePendingDialogModule writes a Go extension with one command per dialog surface, each awaiting the dialog and doing nothing after it.
func writePendingDialogModule(t testing.TB, modulePath, extName string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), fmt.Appendf(nil, "module %s\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n", modulePath), 0o644); err != nil {
		t.Fatal(err)
	}
	src := fmt.Sprintf(`package ext

import "github.com/MichaelKinsy/PiG/extensions/sdk"

func Extension() *sdk.Extension {
	e := sdk.New(%q)
	e.Command("pend-select", "await a selector", func(ctx sdk.Context, _ string) error { _, _, err := ctx.Select("pending", []string{"one", "two"}); return err })
	e.Command("pend-confirm", "await a confirmation", func(ctx sdk.Context, _ string) error { _, err := ctx.Confirm("pending", "sure?"); return err })
	e.Command("pend-input", "await an input", func(ctx sdk.Context, _ string) error { _, _, err := ctx.Input("pending", "type here"); return err })
	e.Command("pend-editor", "await an editor", func(ctx sdk.Context, _ string) error { _, _, err := ctx.Editor("pending", ""); return err })
	return e
}
`, extName)
	if err := os.WriteFile(filepath.Join(dir, "ext.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestReloadLeavesPendingDialogCallsPending pins what a reload does to the command that awaits a dialog of an extension it replaces or removes. Pi's resetExtensionUI
// disposes the open dialogs and does not settle their promises (interactive-mode.ts:2592-2598), and the old instance outlives the reload: the command never goes on.
// Probed on Pi 1.5.2 by extensions-runtime scenarios 109 and 110 for select, confirm, input and editor. The host leaves every dialog call open and ends it with the
// session, in both isolation modes.
func TestReloadLeavesPendingDialogCallsPending(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping packed SDK builds in short mode")
	}
	t.Parallel()
	configs := []ExtConfig{
		packedFactoryConfig("dialog-go-a", writePendingDialogModule(t, "example.com/dialog/goa", "dialog-go-a"), "example.com/dialog/goa", "da"),
		packedFactoryConfig("dialog-go-b", writePendingDialogModule(t, "example.com/dialog/gob", "dialog-go-b"), "example.com/dialog/gob", "db"),
	}
	for _, surface := range []string{"select", "confirm", "input", "editor"} {
		for _, isolation := range []string{"shared-ok", "strict"} {
			for _, owner := range []string{"replaced", "removed"} {
				t.Run(surface+"/"+isolation+"/"+owner, func(t *testing.T) {
					configs := append([]ExtConfig(nil), configs...)
					for i := range configs {
						configs[i].Isolation = isolation
					}
					ui := &pendingDialogUI{testUIContext: newTestUIContext(), opened: make(chan string, 1)}
					host := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
					bridge := NewUIBridge(func() {})
					bridge.SetUIContext(ui)
					bridge.SetHostAction("streamModel", func(context.Context, map[string]any, map[string]any) (*ai.AssistantMessageEventStream, error) {
						return packedTestModelStream()
					})
					host.SetUIBridge(bridge)
					defer host.Shutdown("test done")

					ctx, cancel := buildContext(t)
					defer cancel()
					loaded, errs := host.LoadAll(ctx, configs)
					if len(errs) > 0 || len(loaded) != 2 {
						t.Fatalf("LoadAll loaded=%d errors=%v", len(loaded), errs)
					}
					commandDone := make(chan error, 1)
					go func() { commandDone <- loaded[0].Commands["pend-"+surface].Handler(context.Background(), "") }()
					select {
					case kind := <-ui.opened:
						if kind != surface {
							t.Fatalf("the command opened a %s dialog, want %s", kind, surface)
						}
					case <-time.After(10 * time.Second):
						t.Fatalf("the %s dialog never opened", surface)
					}

					after := configs
					if owner == "removed" {
						after = nil
					}
					host.SetConfigLoader(func() ([]ExtConfig, error) { return after, nil })
					if _, err := host.Reload(ctx); err != nil {
						t.Fatal(err)
					}
					select {
					case err := <-commandDone:
						t.Fatalf("the command that awaited the %s dialog returned after the reload: %v; Pi leaves it awaiting", surface, err)
					case <-time.After(300 * time.Millisecond):
					}
					host.Shutdown("session ended")
					select {
					case <-commandDone:
					case <-time.After(5 * time.Second):
						t.Fatal("the command remained blocked after the session ended")
					}
				})
			}
		}
	}
}
