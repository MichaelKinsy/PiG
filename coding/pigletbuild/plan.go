// Package pigletbuild hosts Piglet Binary planning while orchestration is
// moved to the final Piglet artifact package. The planner is factual: compatible
// Go factories fuse; every other extension is an exact subprocess component.
package pigletbuild

import (
	"crypto/ed25519"
	"fmt"
)

// Language is one extension implementation language.
type Language string

const (
	Go     Language = "go"
	Rust   Language = "rust"
	Python Language = "python"
)

// Decision is how one extension executes in the Piglet Binary.
type Decision string

const (
	DecisionFuse    Decision = "fuse"
	DecisionSidecar Decision = "sidecar"
)

// Compat describes whether a target/component pair is available without a
// target-side build toolchain.
type Compat string

const (
	CompatNative      Compat = "native"
	CompatHostReliant Compat = "host-reliant"
	CompatUnavailable Compat = "unavailable"
)

// Target is an os/arch build target.
type Target struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

func (t Target) String() string { return t.OS + "/" + t.Arch }

// ExtensionInput contains the runtime-cell facts needed for one decision.
type ExtensionInput struct {
	Name             string
	Language         Language
	Fusible          bool
	NotFusibleReason string
}

// Sandbox describes builder cross-target capabilities.
type Sandbox struct {
	Native    Target
	RustCross bool
	PyCross   bool
}

// Options configures one Piglet output build. Component realization is not
// configurable; it is derived from component facts.
type Options struct {
	Format        string
	Locked        bool
	Record        string
	Targets       []Target
	Sandbox       Sandbox
	BakedSettings []byte
	Version       string
	Builder       string
	Verification  string
	Workspace     string
	// SignKeyPath is the ed25519 private key a Piglet Binary is signed with.
	SignKeyPath string
	// SignKey is SignKeyPath's key, read before any build work starts.
	SignKey ed25519.PrivateKey
}

// Warning is one actionable planning diagnostic.
type Warning struct {
	Level     string `json:"level"`
	Code      string `json:"code"`
	Target    string `json:"target,omitempty"`
	Extension string `json:"extension,omitempty"`
	Message   string `json:"message"`
	Remedy    string `json:"remedy,omitempty"`
}

// ExtPlan is one extension's realization and target compatibility.
type ExtPlan struct {
	Extension string            `json:"extension"`
	Decision  Decision          `json:"decision"`
	Reason    string            `json:"reason,omitempty"`
	Compat    map[string]Compat `json:"compat"`
}

// Plan is the deterministic Piglet Binary delivery plan.
type Plan struct {
	Targets  []string  `json:"targets"`
	Ext      []ExtPlan `json:"plan"`
	Warnings []Warning `json:"warnings"`
}

func compatFor(lang Language, target Target, sandbox Sandbox) Compat {
	if target == sandbox.Native {
		return CompatNative
	}
	switch lang {
	case Go:
		return CompatNative
	case Rust:
		if sandbox.RustCross {
			return CompatNative
		}
	case Python:
		if sandbox.PyCross {
			return CompatNative
		}
	}
	return CompatHostReliant
}

func remedyFor(lang Language) string {
	switch lang {
	case Rust:
		return "select a builder with the matching Rust target, or build for the builder's native target"
	case Python:
		return "use a Piglet Image with the matching Python runtime, or build for the builder's native target"
	default:
		return "select a builder that supports this target"
	}
}

// BuildPlan derives realization and target compatibility from extension facts.
func BuildPlan(extensions []ExtensionInput, options Options) Plan {
	targets := make([]string, len(options.Targets))
	for i, target := range options.Targets {
		targets[i] = target.String()
	}
	plan := Plan{Targets: targets}
	for _, extension := range extensions {
		entry := ExtPlan{Extension: extension.Name, Compat: make(map[string]Compat, len(options.Targets))}
		if extension.Fusible {
			entry.Decision = DecisionFuse
		} else {
			entry.Decision = DecisionSidecar
			entry.Reason = extension.NotFusibleReason
		}
		for _, target := range options.Targets {
			compatibility := CompatNative
			if entry.Decision != DecisionFuse {
				compatibility = compatFor(extension.Language, target, options.Sandbox)
			}
			entry.Compat[target.String()] = compatibility
			if compatibility == CompatHostReliant {
				plan.Warnings = append(plan.Warnings, Warning{
					Level:     "warn",
					Code:      "host-reliant-target",
					Target:    target.String(),
					Extension: extension.Name,
					Message:   fmt.Sprintf("no prebuilt %s cell for %s; a Piglet Image or native-target build is required", extension.Language, target),
					Remedy:    remedyFor(extension.Language),
				})
			}
		}
		plan.Ext = append(plan.Ext, entry)
	}
	return plan
}

// Verdict reports whether resolution produced a complete buildable Piglet.
type Verdict struct {
	OK       bool     `json:"ok"`
	Blockers []string `json:"blockers,omitempty"`
}

// Validate rejects unresolved or empty Piglet extension sets and enforces an
// authored realization requirement. Advisory target compatibility warnings are
// represented in the plan and do not masquerade as a successful self-contained
// target.
func Validate(plan Plan, resolutionWarnings []string, extensionCount int, requireFused bool) Verdict {
	blockers := make([]string, 0, len(resolutionWarnings)+1)
	for _, warning := range resolutionWarnings {
		blockers = append(blockers, "unresolved extension: "+warning)
	}
	if extensionCount == 0 {
		blockers = append(blockers, "piglet resolves to no extensions")
	}
	for _, extension := range plan.Ext {
		if !requireFused || extension.Decision == DecisionFuse {
			continue
		}
		reason := ""
		if extension.Reason != "" {
			reason = ": " + extension.Reason
		}
		blockers = append(blockers, fmt.Sprintf("extension %q resolves to %s%s; build.extensionRealization requires fused", extension.Extension, extension.Decision, reason))
	}
	return Verdict{OK: len(blockers) == 0, Blockers: blockers}
}
