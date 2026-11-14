// SPDX-License-Identifier: MIT

// pig additive (D109): Pi has no SDK to drift from; see docs/additive-features.md.

// Package plan is the part of package upgrade that needs no type checker: the
// plan an upgrade makes ([Result]), the request and the reply of the driver
// that makes it ([LoadOptions], [Reply]), the rule behind a change
// ([RuleByID]), the diff of a rewritten file ([Diff]), and the classification
// of a failed build as SDK drift ([ParseDiagnostics], [Classify]).
//
// Every name is the name of package upgrade, and the types are the same types.
//
// The pig command imports this package and not package upgrade, so the pig
// binary does not link go/types (docs/parity/divergences/D272.md). The type
// check is the program upgrade/driver, which pig builds from the staged SDK.
package plan

import impl "github.com/MichaelKinsy/PiG/extensions/sdk/upgrade/internal/upgrade"

// Change is one breaking change to a public SDK symbol.
type Change = impl.Change

// Rule is one breaking SDK change and its remedy.
type Rule = impl.Rule

// RuleByID returns the rule with the given ID.
func RuleByID(id string) (Rule, bool) { return impl.RuleByID(id) }

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

// LoadOptions selects the packages the driver reads.
type LoadOptions = impl.LoadOptions

// Rewrite is one change the plan makes to a file.
type Rewrite = impl.Rewrite

// FileChange is one rewritten file, or one the plan adds: Created is set for a
// file with no Before.
type FileChange = impl.FileChange

// Skipped is a use of a rewritable change that needs a hand edit.
type Skipped = impl.Skipped

// Result is what an upgrade does to an extension's source, and what it leaves
// for the author.
type Result = impl.Result

// Reply is what the analysis driver writes for one [LoadOptions]: the plan, or
// the error that stopped it.
type Reply = impl.Reply
