package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/packagecontent"
)

func main() {
	if err := run(); err != nil {
		panic(err)
	}
}

func run() (resultErr error) {
	root, err := os.MkdirTemp("", "runner-markdown-")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(root)) }()
	fixture, err := filepath.Abs("test/parity/scenarios/extensions-runtime/testdata/runner-markdown")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "extensions"), 0o700); err != nil {
		return err
	}
	identity, err := os.ReadFile(filepath.Join(fixture, "identity.ts"))
	if err != nil {
		return err
	}
	for _, suffix := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(root, "extensions", "markdown-renderer-"+suffix+".ts"), identity, 0o600); err != nil {
			return err
		}
	}
	for _, tc := range []struct {
		name  string
		paths []string
	}{
		{"identity", packagecontent.DiscoverAutomatic(filepath.Join(root, "extensions"), packagecontent.Extensions)},
		{"ordered", []string{filepath.Join(fixture, "first.ts"), filepath.Join(fixture, "second.ts")}},
		{"reversed", []string{filepath.Join(fixture, "second.ts"), filepath.Join(fixture, "first.ts")}},
	} {
		if err := collect(root, tc.name, tc.paths); err != nil {
			return err
		}
	}
	return nil
}

func collect(root, name string, paths []string) error {
	host := subprocess.NewHost(root)
	defer host.Shutdown("done")
	configs := make([]subprocess.ExtConfig, 0, len(paths))
	for _, path := range paths {
		configs = append(configs, subprocess.ExtConfig{Name: strings.TrimSuffix(filepath.Base(path), ".ts"), Source: path, Enabled: true})
	}
	loaded, failures := host.LoadAll(context.Background(), configs)
	if len(failures) != 0 {
		return fmt.Errorf("load: %v", failures)
	}
	runner := inproc.NewRunner(loaded, root)
	transformers := runner.GetMarkdownTransformers()
	output := "x"
	for _, transform := range transformers {
		output = transform(output, extension.MarkdownTransformContext{MessageType: "assistant", IsStreaming: true, AvailableWidth: 73})
	}
	fmt.Printf("%s:%d:%s\n", name, len(transformers), output)
	return nil
}
