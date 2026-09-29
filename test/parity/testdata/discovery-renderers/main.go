package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/packagecontent"
)

func main() {
	if err := run(); err != nil {
		panic(err)
	}
}

func run() (resultErr error) {
	root, err := os.MkdirTemp("", "discovery-renderers-")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(root)) }()
	extensions := filepath.Join(root, "extensions")
	if err := os.Mkdir(extensions, 0o755); err != nil {
		return err
	}
	source, err := os.ReadFile("test/parity/scenarios/extensions-runtime/testdata/discovery-renderers/with-renderer.ts")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(extensions, "with-renderer.ts"), source, 0o644); err != nil {
		return err
	}
	paths := packagecontent.DiscoverAutomatic(extensions, packagecontent.Extensions)
	var configs []subprocess.ExtConfig
	for _, path := range paths {
		configs = append(configs, subprocess.ExtConfig{Name: "with-renderer", Source: path, Enabled: true})
	}
	host := subprocess.NewHost(root)
	defer host.Shutdown("done")
	loaded, failures := host.LoadAll(context.Background(), configs)
	if len(failures) != 0 || len(loaded) != 1 {
		return fmt.Errorf("loaded=%v failures=%v", loaded, failures)
	}
	ext := loaded[0]
	_, message := ext.MessageRenderers["my-custom-type"]
	_, entry := ext.EntryRenderers["my-entry-type"]
	return json.NewEncoder(os.Stdout).Encode(struct {
		Errors   int      `json:"errors"`
		Paths    []string `json:"paths"`
		Markdown bool     `json:"markdown"`
		Message  bool     `json:"message"`
		Entry    bool     `json:"entry"`
	}{len(failures), []string{filepath.Base(ext.Path)}, ext.MarkdownTransformer != nil, message, entry})
}
