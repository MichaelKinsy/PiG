package experimental

// Ports packages/coding-agent/src/experimental/server.ts

import (
	"path/filepath"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// ResolveSessionDirectory uses the experimental storage directory under the selected agent directory by default. Explicit paths retain Pi's cwd-relative, tilde, and file-URL resolution.
func ResolveSessionDirectory(sessionDir *string) (string, error) {
	path := filepath.Join(codingagent.AgentDir(), "experimental", "sessions")
	if sessionDir != nil {
		path = *sessionDir
	}
	return codingagent.ResolvePath(path, "")
}
