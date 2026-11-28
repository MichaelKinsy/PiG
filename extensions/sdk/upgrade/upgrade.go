// SPDX-License-Identifier: MIT

// pig additive (D109): Pi has no SDK to drift from; see docs/additive-features.md.

// Package upgrade rewrites Go extensions written for an older SDK so they build
// against this one. Each rule is one breaking SDK change: the rules sit beside
// the SDK so a breaking change and its rewrite land together, and the
// apidiff gate (automation/ci/sdkapidiff) fails a break that has no rule.
//
// A rule can classify a compiler diagnostic ([Classify]), name the change with
// its old and new shape, and, when the change has one correct mechanical form,
// rewrite the source ([Plan]). The rewrites read the extension's types and edit
// its syntax tree; they never match source text.
//
// The names in this file read no types. They are declared in an internal package
// and are the same names in package plan, which a program imports in place of
// this package when it must not link go/types.
package upgrade

import impl "github.com/MichaelKinsy/PiG/extensions/sdk/upgrade/internal/upgrade"

// SDKModulePath is the import path of the SDK, and LegacySDKModulePath the
// path it carried in earlier releases. An extension may import either.
const (
	SDKModulePath       = impl.SDKModulePath
	LegacySDKModulePath = impl.LegacySDKModulePath
)

// IsSDKPath reports whether path is an SDK import path.
func IsSDKPath(path string) bool { return impl.IsSDKPath(path) }

// Change is one breaking change to a public SDK symbol.
type Change = impl.Change

// Rule is one breaking SDK change and its remedy.
type Rule = impl.Rule

// Rule IDs.
const (
	RuleContextGetters      = impl.RuleContextGetters
	RuleOptionalBool        = impl.RuleOptionalBool
	RuleContextUsage        = impl.RuleContextUsage
	RuleEditorComponent     = impl.RuleEditorComponent
	RuleConstrainedSampling = impl.RuleConstrainedSampling
	RuleAutocompleteFactory = impl.RuleAutocompleteFactory
	RuleNotComparable       = impl.RuleNotComparable
	RuleTransformHeaders    = impl.RuleTransformHeaders
	RuleProviderCredential  = impl.RuleProviderCredential
	RuleSystemPromptGetter  = impl.RuleSystemPromptGetter
	RuleReadonlySession     = impl.RuleReadonlySession
	RuleModelRegistryFind   = impl.RuleModelRegistryFind
	RuleDialogResults       = impl.RuleDialogResults
	RuleHostHighlightCode   = impl.RuleHostHighlightCode
	RuleMarkdownTransformer = impl.RuleMarkdownTransformer
	RuleSetModelObject      = impl.RuleSetModelObject
	RuleSettingsNotices         = impl.RuleSettingsNotices
	RuleInstallTelemetryRemoved = impl.RuleInstallTelemetryRemoved
)

// Rules returns every rule, in the order the SDK changed.
func Rules() []Rule { return impl.Rules() }

// RuleByID returns the rule with the given ID.
func RuleByID(id string) (Rule, bool) { return impl.RuleByID(id) }

// Covers reports whether a rule covers the symbol an apidiff report names.
// apidiff writes a method as "Context.GetSessionID" or "(*Extension).Tool".
func Covers(symbol string) bool { return impl.Covers(symbol) }

// Diagnostic is one positioned compiler message.
type Diagnostic = impl.Diagnostic

// ParseDiagnostics reads the positioned messages of `go build` or `go vet`
// output: `path/file.go:12:9: message`. Package headers, `#` lines and
// continuation lines are not diagnostics.
func ParseDiagnostics(output []byte) []Diagnostic { return impl.ParseDiagnostics(output) }

// Drift is one SDK change an extension was written for the old shape of.
type Drift = impl.Drift

// FileSource reads a source file named by a diagnostic.
type FileSource = impl.FileSource

// Classify maps compiler diagnostics that name an SDK symbol to the SDK change
// behind them. A diagnostic no rule recognizes is not drift: the caller keeps
// the compiler's own message for it. Uses of one symbol are one Drift, in the
// order the compiler reported them.
func Classify(diagnostics []Diagnostic, source FileSource) []Drift {
	return impl.Classify(diagnostics, source)
}

// Diff returns the unified diff of a file's rewrite, or the empty string when
// the file is unchanged. name labels both sides.
func Diff(name string, before, after []byte) string { return impl.Diff(name, before, after) }

// LoadOptions selects the packages to read.
type LoadOptions = impl.LoadOptions

// Rewrite is one change the plan makes to a file.
type Rewrite = impl.Rewrite

// FileChange is one rewritten file, or one the plan adds: Created is set for a
// file with no Before.
type FileChange = impl.FileChange

// Skipped is a use of a rewritable change that needs a hand edit.
type Skipped = impl.Skipped

// Result is what an upgrade does to an extension's source, and what it leaves
// for the author. The lines that Skipped, Remaining and Errors name are lines of
// the source before the rewrites: Skipped and Remaining are uses the compiler
// still rejects, and an upgrade that does not build restores that source.
type Result = impl.Result

// Reply is what the analysis driver (command driver in this module) writes for one [LoadOptions]: the plan, or the error that stopped it. The pig command reads it in place of calling [Open], so it does not link the type checker.
type Reply = impl.Reply
