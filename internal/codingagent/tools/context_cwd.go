package tools

import (
	"context"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// toolCWD selects the invocation context's cwd before the definition's fallback.
// Ports packages/coding-agent/src/core/tools/read.ts.
func toolCWD(ctx context.Context, fallback string) (string, error) {
	if ext := extension.FromContext(ctx); ext != nil {
		cwd, err := ext.CWD()
		if err != nil {
			return "", err
		}
		if cwd != "" {
			return cwd, nil
		}
	}
	return fallback, nil
}
