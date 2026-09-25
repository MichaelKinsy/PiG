// Package artifact defines executable Piglet component plans and artifact records.
// pig additive (D18): Piglet builds record exact component realization and materialization.
package artifact

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// ComponentKind identifies an executable/runtime Piglet component class.
type ComponentKind string

const (
	// ComponentKindExtension is a Pig extension.
	ComponentKindExtension ComponentKind = "extension"
	// ComponentKindMCP is an MCP process or remote service adapter.
	ComponentKindMCP ComponentKind = "mcp"
	// ComponentKindHook is an executable hook.
	ComponentKindHook ComponentKind = "hook"
)

// Realization describes how one Piglet component executes.
type Realization string

const (
	// RealizationFused registers a compatible Go factory in-process.
	RealizationFused Realization = "fused"
	// RealizationSubprocess starts the component through Pig's extension host.
	RealizationSubprocess Realization = "subprocess"
	// RealizationExternal connects to a separately managed service.
	RealizationExternal Realization = "external"
)

// Materialization describes where one component's executable/runtime closure is supplied.
type Materialization string

const (
	// MaterializationBinary carries the component in the Piglet Binary.
	MaterializationBinary Materialization = "binary"
	// MaterializationRelease supplies the component from the managed Piglet release.
	MaterializationRelease Materialization = "release"
	// MaterializationAgentEnvironment supplies the component from agentEnv.
	MaterializationAgentEnvironment Materialization = "agentEnv"
	// MaterializationExternal requires an explicit external mapping.
	MaterializationExternal Materialization = "external"
)

// Origin is the exact source identity of one Piglet component.
type Origin struct {
	Source  string `json:"source"`
	Version string `json:"version,omitempty"`
	Digest  string `json:"digest"`
	Package string `json:"package,omitempty"`
	Plugin  string `json:"plugin,omitempty"`
}

// RuntimeRequirement identifies a language or native runtime needed by a component.
type RuntimeRequirement struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Target  string `json:"target,omitempty"`
}

// ComponentInput contains resolver/build facts used to choose executable component realization.
type ComponentInput struct {
	Kind              ComponentKind
	Name              string
	Origin            Origin
	Language          string
	Fusible           bool
	IsolationRequired bool
	External          bool
	Materialization   Materialization
	Runtime           *RuntimeRequirement
}

// Component is one resolved executable/runtime member of an immutable Piglet plan.
type Component struct {
	Kind            ComponentKind       `json:"kind"`
	Name            string              `json:"name"`
	Origin          Origin              `json:"origin"`
	Language        string              `json:"language,omitempty"`
	Realization     Realization         `json:"realization"`
	Materialization Materialization     `json:"materialization"`
	Runtime         *RuntimeRequirement `json:"runtime,omitempty"`
}

// ID returns the stable kind/name identity used for ordering and duplicate checks.
func (c Component) ID() string {
	return string(c.Kind) + "/" + c.Name
}

// Plan is the deterministic executable component realization plan for one resolved Piglet.
type Plan struct {
	Components []Component `json:"components"`
	Digest     string      `json:"digest"`
}

// BuildPlan validates resolver facts, chooses realization, and returns a stable plan.
func BuildPlan(inputs []ComponentInput) (Plan, error) {
	components := make([]Component, 0, len(inputs))
	for _, input := range inputs {
		component, err := buildComponent(input)
		if err != nil {
			return Plan{}, err
		}
		components = append(components, component)
	}

	slices.SortFunc(components, func(a, b Component) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Name, b.Name))
	})
	for i := 1; i < len(components); i++ {
		if components[i-1].ID() == components[i].ID() {
			return Plan{}, fmt.Errorf("duplicate component %q", components[i].ID())
		}
	}

	digest, err := digestComponents(components)
	if err != nil {
		return Plan{}, err
	}
	return Plan{
		Components: components,
		Digest:     digest,
	}, nil
}

// ValidatePlan verifies a decoded plan's vocabulary, ordering, identities, and digest.
func ValidatePlan(plan Plan) error {
	if !slices.IsSortedFunc(plan.Components, func(a, b Component) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Name, b.Name))
	}) {
		return fmt.Errorf("component plan entries are not in canonical order")
	}
	for i, component := range plan.Components {
		if i > 0 && plan.Components[i-1].ID() == component.ID() {
			return fmt.Errorf("duplicate component %q", component.ID())
		}
		if err := validateResolvedComponent(component); err != nil {
			return err
		}
	}
	digest, err := digestComponents(plan.Components)
	if err != nil {
		return err
	}
	if plan.Digest != digest {
		return fmt.Errorf("component plan digest mismatch: got %q, want %q", plan.Digest, digest)
	}
	return nil
}

func digestComponents(components []Component) (string, error) {
	payload := struct {
		Components []Component `json:"components"`
	}{Components: components}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode component plan: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func validateResolvedComponent(component Component) error {
	if component.Kind != ComponentKind(strings.TrimSpace(string(component.Kind))) || component.Name != strings.TrimSpace(component.Name) || component.Language != strings.ToLower(strings.TrimSpace(component.Language)) {
		return fmt.Errorf("component %q has non-canonical identity or language", component.ID())
	}
	if component.Origin.Source != strings.TrimSpace(component.Origin.Source) || component.Origin.Version != strings.TrimSpace(component.Origin.Version) || component.Origin.Digest != strings.TrimSpace(component.Origin.Digest) || component.Origin.Package != strings.TrimSpace(component.Origin.Package) || component.Origin.Plugin != strings.TrimSpace(component.Origin.Plugin) {
		return fmt.Errorf("component %q has non-canonical origin", component.ID())
	}
	identity, err := componentIdentity(component.Kind, component.Name)
	if err != nil {
		return err
	}
	if strings.TrimSpace(component.Origin.Source) == "" {
		return fmt.Errorf("component %q origin source is required", identity)
	}
	if err := validateDigest(component.Origin.Digest); err != nil {
		return fmt.Errorf("component %q origin digest: %w", identity, err)
	}
	if component.Origin.Package != "" && component.Origin.Plugin != "" {
		return fmt.Errorf("component %q origin cannot declare both package and plugin membership", identity)
	}
	if component.Runtime != nil {
		if component.Runtime.Name != strings.TrimSpace(component.Runtime.Name) || component.Runtime.Version != strings.TrimSpace(component.Runtime.Version) || component.Runtime.Target != strings.TrimSpace(component.Runtime.Target) {
			return fmt.Errorf("component %q has non-canonical runtime", identity)
		}
		if component.Runtime.Name == "" {
			return fmt.Errorf("component %q runtime name is required", identity)
		}
	}
	switch component.Realization {
	case RealizationFused:
		if component.Language != "go" || component.Materialization != MaterializationBinary || component.Runtime != nil {
			return fmt.Errorf("component %q has invalid fused realization", identity)
		}
	case RealizationSubprocess:
		switch component.Materialization {
		case MaterializationBinary, MaterializationRelease, MaterializationAgentEnvironment, MaterializationExternal:
		default:
			return fmt.Errorf("component %q has invalid subprocess materialization %q", identity, component.Materialization)
		}
		if (component.Language == "python" || component.Language == "node") && component.Runtime == nil {
			return fmt.Errorf("component %q %s subprocess requires a runtime", identity, component.Language)
		}
	case RealizationExternal:
		if component.Materialization != MaterializationExternal {
			return fmt.Errorf("component %q external realization requires external materialization", identity)
		}
	default:
		return fmt.Errorf("component %q has unsupported realization %q", identity, component.Realization)
	}
	return nil
}

func buildComponent(input ComponentInput) (Component, error) {
	input.Kind = ComponentKind(strings.TrimSpace(string(input.Kind)))
	input.Name = strings.TrimSpace(input.Name)
	input.Language = strings.ToLower(strings.TrimSpace(input.Language))
	input.Materialization = Materialization(strings.TrimSpace(string(input.Materialization)))
	input.Origin.Source = strings.TrimSpace(input.Origin.Source)
	input.Origin.Version = strings.TrimSpace(input.Origin.Version)
	input.Origin.Digest = strings.TrimSpace(input.Origin.Digest)
	input.Origin.Package = strings.TrimSpace(input.Origin.Package)
	input.Origin.Plugin = strings.TrimSpace(input.Origin.Plugin)
	identity, err := componentIdentity(input.Kind, input.Name)
	if err != nil {
		return Component{}, err
	}
	if strings.TrimSpace(input.Origin.Source) == "" {
		return Component{}, fmt.Errorf("component %q origin source is required", identity)
	}
	if err := validateDigest(input.Origin.Digest); err != nil {
		return Component{}, fmt.Errorf("component %q origin digest: %w", identity, err)
	}
	if input.Origin.Package != "" && input.Origin.Plugin != "" {
		return Component{}, fmt.Errorf("component %q origin cannot declare both package and plugin membership", identity)
	}
	if input.Runtime != nil {
		runtime := *input.Runtime
		runtime.Name = strings.TrimSpace(runtime.Name)
		runtime.Version = strings.TrimSpace(runtime.Version)
		runtime.Target = strings.TrimSpace(runtime.Target)
		if runtime.Name == "" {
			return Component{}, fmt.Errorf("component %q runtime name is required", identity)
		}
		input.Runtime = &runtime
	}

	realization, materialization, err := chooseRealization(input)
	if err != nil {
		return Component{}, fmt.Errorf("component %q: %w", identity, err)
	}
	if realization == RealizationFused && input.Runtime != nil {
		return Component{}, fmt.Errorf("component %q fused realization cannot require a language runtime", identity)
	}
	if realization == RealizationSubprocess && (input.Language == "python" || input.Language == "node") && input.Runtime == nil {
		return Component{}, fmt.Errorf("component %q %s subprocess requires a runtime", identity, input.Language)
	}
	component := Component{
		Kind:            input.Kind,
		Name:            input.Name,
		Origin:          input.Origin,
		Language:        input.Language,
		Realization:     realization,
		Materialization: materialization,
	}
	if input.Runtime != nil {
		runtime := *input.Runtime
		component.Runtime = &runtime
	}
	return component, nil
}

func chooseRealization(input ComponentInput) (Realization, Materialization, error) {
	if input.External {
		if input.Materialization != MaterializationExternal {
			return "", "", fmt.Errorf("external realization requires external materialization")
		}
		return RealizationExternal, MaterializationExternal, nil
	}
	if input.Language == "go" && input.Fusible && !input.IsolationRequired {
		if input.Materialization != "" && input.Materialization != MaterializationBinary {
			return "", "", fmt.Errorf("fused realization requires binary materialization")
		}
		return RealizationFused, MaterializationBinary, nil
	}
	switch input.Materialization {
	case MaterializationBinary, MaterializationRelease, MaterializationAgentEnvironment, MaterializationExternal:
		return RealizationSubprocess, input.Materialization, nil
	case "":
		return "", "", fmt.Errorf("subprocess realization requires materialization")
	default:
		return "", "", fmt.Errorf("unsupported materialization %q", input.Materialization)
	}
}

func componentIdentity(kind ComponentKind, name string) (string, error) {
	kind = ComponentKind(strings.TrimSpace(string(kind)))
	name = strings.TrimSpace(name)
	if kind == "" {
		return "", fmt.Errorf("component kind is required")
	}
	switch kind {
	case ComponentKindExtension, ComponentKindMCP, ComponentKindHook:
	default:
		return "", fmt.Errorf("unsupported executable component kind %q", kind)
	}
	if name == "" {
		return "", fmt.Errorf("component name is required")
	}
	if strings.ContainsAny(string(kind), "/\\") || strings.ContainsAny(name, "/\\") {
		return "", fmt.Errorf("component kind and name cannot contain path separators")
	}
	return string(kind) + "/" + name, nil
}

func validateDigest(digest string) error {
	value, ok := strings.CutPrefix(digest, "sha256:")
	if !ok || len(value) != 64 {
		return fmt.Errorf("must be sha256:<64 lowercase hex characters>")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || hex.EncodeToString(decoded) != value {
		return fmt.Errorf("must be sha256:<64 lowercase hex characters>")
	}
	return nil
}
