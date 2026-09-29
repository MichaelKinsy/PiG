package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/tui"
)

type inputUI struct {
	extension.UIContext
	registered chan extension.RemoteTerminalInputHandler
}

func (ui *inputUI) OnRemoteTerminalInput(_ string, handler extension.RemoteTerminalInputHandler) func() {
	ui.registered <- handler
	return func() {}
}

type observation struct {
	Units  []uint16 `json:"units"`
	Editor []uint16 `json:"editor"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	root, err := os.MkdirTemp("", "pig-surrogate-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(root) }()
	host := subprocess.NewHost(root)
	defer host.Shutdown("surrogate probe complete")
	ui := &inputUI{UIContext: extension.NoopUIContext, registered: make(chan extension.RemoteTerminalInputHandler, 1)}
	bridge := subprocess.NewUIBridge(nil)
	bridge.SetUIContext(ui)
	host.SetUIBridge(bridge)
	source, err := filepath.Abs("test/parity/testdata/terminal-surrogate/probe.mjs")
	if err != nil {
		return err
	}
	ctx := context.Background()
	ext, err := host.Load(ctx, subprocess.ExtConfig{Name: "probe", Source: source, Enabled: true})
	if err != nil {
		return err
	}
	if err := ext.Commands["subscribe"].Handler(ctx, ""); err != nil {
		return err
	}
	handler := <-ui.registered
	editor := tui.NewEditor()
	parser := tui.NewStdinBuffer(tui.StdinBufferOptions{})
	result := struct {
		Observed []observation `json:"observed"`
		Text     []uint16      `json:"text"`
		Rewrites [][]uint16    `json:"rewrites"`
	}{}
	for _, data := range parser.ProcessTerminalBytes([]byte("A😀B")) {
		verdict := handler(subprocess.WithTerminalInputState(ctx, editor.Text(), false), data)
		if verdict.Data == nil {
			return fmt.Errorf("missing input verdict")
		}
		var observed observation
		if err := json.Unmarshal([]byte(*verdict.Data), &observed); err != nil {
			return err
		}
		result.Observed = append(result.Observed, observed)
		editor.HandleInput(data)
	}
	result.Text = jsstring.ToUTF16(editor.Text())
	for _, data := range []string{"rewrite-high", "rewrite-low"} {
		verdict := handler(subprocess.WithTerminalInputState(ctx, editor.Text(), false), data)
		if verdict.Data == nil {
			return fmt.Errorf("missing rewrite verdict")
		}
		result.Rewrites = append(result.Rewrites, jsstring.ToUTF16(*verdict.Data))
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
