package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

type reportUI struct {
	extension.UIContext
	messages chan<- string
}

func (ui *reportUI) Notify(message, _ string) { ui.messages <- message }

func main() {
	entry, err := filepath.Abs("test/parity/scenarios/extensions-runtime/testdata/ext/tool-schema.mjs")
	if err != nil {
		panic(err)
	}
	h := subprocess.NewHost(".")
	defer h.Shutdown("done")
	b := subprocess.NewUIBridge(func() {})
	messages := make(chan string, 1)
	// The Pi driver supplies a notify UI. An observer hook alone does not bind UI availability.
	b.SetUIContext(&reportUI{UIContext: extension.NoopUIContext, messages: messages})
	h.SetUIBridge(b)
	exts, failures := h.LoadAll(context.Background(), []subprocess.ExtConfig{{Name: "tool-schema", Source: entry, Enabled: true}})
	if len(failures) != 0 || len(exts) != 1 {
		panic(fmt.Sprint(failures))
	}
	if err := exts[0].Commands["schema-report"].Handler(context.Background(), ""); err != nil {
		panic(err)
	}
	fmt.Println(<-messages)
	fmt.Println(string(exts[0].Tools["noop"].Definition.Parameters))
}
