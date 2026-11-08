// SPDX-License-Identifier: MIT

package upgrade_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/upgrade"
)

// A GrepOperations literal that still sets ReadFile loses that entry, whatever its place and layout: grep takes its context lines from ripgrep and never called it.
func TestGrepReadFileIsDeletedFromALiteral(t *testing.T) {
	isDir := `func isDir(string) (bool, error) { return true, nil }
`
	for name, c := range map[string]struct{ before, after string }{
		"last": {`var ops = &sdk.GrepOperations{
	IsDirectory: isDir,
	ReadFile: func(path string) (string, error) {
		b, err := os.ReadFile(path)
		return string(b), err
	},
}
`, `var ops = &sdk.GrepOperations{
	IsDirectory: isDir,
}
`},
		"first": {`var ops = sdk.GrepOperations{
	ReadFile:    func(string) (string, error) { return "", nil },
	IsDirectory: isDir,
}
`, `var ops = sdk.GrepOperations{
	IsDirectory: isDir,
}
`},
		"middle": {`var ops = sdk.GrepOperations{
	IsDirectory: isDir,
	ReadFile:    func(string) (string, error) { return "", nil }, // not called
	// kept
}
`, `var ops = sdk.GrepOperations{
	IsDirectory: isDir,
	// kept
}
`},
		"one line": {`var ops = sdk.GrepOperations{IsDirectory: isDir, ReadFile: func(string) (string, error) { return "", nil }}
`, `var ops = sdk.GrepOperations{IsDirectory: isDir}
`},
		"alone": {`var ops = sdk.GrepOperations{ReadFile: func(string) (string, error) { return "", nil }}
`, `var ops = sdk.GrepOperations{}
`},
		"no trailing comma": {`var ops = sdk.GrepOperations{
	IsDirectory: isDir,
	ReadFile: func(string) (string, error) { return "", nil }}
`, `var ops = sdk.GrepOperations{
	IsDirectory: isDir}
`},
	} {
		t.Run(name, func(t *testing.T) {
			result, _ := planSource(t, isDir+c.before)
			if len(result.Remaining) != 0 || len(result.Skipped) != 0 || len(result.Errors) != 0 {
				t.Fatalf("remaining=%v skipped=%v errors=%v", result.Remaining, result.Skipped, result.Errors)
			}
			if got := rewritten(t, result); got != isDir+c.after {
				t.Fatalf("rewritten:\n%s\nwant:\n%s", got, isDir+c.after)
			}
			drifts := result.Drift()
			if len(drifts) != 1 || drifts[0].Rule != upgrade.RuleGrepReadFile || drifts[0].Symbol != "GrepOperations.ReadFile" || !drifts[0].Rewrites || drifts[0].Old != "func(string) (string, error)" || drifts[0].New != "removed" || drifts[0].Since != "0.8.0" {
				t.Fatalf("drift = %+v", drifts)
			}
		})
	}
}

// A call of ops.ReadFile, and a positional literal that still lists it, have no mechanical rewrite: the diagnosis names the removed member with its remedy.
func TestGrepReadFileUsesWithoutARewriteAreNamed(t *testing.T) {
	for name, body := range map[string]string{
		"call": `func read(ops *sdk.GrepOperations) (string, error) { return ops.ReadFile("x") }
`,
		"positional literal": `var ops = sdk.GrepOperations{func(string) (bool, error) { return true, nil }, func(string) (string, error) { return "", nil }}
`,
	} {
		t.Run(name, func(t *testing.T) {
			result, _ := planSource(t, body)
			if result.Changed() || len(result.Remaining) != 1 {
				t.Fatalf("result = %+v", result)
			}
			drift := result.Remaining[0]
			if drift.Rule != upgrade.RuleGrepReadFile || drift.Symbol != "GrepOperations.ReadFile" || drift.New != "removed" {
				t.Fatalf("drift = %+v", drift)
			}
			if drifts := result.Drift(); len(drifts) != 1 || drifts[0].Rewrites {
				t.Fatalf("a use left for the author must not read as rewritten: %+v", drifts)
			}
		})
	}
}
