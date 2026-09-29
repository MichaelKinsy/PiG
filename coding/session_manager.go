// Ports packages/coding-agent/src/core/session-manager.ts.

package coding

import (
	"fmt"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// SessionManager is the actual append-only Session log used by the runtime. Sharing this pointer shares entry identity, branching and persistence, not a copied projection.
type SessionManager = icodingagent.Session

// NewInMemorySessionManager creates a Session log with no persistence path. An empty cwd resolves to the process working directory.
func NewInMemorySessionManager(cwd string) (*SessionManager, error) {
	resolved, err := icodingagent.ResolvePath(cwd, "")
	if err != nil {
		return nil, fmt.Errorf("session manager: resolve cwd: %w", err)
	}
	id, err := icodingagent.GenerateSessionID()
	if err != nil {
		return nil, err
	}
	return icodingagent.NewSession(id, resolved), nil
}

// SessionManager returns the backing log. A supplied manager is used directly; appends through either handle are visible through the other.
func (s *Session) SessionManager() *SessionManager { return s.inner }
