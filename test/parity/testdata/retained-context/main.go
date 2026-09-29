package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

type inputUI struct {
	extension.UIContext
	text string
}

func (ui *inputUI) Input(ctx context.Context, _, _ string, _ extension.ExtensionUIDialogOptions) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return ui.text, nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	dir, err := os.MkdirTemp("", "pig-retained-context-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	host := subprocess.NewHost(dir)
	defer host.Shutdown("probe complete")
	ui := &inputUI{UIContext: extension.NoopUIContext}
	bridge := subprocess.NewUIBridge(nil)
	bridge.SetUIContext(ui)
	host.SetUIBridge(bridge)
	captured := make(chan sdk.Context, 1)
	fixture := sdk.New("retained")
	fixture.Command("capture", "Retain original context", func(ctx sdk.Context, _ string) error { captured <- ctx; return nil })
	loaded, err := host.LoadInProcess(context.Background(), subprocess.ExtConfig{Name: "retained", Enabled: true}, func(conn net.Conn) error { return fixture.RunWithConn(conn) })
	if err != nil {
		return err
	}
	if err := loaded.Commands["capture"].Handler(context.Background(), ""); err != nil {
		return err
	}
	ctx := <-captured
	values := []any{}
	for _, text := range []string{"current draft", "", "replacement draft"} {
		ui.text = text
		value, ok, err := ctx.Input("retained", "")
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("retained input was not accepted")
		}
		values = append(values, value)
	}
	host.Shutdown("generation retired")
	_, _, err = ctx.Input("retained", "")
	values = append(values, err != nil)
	return json.NewEncoder(os.Stdout).Encode(values)
}
