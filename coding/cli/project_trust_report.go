package cli

import (
	"context"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Project trust states `pig status` and `pig diagnose` report.
const (
	projectTrustTrusted     = "trusted"
	projectTrustUntrusted   = "untrusted"
	projectTrustNotRequired = "not required"
)

// projectTrustReport is the trust decision a non-interactive run makes for the working directory.
//
// pig additive (D41): status and diagnose show whether project settings and resources apply.
type projectTrustReport struct {
	// State is projectTrustTrusted, projectTrustUntrusted or projectTrustNotRequired.
	State string `json:"state"`
	// Reason names what decided it.
	Reason string `json:"reason"`
	// Error is the failure that stopped the decision, which leaves the project untrusted.
	Error string `json:"error,omitempty"`
}

// Trusted reports whether project settings and resources apply.
func (r projectTrustReport) Trusted() bool { return r.State != projectTrustUntrusted }

// reportProjectTrust resolves project trust the way a print-mode run does: the saved decision, then defaultProjectTrust, and
// otherwise untrusted because nothing can ask. It cannot run the project_trust handlers of extensions or see a per-run
// --approve, so the reason says so when it matters.
func reportProjectTrust(cwd, agentDir string) projectTrustReport {
	globalSettings := codingagent.NewSettingsManagerWithOptions(cwd, agentDir, codingagent.SettingsManagerCreateOptions{ProjectTrusted: new(false)})
	defaultTrust := string(globalSettings.GetDefaultProjectTrust())
	store := codingagent.NewProjectTrustStore(agentDir)
	trusted, err := resolveProjectTrusted(context.Background(), projectTrustResolutionOptions{
		CWD: cwd, Store: store, Default: defaultTrust, UI: extension.NoopUIContext,
	})
	if err != nil {
		return projectTrustReport{State: projectTrustUntrusted, Reason: "the trust decision could not be read", Error: err.Error()}
	}
	if !codingagent.HasTrustRequiringProjectResources(cwd) {
		return projectTrustReport{State: projectTrustNotRequired, Reason: "the project has no settings or resources that need trust"}
	}
	state := projectTrustUntrusted
	if trusted {
		state = projectTrustTrusted
	}
	if decision, err := store.Get(cwd); err == nil && decision != nil {
		return projectTrustReport{State: state, Reason: "saved decision for this folder"}
	}
	switch defaultTrust {
	case string(codingagent.DefaultProjectTrustAlways), string(codingagent.DefaultProjectTrustNever):
		return projectTrustReport{State: state, Reason: "defaultProjectTrust is " + defaultTrust}
	}
	return projectTrustReport{State: state, Reason: "no saved decision and defaultProjectTrust is ask; print, JSON and RPC runs treat the project as untrusted, an interactive run asks. This report cannot see an extension's project_trust handler or a --approve flag"}
}
