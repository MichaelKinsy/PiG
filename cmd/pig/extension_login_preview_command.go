package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/extension/pigsdk"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// extensionLoginPreviewTimeout bounds building, loading, and rendering the
// previewed extension. Tests replace it with their wait budget because a cold
// extension build on a loaded machine can take longer.
var extensionLoginPreviewTimeout = 30 * time.Second

var extensionLoginPreviewWidth = func() int {
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width <= 0 {
		return 80
	}
	return width
}

func loginPreviewGlyphFreeEnabled() bool {
	switch strings.TrimSpace(os.Getenv("PIG_LOGO_GLYPHFREE")) {
	case "1":
		return true
	case "0":
		return false
	default:
		return os.Getenv("TERM_PROGRAM") == "Apple_Terminal"
	}
}

// pig additive (D60): preview the typed native login without starting a
// model session or restoring the retired authored extension manifest.
type loginPreviewUI struct {
	extension.UIContext
	definition extension.ValidatedLoginDefinition
	accepted   bool
}

func newLoginPreviewUI() *loginPreviewUI {
	return &loginPreviewUI{UIContext: extension.NoopUIContext}
}

func (u *loginPreviewUI) SetLogin(definition extension.LoginDefinition) error {
	validated, err := extension.ValidateLoginDefinition(definition)
	if err != nil {
		return err
	}
	u.definition = validated
	u.accepted = true
	return nil
}

func runExtensionLoginPreview(args []string) int {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			fmt.Fprintf(os.Stderr, "error: unknown option %s for extension preview-login\n", arg)
			return 1
		}
	}
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		fmt.Fprintln(os.Stderr, "error: extension preview-login requires exactly one extension source")
		return 1
	}

	cwd, err := os.Getwd()
	if err != nil {
		return printLoginPreviewError("resolve working directory", err)
	}
	root, err := resolveInputPackageSourceRoot(cwd, args[0])
	if err != nil {
		return printLoginPreviewError("resolve extension source", err)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return printLoginPreviewError("resolve extension source", err)
	}
	if _, err := os.Stat(abs); err != nil {
		return printLoginPreviewError("load extension source", err)
	}
	configs, _, _, err := extensionConfigsFromPath(abs)
	if err != nil {
		return printLoginPreviewError("resolve extension runtime", err)
	}
	if len(configs) != 1 {
		return printLoginPreviewError("resolve extension runtime", fmt.Errorf("source contains %d extensions; preview-login requires exactly one", len(configs)))
	}
	if err := pigsdk.EnsureSynced(codingagent.ConfigRoot()); err != nil {
		return printLoginPreviewError("stage extension SDKs", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), extensionLoginPreviewTimeout)
	defer cancel()
	host := subprocess.NewHostWithConfigRoot(filepath.Dir(abs), codingagent.ConfigRoot())
	defer host.Shutdown("login preview complete")
	ui := newLoginPreviewUI()
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	host.SetUIBridge(bridge)
	width := extensionLoginPreviewWidth()
	trueColor := tui.SupportsTrueColor()
	glyphFree := loginPreviewGlyphFreeEnabled()
	host.SetMode(string(extension.ModeTUI))
	host.SetWidthFunc(func() int { return width })
	loaded, loadErrors := host.LoadAll(ctx, configs)
	if len(loadErrors) > 0 {
		return printLoginPreviewError("load extension", errors.Join(loadErrors...))
	}
	if len(loaded) != 1 {
		return printLoginPreviewError("load extension", fmt.Errorf("registered %d of 1 extensions", len(loaded)))
	}

	runner := inproc.NewRunner(loaded, cwd)
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{UI: ui, Mode: extension.ModeTUI}, nil)
	var handlerErrors []error
	runner.AddErrorListener(func(err *extension.ExtensionError) {
		handlerErrors = append(handlerErrors, fmt.Errorf("%s: %s", err.ExtensionPath, err.Error))
	})
	if _, err := runner.Emit(ctx, extension.SessionStartEvent{Type: "session_start", Reason: "startup"}); err != nil {
		return printLoginPreviewError("run session_start", err)
	}
	if len(handlerErrors) > 0 {
		return printLoginPreviewError("run session_start handler", errors.Join(handlerErrors...))
	}
	if !ui.accepted {
		return printLoginPreviewError("run session_start", errors.New("extension did not set a login"))
	}

	lines := codingagent.RenderLoginHeader(ui.definition, width, codingagent.LoginHeaderOptions{
		TrueColor: trueColor,
		GlyphFree: glyphFree,
	})
	for _, line := range lines {
		if _, err := fmt.Fprintln(os.Stdout, line); err != nil {
			return printLoginPreviewError("write preview", err)
		}
	}
	return 0
}

func printLoginPreviewError(action string, err error) int {
	fmt.Fprintf(os.Stderr, "error: %s: %v\n", action, err)
	return 1
}
