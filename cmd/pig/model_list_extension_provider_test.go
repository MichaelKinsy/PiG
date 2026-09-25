package main

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// TestPrintModelList_ShowsRegisteredExtensionProvider guards the --list-models
// production path: printModelList must enumerate providers registered in the
// supplied registry (e.g. by an extension), not just built-ins. Combined with
// the load-before-list ordering in main(), this is what makes an extension's
// model provider appear in `pig --list-models`. If printModelList stopped
// reading the registry, this fails.
func TestPrintModelList_ShowsRegisteredExtensionProvider(t *testing.T) {
	dir := t.TempDir()
	reg := codingagent.NewModelRegistry(dir)
	reg.RegisterProvider("parity-prov", extension.ProviderConfig{
		Name:    "Parity Prov",
		BaseURL: "http://127.0.0.1:9",
		APIKey:  "x",
		API:     "openai-completions",
		Models: []extension.ProviderModelConfig{
			{ID: "parity-model", Name: "parity-model", Input: []string{"text"}},
		},
	})

	out := captureStdout(func() { printModelList(reg, dir, "") })

	if !strings.Contains(out, "parity-prov") || !strings.Contains(out, "parity-model") {
		t.Fatalf("printModelList did not list the registered extension provider:\n%s", out)
	}
}
