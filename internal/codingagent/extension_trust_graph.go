package codingagent

// Ports packages/coding-agent/src/core/trust-manager.ts

// The ProjectTrustStore an Extension SDK constructs, and hasTrustRequiringProjectResources: Pi's trust store class as a host-owned object an extension names by handle. The object is the runtime's own store, so the decisions an extension saves are the ones the project trust gate reads. The Node runtime serves Pi's own class in the extension process.

import (
	"context"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// GraphProjectTrustStore is a ProjectTrustStore an extension constructed.
type GraphProjectTrustStore struct {
	store *ProjectTrustStore
}

// NewGraphProjectTrustStore builds `new ProjectTrustStore(agentDir)`.
func NewGraphProjectTrustStore(op string, raw json.RawMessage) (*GraphProjectTrustStore, error) {
	if op != "new" {
		return nil, fmt.Errorf("ProjectTrustStore has no static factory %q", op)
	}
	args, err := ParseGraphArgs(raw)
	if err != nil {
		return nil, err
	}
	agentDir, err := args.String(0)
	if err != nil {
		return nil, err
	}
	return &GraphProjectTrustStore{NewProjectTrustStore(agentDir)}, nil
}

// GraphProjectTrustStoreStatic runs the trust-manager.ts functions that are not methods of the class.
func GraphProjectTrustStoreStatic(_ context.Context, method string, raw json.RawMessage) (any, error) {
	args, err := ParseGraphArgs(raw)
	if err != nil {
		return nil, err
	}
	if method != "hasTrustRequiringProjectResources" {
		return nil, fmt.Errorf("ProjectTrustStore has no static function %q", method)
	}
	cwd, err := args.String(0)
	if err != nil {
		return nil, err
	}
	return HasTrustRequiringProjectResources(cwd), nil
}

// Call runs a ProjectTrustStore method.
func (g *GraphProjectTrustStore) Call(_ context.Context, method string, raw json.RawMessage) (any, error) {
	args, err := ParseGraphArgs(raw)
	if err != nil {
		return nil, err
	}
	switch method {
	case "get":
		cwd, err := args.String(0)
		if err != nil {
			return nil, err
		}
		return g.store.Get(cwd)
	case "getEntry":
		cwd, err := args.String(0)
		if err != nil {
			return nil, err
		}
		entry, err := g.store.GetEntry(cwd)
		if err != nil || entry == nil {
			return nil, err
		}
		return map[string]any{"path": entry.Path, "decision": entry.Decision}, nil
	case "set":
		cwd, err := args.String(0)
		if err != nil {
			return nil, err
		}
		var decision *bool
		if err := args.Unmarshal(1, func(raw json.RawMessage) error { return json.Unmarshal(raw, &decision) }); err != nil {
			return nil, err
		}
		return nil, g.store.Set(cwd, decision)
	case "setMany":
		var updates []struct {
			Path     string `json:"path"`
			Decision *bool  `json:"decision"`
		}
		if err := args.Unmarshal(0, func(raw json.RawMessage) error { return json.Unmarshal(raw, &updates) }); err != nil {
			return nil, err
		}
		batch := make([]ProjectTrustUpdate, len(updates))
		for i, update := range updates {
			batch[i] = ProjectTrustUpdate{Path: update.Path, Decision: update.Decision}
		}
		return nil, g.store.SetMany(batch)
	}
	return nil, errors.New("ProjectTrustStore has no method " + method)
}

var _ subprocess.SessionGraphObject = (*GraphProjectTrustStore)(nil)
