package testfixture

import (
	"encoding/json"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

func registrySessionProbe(ctx sdk.Context, _ string) error {
	s := ctx.SessionManager()
	r := ctx.ModelRegistry()
	out := map[string]any{}
	add := func(name string, value any, err error) error {
		if err != nil {
			return err
		}
		out[name] = value
		return nil
	}
	// Each explicit call binds the public method, not a stringly host-call escape hatch.
	cwd, err := s.GetCwd()
	if err = add("cwd", cwd, err); err != nil {
		return err
	}
	dir, err := s.GetSessionDir()
	if err = add("dir", dir, err); err != nil {
		return err
	}
	id, err := s.GetSessionID()
	if err = add("id", id, err); err != nil {
		return err
	}
	name, err := s.GetSessionName()
	if err = add("name", name, err); err != nil {
		return err
	}
	leaf, err := s.GetLeafID()
	if err = add("leaf", leaf, err); err != nil {
		return err
	}
	entry, err := s.GetEntry("one")
	if err = add("entry", entry, err); err != nil {
		return err
	}
	missing, err := s.GetEntry("missing")
	if err = add("missing", missing, err); err != nil {
		return err
	}
	label, err := s.GetLabel("one")
	if err = add("label", label, err); err != nil {
		return err
	}
	entries, err := s.GetEntries()
	if err = add("entries", entries, err); err != nil {
		return err
	}
	branch, err := s.GetBranch(new("one"))
	if err = add("branch", branch, err); err != nil {
		return err
	}
	tree, err := s.GetTree()
	if err = add("tree", tree, err); err != nil {
		return err
	}
	contextEntries, err := s.BuildContextEntries()
	if err = add("contextEntries", contextEntries, err); err != nil {
		return err
	}
	projection, err := s.BuildSessionProjection()
	if err = add("projection", projection, err); err != nil {
		return err
	}
	models, err := r.GetAll()
	if err = add("models", models, err); err != nil {
		return err
	}
	available, err := r.GetAvailable()
	if err = add("available", available, err); err != nil {
		return err
	}
	status, err := r.GetProviderAuthStatus("registry-probe")
	if err = add("status", status, err); err != nil {
		return err
	}
	display, err := r.GetProviderDisplayName("registry-probe")
	if err = add("display", display, err); err != nil {
		return err
	}
	regErr, err := r.GetError()
	if err = add("error", regErr, err); err != nil {
		return err
	}
	config, err := r.GetRegisteredProviderConfig("registry-probe")
	if err = add("config", config, err); err != nil {
		return err
	}
	ids, err := r.GetRegisteredProviderIDs()
	if err = add("ids", ids, err); err != nil {
		return err
	}
	auth, err := r.GetProviderAuth("registry-probe")
	if err = add("auth", auth, err); err != nil {
		return err
	}
	out["apiKey"] = r.GetApiKeyForProvider("registry-probe")
	out["missingKey"] = r.GetApiKeyForProvider("missing")
	refreshed, err := r.Refresh(sdk.ModelsRefreshOptions{AllowNetwork: new(false)})
	if err = add("refresh", refreshed, err); err != nil {
		return err
	}
	data, err := json.Marshal(out)
	if err != nil {
		return err
	}
	ctx.Notify(string(data), "info")
	return nil
}
