// pig-specific: serves the classes an extension builds around a Pi session (SessionManager, SettingsManager, DefaultResourceLoader, DefaultPackageManager, ProjectTrustStore, ModelRuntime) to the Go, Python and Rust extension SDKs. The Node runtime serves Pi's own classes.

package coding

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// SessionGraph is the host's SessionGraphBackend: it builds the objects extensions construct and answers their static functions.
type SessionGraph struct{}

// NewSessionGraph returns the backend to bind with Host.SetSessionGraph.
func NewSessionGraph() *SessionGraph { return &SessionGraph{} }

var _ subprocess.SessionGraphBackend = (*SessionGraph)(nil)

// New implements subprocess.SessionGraphBackend.
func (*SessionGraph) New(_ context.Context, kind, op string, args json.RawMessage) (subprocess.SessionGraphObject, error) {
	switch kind {
	case subprocess.SessionGraphSessionManager:
		return icodingagent.NewGraphSessionManager(op, args)
	}
	return nil, fmt.Errorf("the host does not construct %s", kind)
}

// Static implements subprocess.SessionGraphBackend.
func (*SessionGraph) Static(ctx context.Context, kind, method string, args json.RawMessage) (any, error) {
	switch kind {
	case subprocess.SessionGraphSessionManager:
		return icodingagent.GraphSessionManagerStatic(ctx, method, args)
	}
	return nil, fmt.Errorf("%s has no static function %s", kind, method)
}
