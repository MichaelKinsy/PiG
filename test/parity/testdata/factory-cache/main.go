package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

func main() {
	for _, mode := range []string{"same-cwd", "direct", "reload", "cross-cwd"} {
		if err := run(mode); err != nil {
			panic(err)
		}
	}
}

func run(mode string) (resultErr error) {
	root, err := os.MkdirTemp("", "factory-cache-")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(root)) }()
	for key, value := range map[string]string{"PIG_FACTORY_CACHE_ROOT": root, "PIG_FACTORY_CACHE_CASE": mode, "PIG_FACTORY_CACHE_DIST": ""} {
		if err := os.Setenv(key, value); err != nil {
			return err
		}
		defer func() { resultErr = errors.Join(resultErr, os.Unsetenv(key)) }()
	}
	entry, err := filepath.Abs("test/parity/scenarios/extensions-runtime/testdata/factory-cache/probe.mjs")
	if err != nil {
		return err
	}
	host := subprocess.NewHost(root)
	defer host.Shutdown("done")
	loaded, failures := host.LoadAll(context.Background(), []subprocess.ExtConfig{{Name: "cache-probe", Source: entry, Enabled: true}})
	if len(failures) != 0 || len(loaded) != 1 {
		return fmt.Errorf("loaded=%v failures=%v", loaded, failures)
	}
	fmt.Printf("%s:%s\n", mode, loaded[0].Commands["cache-report"].Description)
	return nil
}
