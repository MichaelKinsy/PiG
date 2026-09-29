package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

func main() {
	root, err := filepath.Abs("test/parity/scenarios/extensions-runtime/testdata/factory-failure")
	if err != nil {
		panic(err)
	}
	host := subprocess.NewHost(".")
	defer host.Shutdown("done")
	type registration struct {
		Name   string                   `json:"name"`
		Config extension.ProviderConfig `json:"config"`
	}
	var mu sync.Mutex
	providers := []registration{}
	removed := []string{}
	host.SetProviderCallbacks(func(name string, config extension.ProviderConfig) error {
		mu.Lock()
		defer mu.Unlock()
		providers = append(providers, registration{Name: name, Config: config})
		return nil
	}, func(name string) {
		mu.Lock()
		defer mu.Unlock()
		removed = append(removed, name)
	})
	loaded, failures := host.LoadAll(context.Background(), []subprocess.ExtConfig{
		{Name: "working", Source: filepath.Join(root, "working.mjs"), Enabled: true},
		{Name: "failing", Source: filepath.Join(root, "failing.mjs"), Enabled: true},
		{Name: "survivor", Source: filepath.Join(root, "survivor.mjs"), Enabled: true},
	})
	if len(failures) != 1 || len(loaded) != 2 || loaded[0].Name != "working" || loaded[1].Name != "survivor" {
		panic(fmt.Sprintf("loaded=%v failures=%v", loaded, failures))
	}
	failure, ok := errors.AsType[*subprocess.FactoryLoadError](failures[0])
	if !ok {
		panic(failures[0])
	}
	fmt.Println(failure.Message)
	fmt.Println(loaded[1].Commands["factory-failure-report"].Description)
	mu.Lock()
	state, err := json.Marshal(struct {
		FailedFlagPresent bool           `json:"failedFlagPresent"`
		TimerFlagPresent  bool           `json:"timerFlagPresent"`
		Providers         []registration `json:"providers"`
		Removed           []string       `json:"removed"`
	}{host.FlagDefault("", "failed-flag") != nil, host.FlagDefault("", "timer-flag") != nil, providers, removed})
	mu.Unlock()
	if err != nil {
		panic(err)
	}
	fmt.Println(string(state))
}
