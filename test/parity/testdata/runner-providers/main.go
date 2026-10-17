package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func main() {
	if err := run(); err != nil {
		panic(err)
	}
}

func emit(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Println(string(data))
	return err
}

func run() (resultErr error) {
	root, err := os.MkdirTemp("", "runner-providers-")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(root)) }()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: root, AgentDir: filepath.Join(root, "agent")})
	if err != nil {
		return err
	}
	configData, err := os.ReadFile("test/parity/scenarios/extensions-runtime/testdata/runner-providers/config.json")
	if err != nil {
		return err
	}
	var config extension.ProviderConfig
	if err := json.Unmarshal(configData, &config); err != nil {
		return err
	}
	runtime := extension.CreateExtensionRuntime()
	if err := runtime.RegisterProvider("broken-provider", extension.ProviderConfig{StreamSimple: func(extension.Model, extension.AIContext, extension.SimpleStreamOptions) extension.AssistantMessageEventStream {
		panic("should not run")
	}}, "/tmp/broken-extension.ts"); err != nil {
		return err
	}
	runner := inproc.NewRunner(nil, root, runtime)
	var failures []string
	runner.AddErrorListener(func(err *extension.ExtensionError) { failures = append(failures, err.ExtensionPath+": "+err.Error) })
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{ModelRegistry: services.Registry()}, nil)
	refreshed := services.ModelRuntime().Refresh(context.Background(), ai.ModelsRefreshOptions{AllowNetwork: new(false)})
	if err := emit(struct {
		Errors  []string `json:"errors"`
		Aborted bool     `json:"aborted"`
	}{failures, refreshed.Aborted}); err != nil {
		return err
	}
	queued := extension.CreateExtensionRuntime()
	if err := queued.RegisterProvider("queued-provider", config); err != nil {
		return err
	}
	second := config
	second.Models = []extension.ProviderModelConfig{{ID: "instant-model-2", Name: "Instant Model 2", Reasoning: false, Input: []string{"text"}, Cost: extension.ProviderModelCost{}, ContextWindow: 128000, MaxTokens: 4096}}
	if err := queued.RegisterProvider("queued-provider", second); err != nil {
		return err
	}
	before := len(queued.PendingProviderRegistrations())
	queued.UnregisterProvider("queued-provider")
	if err := emit(struct {
		Before int `json:"before"`
		After  int `json:"after"`
	}{before, len(queued.PendingProviderRegistrations())}); err != nil {
		return err
	}
	live := extension.CreateExtensionRuntime()
	inproc.NewRunner(nil, root, live).BindCore(extension.ExtensionActions{}, extension.ContextActions{ModelRegistry: services.Registry()}, nil)
	pendingBefore := len(live.PendingProviderRegistrations())
	if err := live.RegisterProvider("instant-provider", config); err != nil {
		return err
	}
	model := services.Registry().Find("instant-provider", "instant-model")
	if model == nil {
		return errors.New("post-bind provider model is absent")
	}
	if err := emit(struct {
		PendingBefore int           `json:"pendingBefore"`
		PendingAfter  int           `json:"pendingAfter"`
		Tiers         []ai.CostTier `json:"tiers"`
	}{pendingBefore, len(live.PendingProviderRegistrations()), model.Capabilities.CostTiers}); err != nil {
		return err
	}
	live.UnregisterProvider("instant-provider")
	return emit(struct {
		Removed bool `json:"removed"`
	}{services.Registry().Find("instant-provider", "instant-model") == nil})
}
