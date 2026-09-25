// bash_executor.go: type aliases and shim for the !cmd interactive path.
//
// User bash execution lives in internal/codingagent/tools/bash_executor.go
// (upstream core/bash-executor.ts executeBashWithOperations). This file
// re-exports its types so interactive code can call it without qualifying
// the tools sub-package.
package codingagent

import (
	"context"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// BashResult is a type alias for tools.BashResult.
// All fields are identical; existing callers compile without changes.
type BashResult = tools.BashResult

// BashExecOptions is a type alias for tools.BashExecOptions.
type BashExecOptions = tools.BashExecOptions

// ExecuteBash runs command under shell in cwd. Delegates to tools.ExecuteBash.
// See tools/bash_executor.go for the canonical implementation.
func ExecuteBash(ctx context.Context, command, cwd string, shell tools.ShellConfig, opts BashExecOptions) (BashResult, error) {
	return tools.ExecuteBash(ctx, command, cwd, shell, opts)
}
