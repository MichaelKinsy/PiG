package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/coding"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() (resultErr error) {
	root, err := os.MkdirTemp("", "model-scope-")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(root)) }()
	type snapshot struct {
		Scope           []string `json:"scope"`
		Enabled         []string `json:"enabled"`
		DefaultProvider string   `json:"defaultProvider"`
		DefaultModel    string   `json:"defaultModel"`
	}
	var snapshots []snapshot
	for i, tc := range []struct{ scoped, persist bool }{{true, true}, {false, true}, {true, false}} {
		dir := filepath.Join(root, fmt.Sprint(i))
		services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
		if err != nil {
			return err
		}
		services.Registry().SetRuntimeAPIKey("anthropic", "test-key")
		sonnet, err := coding.BuildModel("anthropic/claude-sonnet-4-5", services)
		if err != nil {
			return err
		}
		opus, err := coding.BuildModel("anthropic/claude-opus-4-8", services)
		if err != nil {
			return err
		}
		opts := coding.SessionOptions{Model: sonnet, NoSession: true, SystemPrompt: "test", SkipBuiltinTools: true}
		if tc.scoped {
			opts.ScopedModels = []coding.ScopedModel{{Model: sonnet}}
			if err := services.SettingsManager().UpdateGlobal(func(s *coding.Settings) { s.EnabledModels = []string{"anthropic/claude-sonnet-4-5"} }); err != nil {
				return err
			}
		}
		session, err := coding.NewSession(services, opts)
		if err != nil {
			return err
		}
		if err := session.SetModel(opus, coding.ModelMutationOptions{Persist: tc.persist}); err != nil {
			_ = session.Close()
			return err
		}
		got := snapshot{Scope: []string{}, Enabled: services.SettingsManager().GetEnabledModels(), DefaultProvider: services.Settings().DefaultProvider, DefaultModel: services.Settings().DefaultModel}
		for _, entry := range session.ScopedModels() {
			got.Scope = append(got.Scope, entry.Model.ProviderMeta.ProviderID+"/"+entry.Model.ID)
		}
		snapshots = append(snapshots, got)
		if err := session.Close(); err != nil {
			return err
		}
	}
	return json.NewEncoder(os.Stdout).Encode(snapshots)
}
